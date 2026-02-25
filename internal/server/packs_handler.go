package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/dacort/babble/internal/packs"
)

// PacksHandler serves sound pack metadata and audio files over HTTP.
type PacksHandler struct {
	packsDir string
}

// NewPacksHandler returns a PacksHandler rooted at packsDir.
func NewPacksHandler(packsDir string) *PacksHandler {
	return &PacksHandler{packsDir: packsDir}
}

// HandleList handles GET /api/packs. It lists all loadable packs in packsDir
// and writes them as a JSON array. The Dir field is omitted from each pack
// because it is tagged json:"-".
func (h *PacksHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	ps, err := packs.ListPacks(h.packsDir)
	if err != nil {
		log.Printf("packs: list %s: %v", h.packsDir, err)
		http.Error(w, "failed to list packs", http.StatusInternalServerError)
		return
	}

	// Return an empty JSON array rather than null when there are no packs.
	if ps == nil {
		ps = []*packs.Pack{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ps); err != nil {
		log.Printf("packs: encode list response: %v", err)
	}
}

// HandleManifest handles GET /api/packs/{name}/manifest. It loads and returns
// the manifest for the named pack. Returns 404 if the pack cannot be loaded.
func (h *PacksHandler) HandleManifest(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing pack name", http.StatusBadRequest)
		return
	}

	// Sanitize: reject any name containing a path separator so callers cannot
	// traverse outside packsDir.
	for _, c := range name {
		if c == '/' || c == '\\' {
			http.Error(w, "invalid pack name", http.StatusBadRequest)
			return
		}
	}

	packDir := h.packsDir + "/" + name
	p, err := packs.LoadPack(packDir)
	if err != nil {
		log.Printf("packs: load %s: %v", packDir, err)
		http.Error(w, "pack not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(p); err != nil {
		log.Printf("packs: encode manifest response: %v", err)
	}
}

// SoundsFS returns an http.Handler that serves audio files from packsDir
// under the URL prefix /sounds/. A GET to /sounds/default/ambient.ogg maps
// to packsDir/default/ambient.ogg.
func (h *PacksHandler) SoundsFS() http.Handler {
	return http.StripPrefix("/sounds/", http.FileServer(http.Dir(h.packsDir)))
}
