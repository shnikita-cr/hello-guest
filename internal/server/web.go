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
	s.serveHTML(w, r, "web/app.html")
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	s.serveHTML(w, r, "web/admin.html")
}

func (s *Server) serveHTML(w http.ResponseWriter, r *http.Request, name string) {
	b, err := webFS.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) handleJSQR(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/jsQR.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) handleZXing(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/zxing.min.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) handleSW(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func importFrom(r io.Reader, event string) ([]store.Guest, error) {
	return importcsv.Parse(r, event)
}
