package models

import (
	"fmt"
	"time"
)

type Video struct {
	Hash        string    `json:"hash"`
	Path        string    `json:"path"`
	Filename    string    `json:"filename"`
	Directory   string    `json:"directory"`
	Size        int64     `json:"size"`
	Duration    float64   `json:"duration"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Tags        []string  `json:"tags"`
	ThumbCount  int       `json:"thumb_count"`
	MainThumb   int       `json:"main_thumb"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AddedAt     time.Time `json:"added_at"`
	ModifiedAt  time.Time `json:"modified_at"`
	FileModTime time.Time `json:"file_mod_time"`
}

func (v *Video) DisplayName() string {
	if v.Title != "" {
		return v.Title
	}
	return v.Filename
}

func (v *Video) DurationString() string {
	d := time.Duration(v.Duration * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func (v *Video) SizeString() string {
	const (
		MB = 1024 * 1024
		GB = 1024 * MB
	)
	switch {
	case v.Size >= GB:
		return fmt.Sprintf("%.1f GB", float64(v.Size)/float64(GB))
	case v.Size >= MB:
		return fmt.Sprintf("%.1f MB", float64(v.Size)/float64(MB))
	default:
		return fmt.Sprintf("%d KB", v.Size/1024)
	}
}

func (v *Video) MainThumbIndex() int {
	if v.MainThumb >= 0 && v.MainThumb < v.ThumbCount {
		return v.MainThumb
	}
	return 0
}

func (v *Video) MainThumbFilename() string {
	return fmt.Sprintf("thumb_%02d.jpg", v.MainThumbIndex())
}

type TagInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type ExportData struct {
	Version  int      `json:"version"`
	Exported string   `json:"exported"`
	Videos   []*Video `json:"videos"`
}
