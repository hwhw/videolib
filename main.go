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
  serve     Start the web server
  scan      Add videos to the database (from directory, file, or JSON import)
  scrub     Remove stale database entries and orphaned thumbnails
  list      Export video database in JSON or text format
  tags      Add or remove tags for a video by hash
  hash      Compute content hash of files

General options (available to all commands):
  -db       Database file path (default: videolib.db)
  -thumbs   Thumbnail directory (default: thumbnails)

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
		fmt.Fprintf(os.Stderr, `Usage: videolib serve [options]

Start the web server. Video paths are read from the database;
no directory argument is needed.

Options:
`)
		fs.PrintDefaults()
	}

	dbPath := fs.String("db", "videolib.db", "Database file path")
	thumbDir := fs.String("thumbs", "thumbnails", "Thumbnail directory")
	addr := fs.String("addr", ":8080", "Listen address")
	title := fs.String("title", "Video Library", "Web application title")
	fs.Parse(args)

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		log.Fatalf("Database not found: %s (run 'videolib scan' first)", *dbPath)
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	startServer(*addr, database, *thumbDir, *title)
}

// === scan ===

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib scan [options] <path> [<path> ...]

Add videos to the database. Each path can be:
  - A directory (scanned recursively for video files)
  - A single video file
  - A JSON file previously exported with 'videolib list -format json'

When a file with an already-known hash is found at a new path,
the database entry is updated to the new path (preserving tags
and thumbnails).

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nDefault video extensions: %s\n", defaultExtList())
	}

	dbPath := fs.String("db", "videolib.db", "Database file path")
	thumbDir := fs.String("thumbs", "thumbnails", "Thumbnail directory")
	extList := fs.String("ext", "", "Override video extensions (comma-separated, e.g. mp4,mkv,avi)")
	addExt := fs.String("add-ext", "", "Add extra extensions to defaults (comma-separated)")
	fs.Parse(args)

	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		os.Exit(1)
	}

	if err := os.MkdirAll(*thumbDir, 0755); err != nil {
		log.Fatalf("Cannot create thumbnail directory: %v", err)
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	// Parse extensions
	var overrideExts []string
	if *extList != "" {
		overrideExts = strings.Split(*extList, ",")
	}
	var extraExts []string
	if *addExt != "" {
		extraExts = strings.Split(*addExt, ",")
	}

	var totalAdded, totalUpdated, totalSkipped, totalErrors int

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			log.Printf("Cannot access %s: %v", path, err)
			totalErrors++
			continue
		}

		// JSON import
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".json") {
			added, updated, skipped := importJSON(database, path)
			totalAdded += added
			totalUpdated += updated
			totalSkipped += skipped
			continue
		}

		if info.IsDir() {
			s := scanner.New(database, []string{filepath.Clean(path)}, *thumbDir)
			if len(overrideExts) > 0 {
				s.SetExtensions(overrideExts)
			}
			if len(extraExts) > 0 {
				s.AddExtensions(extraExts)
			}
			result, err := s.Scan()
			if err != nil {
				log.Printf("Scan error for %s: %v", path, err)
				totalErrors++
				continue
			}
			totalAdded += result.Added
			totalUpdated += result.Updated
			totalSkipped += result.Skipped
			totalErrors += result.Errors
			log.Printf("Scanned %s: %d added, %d updated, %d skipped, %d errors, %d total on disk",
				path, result.Added, result.Updated, result.Skipped, result.Errors, result.Total)
		} else {
			dir := filepath.Dir(filepath.Clean(path))
			filename := filepath.Base(path)
			s := scanner.New(database, []string{dir}, *thumbDir)
			if len(overrideExts) > 0 {
				s.SetExtensions(overrideExts)
			}
			if len(extraExts) > 0 {
				s.AddExtensions(extraExts)
			}
			s.SetFileFilter(filename)
			result, err := s.Scan()
			if err != nil {
				log.Printf("Error scanning %s: %v", path, err)
				totalErrors++
				continue
			}
			totalAdded += result.Added
			totalUpdated += result.Updated
			totalSkipped += result.Skipped
			totalErrors += result.Errors
		}
	}

	log.Printf("Scan complete: %d added, %d updated, %d skipped, %d errors",
		totalAdded, totalUpdated, totalSkipped, totalErrors)
}

