package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"videolib/db"
	"videolib/handlers"
	"videolib/hasher"
	"videolib/models"
	"videolib/scanner"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: videolib <command> [options] [arguments]

Commands:
  serve        Start the web server
  scan         Add videos to the database
  scrub        Remove stale database entries and orphaned thumbnails
  list         Export video database in JSON or text format
  tags         Add or remove tags for videos
  hash         Compute content hash of files
  title        Set video title
  description  Set video description

Run 'videolib <command> -help' for command-specific options.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	command := os.Args[1]
	if command == "-help" || command == "--help" || command == "-h" {
		usage()
		os.Exit(0)
	}
	switch command {
	case "serve":
		cmdServe(os.Args[2:])
	case "scan":
		cmdScan(os.Args[2:])
	case "scrub":
		cmdScrub(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "tags":
		cmdTags(os.Args[2:])
	case "hash":
		cmdHash(os.Args[2:])
	case "title":
		cmdTitle(os.Args[2:])
	case "description":
		cmdDescription(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		usage()
		os.Exit(1)
	}
}

// === serve ===

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: videolib serve [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}
	dbPath := fs.String("db", "videolib.db", "Database file path")
	thumbDir := fs.String("thumbs", "thumbnails", "Thumbnail directory")
	addr := fs.String("addr", ":8080", "Listen address")
	title := fs.String("title", "Video Library", "Web application title")
	readOnly := fs.Bool("readonly", false, "Read-only mode (disable all editing)")
	fs.Parse(args)

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	if *readOnly {
		log.Println("Starting in READ-ONLY mode")
	}

	startServer(*addr, database, *thumbDir, *title, *readOnly)
}

// === scan ===

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: videolib scan [options] <path> [<path> ...]\n\nOptions:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDefault video extensions: %s\n", defaultExtList())
	}
	dbPath := fs.String("db", "videolib.db", "Database file path")
	thumbDir := fs.String("thumbs", "thumbnails", "Thumbnail directory")
	extList := fs.String("ext", "", "Override video extensions (comma-separated)")
	addExt := fs.String("add-ext", "", "Add extra extensions to defaults (comma-separated)")
	dupeLogFile := fs.String("dupe-log", "", "log duplicate video files to this file")
	fs.Parse(args)
	paths := fs.Args()
	if len(paths) == 0 { fs.Usage(); os.Exit(1) }
	os.MkdirAll(*thumbDir, 0755)
	database, err := db.Open(*dbPath)
	if err != nil { log.Fatalf("Cannot open database: %v", err) }
	defer database.Close()
	var dupeLog *os.File
	dupeLog, _ = os.Create(*dupeLogFile)
	defer dupeLog.Close()
	var overrideExts, extraExts []string
	if *extList != "" { overrideExts = strings.Split(*extList, ",") }
	if *addExt != "" { extraExts = strings.Split(*addExt, ",") }
	var tA, tU, tS, tE int
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil { log.Printf("Cannot access %s: %v", path, err); tE++; continue }
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".json") {
			a, u, s := importJSON(database, path); tA += a; tU += u; tS += s; continue
		}
		var s *scanner.Scanner
		if info.IsDir() {
			s = scanner.New(database, []string{filepath.Clean(path)}, *thumbDir, dupeLog)
		} else {
			s = scanner.New(database, []string{filepath.Dir(filepath.Clean(path))}, *thumbDir, dupeLog)
			s.SetFileFilter(filepath.Base(path))
		}
		if len(overrideExts) > 0 { s.SetExtensions(overrideExts) }
		if len(extraExts) > 0 { s.AddExtensions(extraExts) }
		result, err := s.Scan()
		if err != nil { log.Printf("Scan error for %s: %v", path, err); tE++; continue }
		tA += result.Added; tU += result.Updated; tS += result.Skipped; tE += result.Errors
		log.Printf("Scanned %s: %d added, %d updated, %d skipped, %d errors", path, result.Added, result.Updated, result.Skipped, result.Errors)
	}
	log.Printf("Scan complete: %d added, %d updated, %d skipped, %d errors", tA, tU, tS, tE)
}

func importJSON(database *db.Database, path string) (int, int, int) {
	log.Printf("Importing JSON: %s", path)
	jsonBytes, err := os.ReadFile(path)
	if err != nil { log.Printf("Cannot read %s: %v", path, err); return 0, 0, 0 }
	var data models.ExportData
	if err := json.Unmarshal(jsonBytes, &data); err != nil { log.Printf("Invalid JSON: %v", err); return 0, 0, 0 }
	a, u, s, _ := database.Import(&data)
	log.Printf("Imported %s: %d added, %d updated, %d skipped", path, a, u, s)
	return a, u, s
}

