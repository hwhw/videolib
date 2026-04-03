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

var DefaultVideoExtensions = map[string]bool{
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
	fileFilter string
	extensions map[string]bool
}

type ScanResult struct {
	Added   int
	Updated int
	Skipped int
	Errors  int
	Total   int
}

func New(database *db.Database, roots []string, thumbDir string) *Scanner {
	return &Scanner{
		database:   database,
		roots:      roots,
		thumbDir:   thumbDir,
		thumbCount: 16,
		extensions: DefaultVideoExtensions,
	}
}

// SetFileFilter restricts scanning to a single filename within the root directories
func (s *Scanner) SetFileFilter(filename string) {
	s.fileFilter = filename
}

// SetExtensions replaces the video extension list
func (s *Scanner) SetExtensions(exts []string) {
	m := make(map[string]bool, len(exts))
	for _, ext := range exts {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext != "" {
			if ext[0] != '.' {
				ext = "." + ext
			}
			m[ext] = true
		}
	}
	if len(m) > 0 {
		s.extensions = m
	}
}

// AddExtensions adds extensions to the existing list
func (s *Scanner) AddExtensions(exts []string) {
	for _, ext := range exts {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext != "" {
			if ext[0] != '.' {
				ext = "." + ext
			}
			s.extensions[ext] = true
		}
	}
}

func (s *Scanner) isVideoFile(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !s.extensions[ext] {
		return false
	}
	if s.fileFilter != "" && filepath.Base(path) != s.fileFilter {
		return false
	}
	return true
}

func (s *Scanner) Scan() (*ScanResult, error) {
	result := &ScanResult{}

	log.Println("Scanning for video files...")
	diskFiles := make(map[string]os.FileInfo)
	for _, root := range s.roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Printf("Warning: cannot access %s: %v", path, err)
				return nil
			}
			if s.isVideoFile(path, info) {
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

	type workItem struct {
		path string
		info os.FileInfo
	}

	var items []workItem
	for path, info := range diskFiles {
		if _, exists := existingPaths[path]; !exists {
			items = append(items, workItem{path, info})
		} else {
			result.Skipped++
		}
	}

	log.Printf("Processing %d new files (%d already known)...", len(items), result.Skipped)

	for i, item := range items {
		log.Printf("[%d/%d] Processing: %s", i+1, len(items), item.path)

		hash, err := hasher.HashFile(item.path)
		if err != nil {
			log.Printf("Error hashing %s: %v", item.path, err)
			result.Errors++
			continue
		}

		existing, existErr := s.database.GetVideo(hash)
		if existErr == nil {
			oldPath := existing.Path
			existing.Path = filepath.Clean(item.path)
			existing.Filename = filepath.Base(item.path)
			existing.Directory = filepath.Dir(item.path)
			existing.Size = item.info.Size()
			existing.FileModTime = item.info.ModTime()
			existing.ModifiedAt = time.Now()

			if err := s.database.UpdateVideoPath(existing, oldPath); err != nil {
				log.Printf("Error updating path for %s: %v", item.path, err)
				result.Errors++
			} else {
				log.Printf("Updated path: %s -> %s (hash %s)", oldPath, item.path, hash[:12])
				result.Updated++
			}
			continue
		}

		video, err := s.buildVideo(hash, item.path, item.info)
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

func (s *Scanner) buildVideo(hash, path string, info os.FileInfo) (*models.Video, error) {
	duration, width, height := s.probeVideo(path)

	return &models.Video{
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
	}, nil
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
		cmd.Run()
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
