package monitor

import (
	"encoding/json"
	"net/http"
)

// Server holds the configuration for the monitor web server.
type Server struct {
	Addr string // e.g. ":8080"
	CWD  string // working directory for run discovery
}

// Start starts the HTTP server (blocking).
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/runs", s.handleAPIRuns)
	mux.HandleFunc("/", s.handleDashboard)
	return http.ListenAndServe(s.Addr, mux)
}

func (s *Server) handleAPIRuns(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	entries, err := DiscoverRuns(s.CWD, prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Return [] instead of null for empty results
	if entries == nil {
		entries = []RunEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries) //nolint:errcheck
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML)) //nolint:errcheck
}