func defaultExtList() string {
	var exts []string
	for ext := range scanner.DefaultVideoExtensions { exts = append(exts, ext) }
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// === scrub ===

func cmdScrub(args []string) {
	fs := flag.NewFlagSet("scrub", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "Usage: videolib scrub [options]\n\nOptions:\n"); fs.PrintDefaults() }
	dbPath := fs.String("db", "videolib.db", "Database file path")
	thumbDir := fs.String("thumbs", "thumbnails", "Thumbnail directory")
	noVideos := fs.Bool("no-videos", false, "Skip removing stale database entries")
	noThumbs := fs.Bool("no-thumbs", false, "Skip removing orphaned thumbnails")
	dryRun := fs.Bool("dry-run", false, "Show what would be done without making changes")
	fs.Parse(args)
	database, err := db.Open(*dbPath)
	if err != nil { log.Fatalf("Cannot open database: %v", err) }
	defer database.Close()
	if !*noVideos { scrubVideos(database, *dryRun) }
	if !*noThumbs { scrubThumbnails(database, *thumbDir, *dryRun) }
}

func scrubVideos(database *db.Database, dryRun bool) {
	log.Println("Checking for stale database entries...")
	paths, _ := database.GetAllPaths()
	var removed int
	for path, hash := range paths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if dryRun { log.Printf("[DRY RUN] Would remove: %s", path); removed++ } else {
				log.Printf("Removing: %s", path); database.DeleteVideo(hash); removed++
			}
		}
	}
	if dryRun { log.Printf("Dry run: %d stale entries", removed) } else { log.Printf("Removed %d stale entries", removed) }
}

func scrubThumbnails(database *db.Database, thumbDir string, dryRun bool) {
	log.Println("Checking for orphaned thumbnails...")
	hashes, _ := database.GetAllHashes()
	entries, err := os.ReadDir(thumbDir)
	if err != nil { return }
	var removed int
	for _, e := range entries {
		if !e.IsDir() { continue }
		if !hashes[e.Name()] {
			dir := filepath.Join(thumbDir, e.Name())
			if dryRun { log.Printf("[DRY RUN] Would remove: %s", dir); removed++ } else {
				log.Printf("Removing: %s", dir); os.RemoveAll(dir); removed++
			}
		}
	}
	if dryRun { log.Printf("Dry run: %d orphaned dirs", removed) } else { log.Printf("Removed %d orphaned dirs", removed) }
}

// === list ===

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "Usage: videolib list [options] [search query]\n\nOptions:\n"); fs.PrintDefaults() }
	dbPath := fs.String("db", "videolib.db", "Database file path")
	format := fs.String("format", "text", "Output format: json, text")
	output := fs.String("output", "-", "Output file (- for stdout)")
	fs.Parse(args)
	search := strings.Join(fs.Args(), " ")
	database, err := db.Open(*dbPath)
	if err != nil { log.Fatalf("Cannot open database: %v", err) }
	defer database.Close()
	var videos []*models.Video
	if search != "" { videos, err = database.SearchByQuery(search) } else { videos, err = database.ListAllVideos() }
	if err != nil { log.Fatalf("Error: %v", err) }
	var w *os.File
	if *output == "-" { w = os.Stdout } else { w, _ = os.Create(*output); defer w.Close() }
	switch *format {
	case "json":
		enc := json.NewEncoder(w); enc.SetIndent("", "  ")
		enc.Encode(&models.ExportData{Version: 1, Exported: time.Now().UTC().Format(time.RFC3339), Videos: videos})
	case "text":
		for _, v := range videos {
			tags := strings.Join(v.Tags, ","); if tags == "" { tags = "-" }
			fmt.Fprintf(w, "%s\t%s\t%s\n", v.Hash, tags, v.Path)
		}
	default:
		log.Fatalf("Unknown format: %s", *format)
	}
	if *output != "-" { log.Printf("Written %d videos to %s", len(videos), *output) }
}

// === tags ===

type stringSlice []string
func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(v string) error {
	v = strings.ToLower(strings.TrimSpace(v)); v = strings.ReplaceAll(v, " ", "")
	if v == "" { return fmt.Errorf("empty tag") }
	*s = append(*s, v); return nil
}

