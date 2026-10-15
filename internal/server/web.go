package server

import (
	"embed"
	"io"
	"net/http"

	"hello-guest/internal/importcsv"
	"hello-guest/internal/store"
)

//go:embed web/*
var webFS embed.FS

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("web/app.html")
	if err != nil {
		http.Error(w, "no client", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func importFrom(r io.Reader, event string) ([]store.Guest, error) {
	return importcsv.Parse(r, event)
}
