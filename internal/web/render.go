package web

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/belcaik/open-nvr/internal/logx"
)

// parsePages compila cada página junto con layout.html. Los parciales
// (nombre que empieza por "_") se compilan solos.
func parsePages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	names, _ := fs.Glob(templateFS, "templates/*.html")
	for _, n := range names {
		base := strings.TrimSuffix(strings.TrimPrefix(n, "templates/"), ".html")
		if base == "layout" {
			continue
		}
		if strings.HasPrefix(base, "_") {
			pages[base] = template.Must(template.ParseFS(templateFS, n))
			continue
		}
		pages[base] = template.Must(template.ParseFS(templateFS, "templates/layout.html", n))
	}
	return pages
}

// pageData es lo que reciben todas las plantillas.
type pageData struct {
	Title string
	User  string
	CSRF  string
	Data  any
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	pd := pageData{Title: title, Data: data}
	if se := sessionFrom(r); se.ID != "" {
		pd.User, pd.CSRF = se.User, s.d.Sessions.CSRFToken(se)
	}
	t := s.pages[page]
	name := "layout"
	if strings.HasPrefix(page, "_") {
		name = page
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name+".html", pd); err != nil {
		logx.Printf("plantilla %s: %v", page, err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}
