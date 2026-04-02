package scanner

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"videolib/db"
	"videolib/hasher"
	"videolib/models"
)

var videoExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
	".mpg": true, ".mpeg": true, ".3gp": true, ".ogv": true,
	".ts": true, ".vob": true, ".divx": true, ".rm": true,
	".rmvb": true,
}

type Scanner struct {
	database   *db.Database
	roots      []string
	thumbDir   string
	thumbCount int
}

type ScanResult struct {
	Added   int
	Removed int
	Errors  int
	Total   int
}

func New(database *db.Database, roots []string, thumbDir string) *Scanner {
	return &Scanner{
		database:   database,
		roots:      roots,
		thumbDir:   thumbDir,
		thumbCount: 16,
	}
}

func (s *Scanner) Scan() (*ScanResult, error) {
	result := &ScanResult{}

	log.Println("Scanning directories for video files...")
	diskFiles := make(map[string]os.FileInfo)
	for _, root := range s.roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Printf("Warning: cannot access %s: %v", path, err)
				return nil
			}
			if info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if videoExtensions[ext] {
				// Keep as relative path (clean it)
				cleanPath := filepath.Clean(path)
				diskFiles[cleanPath] = info
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", root, err)
		}
	}
	log.Printf("Found %d video files on disk", len(diskFiles))
	result.Total = len(diskFiles)

	existingPaths, err := s.database.GetAllPaths()
	if err != nil {
		return nil, fmt.Errorf("reading existing paths: %w", err)
	}

	// Find deleted files
	for path, hash := range existingPaths {
		if _, exists := diskFiles[path]; !exists {
			log.Printf("Removing deleted file: %s", path)
			if err := s.database.DeleteVideo(hash); err != nil {
				log.Printf("Error removing %s: %v", path, err)
				result.Errors++
			} else {
				result.Removed++
			}
		}
	}

	// Find new files
	type workItem struct {
		path string
		info os.FileInfo
	}

	var items []workItem
	for path, info := range diskFiles {
		if _, exists := existingPaths[path]; !exists {
			items = append(items, workItem{path, info})
		}
	}

	log.Printf("Processing %d new files...", len(items))

	for i, item := range items {
		log.Printf("[%d/%d] Processing: %s", i+1, len(items), item.path)

		video, err := s.processFile(item.path, item.info)
		if err != nil {
			log.Printf("Error processing %s: %v", item.path, err)
			result.Errors++
			continue
		}

		if err := s.database.PutVideo(video); err != nil {
			log.Printf("Error storing %s: %v", item.path, err)
			result.Errors++
			continue
		}

		if err := s.generateThumbnails(video); err != nil {
			log.Printf("Warning: thumbnail generation failed for %s: %v", item.path, err)
		}

		result.Added++
	}

	return result, nil
}

func (s *Scanner) processFile(path string, info os.FileInfo) (*models.Video, error) {
	hash, err := hasher.HashFile(path)
	if err != nil {
		return nil, fmt.Errorf("hashing: %w", err)
	}

	duration, width, height := s.probeVideo(path)

	video := &models.Video{
		Hash:        hash,
		Path:        filepath.Clean(path),
		Filename:    filepath.Base(path),
		Directory:   filepath.Dir(path),
		Size:        info.Size(),
		Duration:    duration,
		Width:       width,
		Height:      height,
		Tags:        []string{},
		ThumbCount:  0,
		MainThumb:   -1,
		AddedAt:     time.Now(),
		ModifiedAt:  time.Now(),
		FileModTime: info.ModTime(),
	}

	return video, nil
}

type ffprobeResult struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func (s *Scanner) probeVideo(path string) (duration float64, width, height int) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)

	output, err := cmd.Output()
	if err != nil {
		log.Printf("ffprobe failed for %s: %v", path, err)
		return 0, 0, 0
	}

	var result ffprobeResult
	if err := json.Unmarshal(output, &result); err != nil {
		log.Printf("ffprobe parse failed for %s: %v", path, err)
		return 0, 0, 0
	}

	duration, _ = strconv.ParseFloat(result.Format.Duration, 64)

	for _, stream := range result.Streams {
		if stream.CodecType == "video" {
			width = stream.Width
			height = stream.Height
			break
		}
	}

	return duration, width, height
}

func (s *Scanner) generateThumbnails(video *models.Video) error {
	if video.Duration <= 0 {
		return fmt.Errorf("unknown duration, cannot generate thumbnails")
	}

	thumbDir := filepath.Join(s.thumbDir, video.Hash)
	if err := os.MkdirAll(thumbDir, 0755); err != nil {
		return err
	}

	count := s.thumbCount
	interval := video.Duration / float64(count+1)

	for i := 0; i < count; i++ {
		timestamp := interval * float64(i+1)
		outPath := filepath.Join(thumbDir, fmt.Sprintf("thumb_%02d.jpg", i))

		if _, err := os.Stat(outPath); err == nil {
			continue
		}

		cmd := exec.Command("ffmpeg",
			"-ss", fmt.Sprintf("%.2f", timestamp),
			"-i", video.Path,
			"-vframes", "1",
			"-vf", "scale=320:-1",
			"-q:v", "5",
			"-y",
			outPath,
		)
		cmd.Run() // best effort
	}

	entries, _ := os.ReadDir(thumbDir)
	thumbCount := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			thumbCount++
		}
	}
	video.ThumbCount = thumbCount

	return s.database.PutVideo(video)
}

// ScrubThumbnails removes thumbnail directories for videos not in the database
func (s *Scanner) ScrubThumbnails() (removed int, err error) {
	knownHashes, err := s.database.GetAllHashes()
	if err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(s.thumbDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hash := entry.Name()
		if !knownHashes[hash] {
			dir := filepath.Join(s.thumbDir, hash)
			log.Printf("Removing orphaned thumbnail directory: %s", dir)
			if err := os.RemoveAll(dir); err != nil {
				log.Printf("Error removing %s: %v", dir, err)
			} else {
				removed++
			}
		}
	}

	return removed, nil
}
