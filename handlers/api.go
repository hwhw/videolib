package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"videolib/db"
)

type APIHandler struct {
	DB       *db.Database
	ReadOnly bool
}

func NewAPIHandler(database *db.Database, readOnly bool) *APIHandler {
	return &APIHandler{DB: database, ReadOnly: readOnly}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (h *APIHandler) checkWritable(w http.ResponseWriter) bool {
	if h.ReadOnly {
		writeError(w, 403, "server is in read-only mode")
		return false
	}
	return true
}

func (h *APIHandler) ListVideos(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	query := r.URL.Query().Get("q")
	tagsParam := r.URL.Query().Get("tags")

	if search != "" {
		videos, err := h.DB.SearchByQuery(search)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, videos)
		return
	}
	if tagsParam != "" {
		tags := strings.Split(tagsParam, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
		videos, err := h.DB.SearchByTags(tags)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, videos)
		return
	}
	if query != "" {
		videos, err := h.DB.FullTextSearch(query)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, videos)
		return
	}
	videos, err := h.DB.ListAllVideos()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, videos)
}

func (h *APIHandler) GetVideo(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	video, err := h.DB.GetVideo(hash)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, video)
}

func (h *APIHandler) AddTags(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.DB.AddTags(hash, body.Tags); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	video, _ := h.DB.GetVideo(hash)
	writeJSON(w, 200, video)
}

func (h *APIHandler) SetTags(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.DB.SetTags(hash, body.Tags); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	video, _ := h.DB.GetVideo(hash)
	writeJSON(w, 200, video)
}

func (h *APIHandler) RemoveTags(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.DB.RemoveTags(hash, body.Tags); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	video, _ := h.DB.GetVideo(hash)
	writeJSON(w, 200, video)
}

func (h *APIHandler) BulkTags(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	var body struct {
		Hashes []string `json:"hashes"`
		Tags   []string `json:"tags"`
		Action string   `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	log.Printf("Bulk tag %s: %d videos, tags: %v", body.Action, len(body.Hashes), body.Tags)
	var err error
	switch body.Action {
	case "add":
		err = h.DB.BulkAddTags(body.Hashes, body.Tags)
	case "remove":
		err = h.DB.BulkRemoveTags(body.Hashes, body.Tags)
	default:
		writeError(w, 400, "action must be 'add' or 'remove'")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (h *APIHandler) SetMainThumb(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	var body struct {
		Index int `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.DB.SetMainThumb(hash, body.Index); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	video, _ := h.DB.GetVideo(hash)
	writeJSON(w, 200, video)
}

func (h *APIHandler) SetTitle(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.DB.SetTitle(hash, body.Title); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	video, _ := h.DB.GetVideo(hash)
	writeJSON(w, 200, video)
}

func (h *APIHandler) SetDescription(w http.ResponseWriter, r *http.Request) {
	if !h.checkWritable(w) {
		return
	}
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, 400, "missing hash")
		return
	}
	var body struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if err := h.DB.SetDescription(hash, body.Description); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	video, _ := h.DB.GetVideo(hash)
	writeJSON(w, 200, video)
}

func (h *APIHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.DB.ListAllTags()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, tags)
}

// ConfigHandler returns frontend configuration (like read-only mode)
func (h *APIHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"read_only": h.ReadOnly,
	})
}
