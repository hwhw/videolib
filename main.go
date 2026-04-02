package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"videolib/db"
	"videolib/handlers"
	"videolib/models"
	"videolib/scanner"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func main() {
	addr := flag.String("addr", ":8421", "Listen address")
	dbPath := flag.String("db", "videolib.db", "Database file path")
	thumbDir := flag.String("thumbs", "thumbnails", "Thumbnail directory")
	title := flag.String("title", "Video Library", "Web application title")
	scanFlag := flag.Bool("scan", false, "Scan for new/deleted videos and exit")
	scanAndServe := flag.Bool("scan-and-serve", false, "Scan then start server")
	scrubThumbs := flag.Bool("scrub-thumbs", false, "Remove orphaned thumbnail directories and exit")
	exportFile := flag.String("export", "", "Export database to JSON file")
	importFile := flag.String("import", "", "Import database from JSON file")
	dirs := flag.String("dirs", "", "Comma-separated directories to scan for videos")

	flag.Parse()

	// Collect directories from both -dirs and positional args
	scanDirs := flag.Args()
	if *dirs != "" {
		for _, d := range strings.Split(*dirs, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				scanDirs = append(scanDirs, d)
			}
		}
	}

	// Create thumbnail directory
	if err := os.MkdirAll(*thumbDir, 0755); err != nil {
		log.Fatalf("Cannot create thumbnail directory: %v", err)
	}

	// Handle export
	if *exportFile != "" {
		database, err := db.Open(*dbPath)
		if err != nil {
			log.Fatalf("Cannot open database: %v", err)
		}
		defer database.Close()

		data, err := database.Export()
		if err != nil {
			log.Fatalf("Export failed: %v", err)
		}

		jsonBytes, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			log.Fatalf("JSON marshal failed: %v", err)
		}

		if err := os.WriteFile(*exportFile, jsonBytes, 0644); err != nil {
			log.Fatalf("Write failed: %v", err)
		}
		log.Printf("Exported %d videos to %s", len(data.Videos), *exportFile)
		return
	}

	// Handle import
	if *importFile != "" {
		database, err := db.Open(*dbPath)
		if err != nil {
			log.Fatalf("Cannot open database: %v", err)
		}
		defer database.Close()

		jsonBytes, err := os.ReadFile(*importFile)
		if err != nil {
			log.Fatalf("Read failed: %v", err)
		}

		var data models.ExportData
		if err := json.Unmarshal(jsonBytes, &data); err != nil {
			log.Fatalf("JSON parse failed: %v", err)
		}

		added, updated, skipped, err := database.Import(&data)
		if err != nil {
			log.Fatalf("Import failed: %v", err)
		}
		log.Printf("Import complete: %d added, %d updated, %d skipped", added, updated, skipped)
		return
	}

	// Need directories for scan/scrub/serve
	if len(scanDirs) == 0 && !*scrubThumbs {
		fmt.Println("Usage: videolib [flags] <directory> [<directory> ...]")
		fmt.Println()
		fmt.Println("Flags:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println("  videolib --scan ./videos")
		fmt.Println("  videolib --scan-and-serve ./videos ./more-videos")
		fmt.Println("  videolib ./videos")
		fmt.Println("  videolib --export backup.json ./videos")
		fmt.Println("  videolib --import backup.json ./videos")
		fmt.Println("  videolib --scrub-thumbs")
		os.Exit(1)
	}

	// Open database
	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Cannot open database: %v", err)
	}
	defer database.Close()

	s := scanner.New(database, scanDirs, *thumbDir)

	// Handle scrub
	if *scrubThumbs {
		removed, err := s.ScrubThumbnails()
		if err != nil {
			log.Fatalf("Scrub failed: %v", err)
		}
		log.Printf("Scrubbed %d orphaned thumbnail directories", removed)
		return
	}

	// Handle scan
	if *scanFlag || *scanAndServe {
		result, err := s.Scan()
		if err != nil {
			log.Fatalf("Scan failed: %v", err)
		}
		log.Printf("Scan complete: %d added, %d removed, %d errors, %d total on disk",
			result.Added, result.Removed, result.Errors, result.Total)

		if *scanFlag {
			return
		}
	}

	startServer(*addr, database, *thumbDir, *title)
}

func startServer(addr string, database *db.Database, thumbDir, title string) {
	mux := http.NewServeMux()

	// API handlers
	apiHandler := handlers.NewAPIHandler(database)
	mux.HandleFunc("GET /api/videos", apiHandler.ListVideos)
	mux.HandleFunc("GET /api/videos/{hash}", apiHandler.GetVideo)
	mux.HandleFunc("POST /api/videos/{hash}/tags", apiHandler.AddTags)
	mux.HandleFunc("PUT /api/videos/{hash}/tags", apiHandler.SetTags)
	mux.HandleFunc("DELETE /api/videos/{hash}/tags", apiHandler.RemoveTags)
	mux.HandleFunc("PUT /api/videos/{hash}/main-thumb", apiHandler.SetMainThumb)
	mux.HandleFunc("POST /api/bulk/tags", apiHandler.BulkTags)
	mux.HandleFunc("GET /api/tags", apiHandler.ListTags)

	// Thumbnail list API
	thumbListHandler := &handlers.ThumbListHandler{ThumbDir: thumbDir}
	mux.Handle("GET /api/thumbs/{hash}", thumbListHandler)

	// Media file serving
	videoHandler := handlers.NewVideoFileHandler(database)
	mux.Handle("GET /media/{hash}", videoHandler)

	// Thumbnail file serving
	thumbHandler := handlers.NewThumbHandler(thumbDir)
	mux.Handle("GET /thumbs/{hash}/{filename}", thumbHandler)

	// Static files from embedded FS
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("Cannot create static sub-FS: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Page handlers from embedded FS
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
