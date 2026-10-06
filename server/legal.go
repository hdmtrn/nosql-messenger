package main

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"
)

//go:embed legal/*.html
var legalFiles embed.FS

// operator is who runs this instance, for the privacy notice and the terms. It
// comes from the environment: the repository is public, and it carries the text
// of the pages, not the name and address of whoever deploys it.
type operator struct {
	Name  string
	Email string
}

func operatorFromEnv() operator {
	return operator{Name: os.Getenv("OPERATOR_NAME"), Email: os.Getenv("OPERATOR_EMAIL")}
}

// Configured is false until both are set; the pages then say so instead of
// naming nobody.
func (o operator) Configured() bool { return o.Name != "" && o.Email != "" }

var legalPages = map[string]*template.Template{
	"privacy": template.Must(template.ParseFS(legalFiles, "legal/layout.html", "legal/privacy.html")),
	"terms":   template.Must(template.ParseFS(legalFiles, "legal/layout.html", "legal/terms.html")),
}

func (s *server) handleLegal(name string) http.HandlerFunc {
	page := legalPages[name]
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if err := page.ExecuteTemplate(w, "layout", s.operator); err != nil {
			log.Printf("rendering the %s page: %v", name, err)
		}
	}
}
