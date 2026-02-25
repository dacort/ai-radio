package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/dacort/babble/internal/config"
)

// ConfigHandler serves GET and PUT /api/config, persisting configuration to
// and from a JSON file on disk.
type ConfigHandler struct {
	configPath string
}

// NewConfigHandler returns a ConfigHandler that reads from and writes to the
// file at configPath.
func NewConfigHandler(configPath string) *ConfigHandler {
	return &ConfigHandler{configPath: configPath}
}

// HandleGet handles GET /api/config. It loads the current config from disk
// (returning defaults when the file is absent) and writes it as JSON.
func (h *ConfigHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(h.configPath)
	if err != nil {
		log.Printf("config: load %s: %v", h.configPath, err)
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		log.Printf("config: encode response: %v", err)
	}
}

// HandleUpdate handles PUT /api/config. It decodes the JSON request body,
// saves it to disk, and returns the updated config as JSON. Unknown fields in
// the request body are silently ignored.
func (h *ConfigHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(h.configPath)
	if err != nil {
		log.Printf("config: load for update %s: %v", h.configPath, err)
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.Save(cfg, h.configPath); err != nil {
		log.Printf("config: save %s: %v", h.configPath, err)
		http.Error(w, "failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		log.Printf("config: encode update response: %v", err)
	}
}
