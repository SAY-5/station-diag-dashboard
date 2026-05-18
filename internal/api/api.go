// Package api wires the REST handlers and the WebSocket endpoint onto an
// http.ServeMux. Handlers are thin: they marshal between HTTP and the
// store, hub and rule engine.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/hub"
	"github.com/SAY-5/station-diag-dashboard/internal/store"
	"github.com/gorilla/websocket"
)

// Server holds the dependencies the HTTP handlers need.
type Server struct {
	store    *store.Store
	hub      *hub.Hub
	logger   *slog.Logger
	upgrader websocket.Upgrader
	static   fs.FS
}

// New constructs an API server. staticFS supplies the embedded frontend.
func New(st *store.Store, h *hub.Hub, staticFS fs.FS, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:  st,
		hub:    h,
		logger: logger,
		static: staticFS,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			// The dashboard is a same-origin internal tool; allow all
			// origins so the embedded page works behind any host name.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// Routes returns an http.Handler with every endpoint registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/runs/", s.handleRunSubpath)
	mux.HandleFunc("/ws", s.handleWS)
	mux.Handle("/", http.FileServer(http.FS(s.static)))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := s.store.ListRuns(limit)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	if runs == nil {
		runs = []store.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleRunSubpath dispatches /api/runs/{id}, /api/runs/{id}/notes,
// /api/runs/{id}/export and /api/runs/{id}/resolve.
func (s *Server) handleRunSubpath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]

	switch {
	case len(parts) == 1:
		s.handleRunGet(w, r, runID)
	case len(parts) == 2 && parts[1] == "notes":
		s.handleNotes(w, r, runID)
	case len(parts) == 2 && parts[1] == "export":
		s.handleExport(w, r, runID)
	case len(parts) == 2 && parts[1] == "resolve":
		s.handleResolve(w, r, runID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	run, err := s.store.GetRun(runID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	events, _ := s.store.RunEvents(runID)
	failures, _ := s.store.RunFailures(runID)
	notes, _ := s.store.RunNotes(runID)
	writeJSON(w, http.StatusOK, map[string]any{
		"run":      run,
		"events":   events,
		"failures": failures,
		"notes":    notes,
	})
}

type noteRequest struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req noteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		s.fail(w, http.StatusBadRequest, errors.New("note body is required"))
		return
	}
	run, err := s.store.GetRun(runID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	note, err := s.store.AddNote(run.StationID, runID, req.Author, req.Body)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	s.hub.PublishNote(hub.Note{
		ID: note.ID, StationID: note.StationID, RunID: note.RunID,
		Author: note.Author, Body: note.Body,
		CreatedAt: note.CreatedAt.Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, note)
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Resolved bool `json:"resolved"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, errors.New("invalid JSON body"))
		return
	}
	if err := s.store.SetResolved(runID, req.Resolved); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.NotFound(w, r)
			return
		}
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "resolved": req.Resolved})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	run, err := s.store.GetRun(runID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	events, _ := s.store.RunEvents(runID)
	failures, _ := s.store.RunFailures(runID)
	notes, _ := s.store.RunNotes(runID)

	rows := make([]failureRow, 0, len(failures))
	for _, f := range failures {
		rows = append(rows, failureRow{
			RuleID: f.RuleID, Actuator: f.Actuator, Detail: f.Detail,
			Severity: f.Severity, At: f.At,
		})
	}
	md := renderMarkdown(reportData{Run: run, Events: events, Failures: rows, Notes: notes})

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\"run-"+sanitizeFilename(runID)+".md\"")
	_, _ = w.Write([]byte(md))
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	lastSeq := int64(0)
	if q := r.URL.Query().Get("last_seq"); q != "" {
		if n, err := strconv.ParseInt(q, 10, 64); err == nil && n >= 0 {
			lastSeq = n
		}
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn("websocket upgrade failed", "error", err)
		return
	}
	NewWSConn(conn, s.hub, lastSeq, s.logger).Serve(r.Context())
}

func (s *Server) fail(w http.ResponseWriter, code int, err error) {
	s.logger.Warn("api error", "code", code, "error", err)
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}
