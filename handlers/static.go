package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"videolib/db"
)

type VideoFileHandler struct {
	DB *db.Database
}

func NewVideoFileHandler(database *db.Database) *VideoFileHandler {
	return &VideoFileHandler{DB: database}
}

func (h *VideoFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" {
		http.NotFound(w, r)
		return
	}

	video, err := h.DB.GetVideo(hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if _, err := os.Stat(video.Path); os.IsNotExist(err) {
		http.Error(w, "Video file not found on disk", 404)
		return
	}

	ext := strings.ToLower(filepath.Ext(video.Path))
	contentTypes := map[string]string{
		".mp4":  "video/mp4",
		".webm": "video/webm",
		".ogv":  "video/ogg",
		".mkv":  "video/x-matroska",
		".avi":  "video/x-msvideo",
		".mov":  "video/quicktime",
		".wmv":  "video/x-ms-wmv",
		".flv":  "video/x-flv",
		".m4v":  "video/mp4",
		".ts":   "video/mp2t",
	}
	if ct, ok := contentTypes[ext]; ok {
		w.Header().Set("Content-Type", ct)
	}

	http.ServeFile(w, r, video.Path)
}

type ThumbHandler struct {
	ThumbDir string
}

func NewThumbHandler(thumbDir string) *ThumbHandler {
	return &ThumbHandler{ThumbDir: thumbDir}
}

func (h *ThumbHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	filename := r.PathValue("filename")

	if hash == "" || filename == "" {
		http.NotFound(w, r)
		return
	}

	if strings.Contains(hash, "..") || strings.Contains(filename, "..") {
		http.Error(w, "Invalid path", 400)
		return
	}

	path := filepath.Join(h.ThumbDir, hash, filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

type ThumbListHandler struct {
	ThumbDir string
}

func (h *ThumbListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" || strings.Contains(hash, "..") {
		http.NotFound(w, r)
		return
	}

	dir := filepath.Join(h.ThumbDir, hash)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, 200, []string{})
		return
	}

	var thumbs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jpg") {
			thumbs = append(thumbs, fmt.Sprintf("/thumbs/%s/%s", hash, e.Name()))
		}
	}

	writeJSON(w, 200, thumbs)
}
