package handlers

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"

	"videolib/db"
)

type PageHandler struct {
	DB        *db.Database
	Templates map[string]*template.Template
	Title     string
}

type pageData struct {
	Title string
	Data  interface{}
}

func NewPageHandler(database *db.Database, templateFS fs.FS, title string) (*PageHandler, error) {
	funcMap := template.FuncMap{
		"join": func(s []string, sep string) string {
			result := ""
			for i, v := range s {
				if i > 0 {
					result += sep
				}
				result += v
			}
			return result
		},
	}

	layoutBytes, err := fs.ReadFile(templateFS, "layout.html")
	if err != nil {
		return nil, err
	}
	layoutStr := string(layoutBytes)

	pageFiles := []string{"index.html", "video.html", "tags.html"}
	templates := make(map[string]*template.Template)

	for _, page := range pageFiles {
		pageBytes, err := fs.ReadFile(templateFS, page)
		if err != nil {
			return nil, err
		}

		t, err := template.New(page).Funcs(funcMap).Parse(layoutStr)
		if err != nil {
			return nil, err
		}
		t, err = t.Parse(string(pageBytes))
		if err != nil {
			return nil, err
		}
		templates[page] = t
	}

	return &PageHandler{
		DB:        database,
		Templates: templates,
		Title:     title,
	}, nil
}

func (h *PageHandler) render(w http.ResponseWriter, name string, data interface{}) {
	t, ok := h.Templates[name]
	if !ok {
		log.Printf("Template not found: %s", name)
		http.Error(w, "Template not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pd := pageData{Title: h.Title, Data: data}
	if err := t.ExecuteTemplate(w, name, pd); err != nil {
		log.Printf("Template error rendering %s: %v", name, err)
		http.Error(w, "Template error", 500)
	}
}

func (h *PageHandler) Index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.render(w, "index.html", nil)
}

func (h *PageHandler) VideoPage(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	video, err := h.DB.GetVideo(hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.render(w, "video.html", video)
}

func (h *PageHandler) TagsPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "tags.html", nil)
}