func cmdTags(args []string) {
	fs := flag.NewFlagSet("tags", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "Usage: videolib tags [options] [<hash> ...]\n\nOptions:\n"); fs.PrintDefaults() }
	dbPath := fs.String("db", "videolib.db", "Database file path")
	var addTags, removeTags stringSlice
	fs.Var(&addTags, "add", "Tag to add (repeatable)")
	fs.Var(&removeTags, "remove", "Tag to remove (repeatable)")
	fs.Parse(args)
	if len(addTags) == 0 && len(removeTags) == 0 { fs.Usage(); os.Exit(1) }
	database, err := db.Open(*dbPath)
	if err != nil { log.Fatalf("Cannot open database: %v", err) }
	defer database.Close()
	var hashInputs []string
	if fs.NArg() > 0 { hashInputs = fs.Args() } else { hashInputs = readHashesFromStdin() }
	if len(hashInputs) == 0 { log.Fatalf("No hashes provided") }
	var hashes []string
	for _, h := range hashInputs { r, err := resolveHash(database, h); if err != nil { log.Printf("Warning: %v", err); continue }; hashes = append(hashes, r) }
	if len(hashes) == 0 { log.Fatalf("No valid videos found") }
	log.Printf("Applying to %d video(s)...", len(hashes))
	if len(removeTags) > 0 { database.BulkRemoveTags(hashes, []string(removeTags)); log.Printf("Removed: %s", strings.Join(removeTags, ", ")) }
	if len(addTags) > 0 { database.BulkAddTags(hashes, []string(addTags)); log.Printf("Added: %s", strings.Join(addTags, ", ")) }
	for _, h := range hashes { v, _ := database.GetVideo(h); tags := strings.Join(v.Tags, ","); if tags == "" { tags = "-" }; fmt.Printf("%s\t%s\t%s\n", v.Hash, tags, v.Path) }
}

// === title ===

func cmdTitle(args []string) {
	fs := flag.NewFlagSet("title", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib title [options] <hash> [title text]

Set or clear the title of a video. If no title text is given and -file
is not specified, the title is cleared.

Options:
`)
		fs.PrintDefaults()
	}
	dbPath := fs.String("db", "videolib.db", "Database file path")
	fromFile := fs.String("file", "", "Read title from file")
	fs.Parse(args)

	if fs.NArg() < 1 { fs.Usage(); os.Exit(1) }
	hashPrefix := fs.Arg(0)

	database, err := db.Open(*dbPath)
	if err != nil { log.Fatalf("Cannot open database: %v", err) }
	defer database.Close()

	hash, err := resolveHash(database, hashPrefix)
	if err != nil { log.Fatalf("Error: %v", err) }

	var title string
	if *fromFile != "" {
		data, err := os.ReadFile(*fromFile)
		if err != nil { log.Fatalf("Cannot read file: %v", err) }
		title = strings.TrimSpace(string(data))
	} else if fs.NArg() > 1 {
		title = strings.Join(fs.Args()[1:], " ")
	}

	if err := database.SetTitle(hash, title); err != nil {
		log.Fatalf("Error: %v", err)
	}

	v, _ := database.GetVideo(hash)
	if v.Title != "" {
		fmt.Printf("%s\ttitle: %s\t%s\n", v.Hash, v.Title, v.Path)
	} else {
		fmt.Printf("%s\ttitle: (cleared)\t%s\n", v.Hash, v.Path)
	}
}

// === description ===

func cmdDescription(args []string) {
	fs := flag.NewFlagSet("description", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib description [options] <hash> [description text]

Set or clear the description of a video. Use -file to read from a file
(useful for markdown content). If no text is given and -file is not
specified, the description is cleared.

Options:
`)
		fs.PrintDefaults()
	}
	dbPath := fs.String("db", "videolib.db", "Database file path")
	fromFile := fs.String("file", "", "Read description from file (supports markdown)")
	fs.Parse(args)

	if fs.NArg() < 1 { fs.Usage(); os.Exit(1) }
	hashPrefix := fs.Arg(0)

	database, err := db.Open(*dbPath)
	if err != nil { log.Fatalf("Cannot open database: %v", err) }
	defer database.Close()

	hash, err := resolveHash(database, hashPrefix)
	if err != nil { log.Fatalf("Error: %v", err) }

	var desc string
	if *fromFile != "" {
		data, err := os.ReadFile(*fromFile)
		if err != nil { log.Fatalf("Cannot read file: %v", err) }
		desc = string(data)
	} else if fs.NArg() > 1 {
		desc = strings.Join(fs.Args()[1:], " ")
	}

	if err := database.SetDescription(hash, desc); err != nil {
		log.Fatalf("Error: %v", err)
	}

	v, _ := database.GetVideo(hash)
	if v.Description != "" {
		lines := strings.Count(v.Description, "\n") + 1
		fmt.Printf("%s\tdescription: %d lines\t%s\n", v.Hash, lines, v.Path)
	} else {
		fmt.Printf("%s\tdescription: (cleared)\t%s\n", v.Hash, v.Path)
	}
}