func defaultExtList() string {
	var exts []string
	for ext := range scanner.DefaultVideoExtensions {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

func importJSON(database *db.Database, path string) (added, updated, skipped int) {
	log.Printf("Importing JSON: %s", path)

	jsonBytes, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Cannot read %s: %v", path, err)
		return 0, 0, 0
	}

	var data models.ExportData
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		log.Printf("Invalid JSON in %s: %v", path, err)
		return 0, 0, 0
	}

	added, updated, skipped, err = database.Import(&data)
	if err != nil {
		log.Printf("Import error: %v", err)
	}

	log.Printf("Imported %s: %d added, %d updated, %d skipped", path, added, updated, skipped)
	return
}

// === scrub ===

func cmdScrub(args []string) {
	fs := flag.NewFlagSet("scrub", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib scrub [options]

Clean up the database and thumbnail directory.

By default, both operations run in order:
  1. Remove database entries for video files that no longer exist on disk
  2. Remove thumbnail directories that have no matching database entry

Either can be disabled with flags.

Options:
`)
		fs.PrintDefaults()
	}

	dbPath := fs.String("db", "videolib.db", "Database file path")
	thumbDir := fs.String("thumbs", "thumbnails", "Thumbnail directory")
	noVideos := fs.Bool("no-videos", false, "Skip removing stale database entries")
	noThumbs := fs.Bool("no-thumbs", false, "Skip removing orphaned thumbnails")
	dryRun := fs.Bool("dry-run", false, "Show what would be done without making changes")
	fs.Parse(args)

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	if !*noVideos {
		scrubVideos(database, *dryRun)
	}

	if !*noThumbs {
		scrubThumbnails(database, *thumbDir, *dryRun)
	}
}

func scrubVideos(database *db.Database, dryRun bool) {
	log.Println("Checking for stale database entries...")

	paths, err := database.GetAllPaths()
	if err != nil {
		log.Fatalf("Cannot read paths: %v", err)
	}

	var removed int
	for path, hash := range paths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if dryRun {
				log.Printf("[DRY RUN] Would remove: %s (hash: %s)", path, hash[:12])
				removed++
			} else {
				log.Printf("Removing stale entry: %s", path)
				if err := database.DeleteVideo(hash); err != nil {
					log.Printf("Error removing %s: %v", path, err)
				} else {
					removed++
				}
			}
		}
	}

	if dryRun {
		log.Printf("Dry run: %d stale entries found", removed)
	} else {
		log.Printf("Removed %d stale database entries", removed)
	}
}

func scrubThumbnails(database *db.Database, thumbDir string, dryRun bool) {
	log.Println("Checking for orphaned thumbnail directories...")

	knownHashes, err := database.GetAllHashes()
	if err != nil {
		log.Fatalf("Cannot read hashes: %v", err)
	}

	entries, err := os.ReadDir(thumbDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("Thumbnail directory does not exist, nothing to scrub")
			return
		}
		log.Fatalf("Cannot read thumbnail directory: %v", err)
	}

	var removed int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hash := entry.Name()
		if !knownHashes[hash] {
			dir := filepath.Join(thumbDir, hash)
			if dryRun {
				log.Printf("[DRY RUN] Would remove thumbnail dir: %s", dir)
				removed++
			} else {
				log.Printf("Removing orphaned thumbnails: %s", dir)
				if err := os.RemoveAll(dir); err != nil {
					log.Printf("Error removing %s: %v", dir, err)
				} else {
					removed++
				}
			}
		}
	}

	if dryRun {
		log.Printf("Dry run: %d orphaned thumbnail directories found", removed)
	} else {
		log.Printf("Removed %d orphaned thumbnail directories", removed)
	}
}

// === list ===

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib list [options] [search query]

List videos from the database.

Output formats:
  json   Full JSON export (can be imported with 'videolib scan file.json')
  text   One line per video: HASH<tab>TAGS<tab>PATH

The optional search query uses the same syntax as the web interface:
  tag:value, UNTAGGED, AND/OR/NOT, filename search, wildcards*

Options:
`)
		fs.PrintDefaults()
	}

	dbPath := fs.String("db", "videolib.db", "Database file path")
	format := fs.String("format", "text", "Output format: json, text")
	output := fs.String("output", "-", "Output file (- for stdout)")
	fs.Parse(args)

	search := strings.Join(fs.Args(), " ")

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	var videos []*models.Video

	if search != "" {
		videos, err = database.SearchByQuery(search)
		if err != nil {
			log.Fatalf("Search error: %v", err)
		}
	} else {
		videos, err = database.ListAllVideos()
		if err != nil {
			log.Fatalf("List error: %v", err)
		}
	}

	var w *os.File
	if *output == "-" {
		w = os.Stdout
	} else {
		w, err = os.Create(*output)
		if err != nil {
			log.Fatalf("Cannot create output file: %v", err)
		}
		defer w.Close()
	}

	switch *format {
	case "json":
		exportData := &models.ExportData{
			Version:  1,
			Exported: time.Now().UTC().Format(time.RFC3339),
			Videos:   videos,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(exportData); err != nil {
			log.Fatalf("JSON encode error: %v", err)
		}

	case "text":
		for _, v := range videos {
			tags := strings.Join(v.Tags, ",")
			if tags == "" {
				tags = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", v.Hash, tags, v.Path)
		}

	default:
		log.Fatalf("Unknown format: %s (use 'json' or 'text')", *format)
	}

	if *output != "-" {
		log.Printf("Written %d videos to %s (%s format)", len(videos), *output, *format)
	}
}

// === tags ===

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	if value == "" {
		return fmt.Errorf("tag cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

func cmdTags(args []string) {
	fs := flag.NewFlagSet("tags", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib tags [options] [<hash> ...]

Add or remove tags for videos identified by hash.
Multiple -add and -remove flags can be specified.

If no hash arguments are given, hashes are read from stdin
(one per line, compatible with 'videolib list' text output —
only the first whitespace-delimited token per line is used).

Hash arguments can be prefixes (minimum 8 characters).

Examples:
  videolib tags -add action -add comedy abc123def456
  videolib tags -remove boring abc123de
  videolib tags -add genre:action -remove old-tag hash1 hash2 hash3

  # Tag all untagged videos:
  videolib list UNTAGGED | videolib tags -add needs-review

  # Tag search results:
  videolib list 'holiday*' | videolib tags -add vacation

Options:
`)
		fs.PrintDefaults()
	}

	dbPath := fs.String("db", "videolib.db", "Database file path")
	var addTags stringSlice
	var removeTags stringSlice
	fs.Var(&addTags, "add", "Tag to add (can be specified multiple times)")
	fs.Var(&removeTags, "remove", "Tag to remove (can be specified multiple times)")
	fs.Parse(args)

	if len(addTags) == 0 && len(removeTags) == 0 {
		fmt.Fprintf(os.Stderr, "Error: specify at least one -add or -remove flag\n\n")
		fs.Usage()
		os.Exit(1)
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	// Collect hashes from arguments or stdin
	var hashInputs []string
	if fs.NArg() > 0 {
		hashInputs = fs.Args()
	} else {
		// Read from stdin
		hashInputs = readHashesFromStdin()
		if len(hashInputs) == 0 {
			fmt.Fprintf(os.Stderr, "Error: no hashes provided (pass as arguments or pipe via stdin)\n")
			os.Exit(1)
		}
	}

	// Resolve all hash prefixes
	var hashes []string
	for _, input := range hashInputs {
		hash, err := resolveHash(database, input)
		if err != nil {
			log.Printf("Warning: %v (skipping)", err)
			continue
		}
		hashes = append(hashes, hash)
	}

	if len(hashes) == 0 {
		log.Fatalf("No valid videos found")
	}

	log.Printf("Applying tag changes to %d video(s)...", len(hashes))

	// Apply removals first, then additions
	if len(removeTags) > 0 {
		if err := database.BulkRemoveTags(hashes, []string(removeTags)); err != nil {
			log.Fatalf("Error removing tags: %v", err)
		}
		log.Printf("Removed tags: %s", strings.Join(removeTags, ", "))
	}

	if len(addTags) > 0 {
		if err := database.BulkAddTags(hashes, []string(addTags)); err != nil {
			log.Fatalf("Error adding tags: %v", err)
		}
		log.Printf("Added tags: %s", strings.Join(addTags, ", "))
	}

	// Show results
	for _, hash := range hashes {
		video, err := database.GetVideo(hash)
		if err != nil {
			continue
		}
		tags := strings.Join(video.Tags, ",")
		if tags == "" {
			tags = "-"
		}
		fmt.Printf("%s\t%s\t%s\n", video.Hash, tags, video.Path)
	}
}

func readHashesFromStdin() []string {
	var hashes []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// First whitespace-delimited token is the hash
		fields := strings.Fields(line)
		if len(fields) > 0 {
			hashes = append(hashes, fields[0])
		}
	}
	return hashes
}

// resolveHash finds a full hash from a prefix (minimum 8 chars)
func resolveHash(database *db.Database, prefix string) (string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))

	if len(prefix) < 8 {
		return "", fmt.Errorf("hash prefix too short (minimum 8 characters): %s", prefix)
	}

	// Try exact match first
	_, err := database.GetVideo(prefix)
	if err == nil {
		return prefix, nil
	}

	// Prefix search
	allHashes, err := database.GetAllHashes()
	if err != nil {
		return "", fmt.Errorf("cannot read hashes: %w", err)
	}

	var matches []string
	for hash := range allHashes {
		if strings.HasPrefix(hash, prefix) {
			matches = append(matches, hash)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no video found with hash prefix: %s", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous hash prefix '%s' matches %d videos (use more characters)", prefix, len(matches))
	}
}

// === hash ===

func cmdHash(args []string) {
	fs := flag.NewFlagSet("hash", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: videolib hash [<file> ...]

Compute the content hash of one or more files. This is the same
hash used internally to identify videos.

If no filenames are given, filenames are read from stdin (one per line).
Outputs one hash per line. If a file is not found, outputs NOTFOUND.

Examples:
  videolib hash video.mp4
  videolib hash *.mp4
  find . -name '*.mkv' | videolib hash
  videolib list -format text | cut -f3 | videolib hash

`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	var files []string
	if fs.NArg() > 0 {
		files = fs.Args()
	} else {
		s := bufio.NewScanner(os.Stdin)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line != "" {
				files = append(files, line)
			}
		}
	}

	if len(files) == 0 {
		fs.Usage()
		os.Exit(1)
	}

	for _, file := range files {
		h, err := hasher.HashFile(file)
		if err != nil {
			fmt.Println("NOTFOUND")
		} else {
			fmt.Println(h)
		}
	}
}

// === web server ===

func startServer(addr string, database *db.Database, thumbDir, title string) {
	mux := http.NewServeMux()

	apiHandler := handlers.NewAPIHandler(database)
	mux.HandleFunc("GET /api/videos", apiHandler.ListVideos)
	mux.HandleFunc("GET /api/videos/{hash}", apiHandler.GetVideo)
	mux.HandleFunc("POST /api/videos/{hash}/tags", apiHandler.AddTags)
	mux.HandleFunc("PUT /api/videos/{hash}/tags", apiHandler.SetTags)
	mux.HandleFunc("DELETE /api/videos/{hash}/tags", apiHandler.RemoveTags)
	mux.HandleFunc("PUT /api/videos/{hash}/main-thumb", apiHandler.SetMainThumb)
	mux.HandleFunc("POST /api/bulk/tags", apiHandler.BulkTags)
	mux.HandleFunc("GET /api/tags", apiHandler.ListTags)

	thumbListHandler := &handlers.ThumbListHandler{ThumbDir: thumbDir}
	mux.Handle("GET /api/thumbs/{hash}", thumbListHandler)

	videoHandler := handlers.NewVideoFileHandler(database)
	mux.Handle("GET /media/{hash}", videoHandler)

	thumbHandler := handlers.NewThumbHandler(thumbDir)
	mux.Handle("GET /thumbs/{hash}/{filename}", thumbHandler)

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("Cannot create static sub-FS: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	templateSub, err := fs.Sub(templateFS, "templates")
	if err != nil {
		log.Fatalf("Cannot create template sub-FS: %v", err)
	}
	pageHandler, err := handlers.NewPageHandler(database, templateSub, title)
	if err != nil {
		log.Fatalf("Cannot load templates: %v", err)
	}
	mux.HandleFunc("GET /video/{hash}", pageHandler.VideoPage)
	mux.HandleFunc("GET /tags", pageHandler.TagsPage)
	mux.HandleFunc("GET /", pageHandler.Index)

	log.Printf("Starting '%s' on %s", title, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
