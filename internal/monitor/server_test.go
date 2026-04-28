package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redblacktree/fastflow/internal/state"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	return &Server{Addr: ":0", CWD: root}, root
}

func TestHandleAPIRuns_JSON(t *testing.T) {
	srv, root := newTestServer(t)

	// Create one run so the response is non-trivial
	runDir := makeRunDir(t, root, "ENG-001")
	st := state.NewState("default", []string{"implement"}, "ENG-001", root)
	st.Status = state.StatusComplete
	if err := st.Save(runDir); err != nil {
		t.Fatalf("save state: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	rr := httptest.NewRecorder()
	srv.handleAPIRuns(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}

	var entries []RunEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v — body: %s", err, rr.Body.String())
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestHandleAPIRuns_PrefixParam(t *testing.T) {
	srv, root := newTestServer(t)

	for _, ticket := range []string{"ENG-001", "REL-100"} {
		rd := makeRunDir(t, root, ticket)
		st := state.NewState("default", []string{"implement"}, ticket, root)
		st.Status = state.StatusComplete
		if err := st.Save(rd); err != nil {
			t.Fatalf("save state: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runs?prefix=ENG-", nil)
	rr := httptest.NewRecorder()
	srv.handleAPIRuns(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}

	var entries []RunEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after prefix filter, got %d", len(entries))
	}
	if len(entries) > 0 && entries[0].Ticket != "ENG-001" {
		t.Errorf("ticket: got %q, want ENG-001", entries[0].Ticket)
	}
}

func TestHandleAPIRuns_EmptyState(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	rr := httptest.NewRecorder()
	srv.handleAPIRuns(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}

	body := strings.TrimSpace(rr.Body.String())
	// Should return [] not null
	if body != "[]" {
		t.Errorf("body: got %q, want %q", body, "[]")
	}
}

func TestHandleDashboard_ServesHTML(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.handleDashboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html prefix", ct)
	}
	if !strings.Contains(rr.Body.String(), "Fastflow Monitor") {
		t.Errorf("dashboard body should contain 'Fastflow Monitor'")
	}
}

func TestHandleAPIRuns_LogPath(t *testing.T) {
	srv, root := newTestServer(t)

	runDir := makeRunDir(t, root, "ENG-LOG")
	st := state.NewState("default", []string{"implement"}, "ENG-LOG", root)
	st.Status = state.StatusComplete
	if err := st.Save(runDir); err != nil {
		t.Fatalf("save state: %v", err)
	}
	// Create a log file
	logPath := filepath.Join(runDir, "fastflow.log")
	if err := os.WriteFile(logPath, []byte("log output"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	rr := httptest.NewRecorder()
	srv.handleAPIRuns(rr, req)

	var entries []RunEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].LogPath != logPath {
		t.Errorf("log_path: got %q, want %q", entries[0].LogPath, logPath)
	}
}