// === hash ===

func cmdHash(args []string) {
	fs := flag.NewFlagSet("hash", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "Usage: videolib hash [<file> ...]\n\n"); fs.PrintDefaults() }
	fs.Parse(args)
	var files []string
	if fs.NArg() > 0 { files = fs.Args() } else {
		s := bufio.NewScanner(os.Stdin)
		for s.Scan() { l := strings.TrimSpace(s.Text()); if l != "" { files = append(files, l) } }
	}
	if len(files) == 0 { fs.Usage(); os.Exit(1) }
	for _, f := range files {
		h, err := hasher.HashFile(f)
		if err != nil { fmt.Println("NOTFOUND") } else { fmt.Println(h) }
	}
}

// === helpers ===

func readHashesFromStdin() []string {
	var hashes []string
	s := bufio.NewScanner(os.Stdin)
	for s.Scan() { l := strings.TrimSpace(s.Text()); if l != "" { f := strings.Fields(l); if len(f) > 0 { hashes = append(hashes, f[0]) } } }
	return hashes
}

func resolveHash(database *db.Database, prefix string) (string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if len(prefix) < 8 { return "", fmt.Errorf("hash prefix too short: %s", prefix) }
	if _, err := database.GetVideo(prefix); err == nil { return prefix, nil }
	all, err := database.GetAllHashes()
	if err != nil { return "", err }
	var matches []string
	for h := range all { if strings.HasPrefix(h, prefix) { matches = append(matches, h) } }
	switch len(matches) {
	case 0: return "", fmt.Errorf("not found: %s", prefix)
	case 1: return matches[0], nil
	default: return "", fmt.Errorf("ambiguous prefix '%s' (%d matches)", prefix, len(matches))
	}
}

// === web server ===

func startServer(addr string, database *db.Database, thumbDir, title string, readOnly bool) {
	mux := http.NewServeMux()

	apiHandler := handlers.NewAPIHandler(database, readOnly)
	mux.HandleFunc("GET /api/config", apiHandler.GetConfig)
	mux.HandleFunc("GET /api/videos", apiHandler.ListVideos)
	mux.HandleFunc("GET /api/videos/{hash}", apiHandler.GetVideo)
	mux.HandleFunc("POST /api/videos/{hash}/tags", apiHandler.AddTags)
	mux.HandleFunc("PUT /api/videos/{hash}/tags", apiHandler.SetTags)
	mux.HandleFunc("DELETE /api/videos/{hash}/tags", apiHandler.RemoveTags)
	mux.HandleFunc("PUT /api/videos/{hash}/main-thumb", apiHandler.SetMainThumb)
	mux.HandleFunc("PUT /api/videos/{hash}/title", apiHandler.SetTitle)
	mux.HandleFunc("PUT /api/videos/{hash}/description", apiHandler.SetDescription)
	mux.HandleFunc("POST /api/bulk/tags", apiHandler.BulkTags)
	mux.HandleFunc("GET /api/tags", apiHandler.ListTags)

	thumbListHandler := &handlers.ThumbListHandler{ThumbDir: thumbDir}
	mux.Handle("GET /api/thumbs/{hash}", thumbListHandler)
	mux.Handle("GET /media/{hash}", handlers.NewVideoFileHandler(database))
	mux.Handle("GET /thumbs/{hash}/{filename}", handlers.NewThumbHandler(thumbDir))

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil { log.Fatalf("Static FS error: %v", err) }
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	templateSub, err := fs.Sub(templateFS, "templates")
	if err != nil { log.Fatalf("Template FS error: %v", err) }
	pageHandler, err := handlers.NewPageHandler(database, templateSub, title, readOnly)
	if err != nil { log.Fatalf("Template error: %v", err) }
	mux.HandleFunc("GET /video/{hash}", pageHandler.VideoPage)
	mux.HandleFunc("GET /tags", pageHandler.TagsPage)
	mux.HandleFunc("GET /", pageHandler.Index)

	log.Printf("Starting '%s' on %s", title, addr)
	if readOnly { log.Println("Read-only mode: editing disabled") }
	if err := http.ListenAndServe(addr, mux); err != nil { log.Fatalf("Server error: %v", err) }
}
