// Package server wires the WebSocket hub and HTTP file server together into a
// single runnable HTTP server.
package server

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"github.com/dacort/babble/internal/events"
	"github.com/dacort/babble/internal/hub"
)

// Server holds the HTTP server configuration and the components it connects.
type Server struct {
	port     int
	hub      *hub.Hub
	eventCh  chan *events.BabbleEvent
	staticFS fs.FS
	packsDir string
}

// New creates a Server that listens on port, serves static files from
// staticFS, and serves sound packs from packsDir. It allocates a buffered
// event channel (capacity 100) and constructs the Hub that reads from it.
func New(port int, staticFS fs.FS, packsDir string) *Server {
	eventCh := make(chan *events.BabbleEvent, 100)
	h := hub.New(eventCh)
	return &Server{
		port:     port,
		hub:      h,
		eventCh:  eventCh,
		staticFS: staticFS,
		packsDir: packsDir,
	}
}

// EventCh returns a send-only channel that callers (e.g. the session manager)
// use to push BabbleEvents into the server's broadcast pipeline.
func (s *Server) EventCh() chan<- *events.BabbleEvent {
	return s.eventCh
}

// Start launches the hub's broadcast loop in a background goroutine, registers
// the HTTP routes, and begins listening on s.port. It blocks until the server
// encounters a fatal error, which it returns.
func (s *Server) Start() error {
	go s.hub.Run()

	packsHandler := NewPacksHandler(s.packsDir)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.hub.HandleWS)
	mux.HandleFunc("GET /api/packs", packsHandler.HandleList)
	mux.HandleFunc("GET /api/packs/{name}/manifest", packsHandler.HandleManifest)
	mux.Handle("/sounds/", packsHandler.SoundsFS())
	mux.Handle("/", http.FileServer(http.FS(s.staticFS)))

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("server: listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}
