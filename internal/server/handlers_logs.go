package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

func (s *Server) handleStreamLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	task, err := s.store.GetTask(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		s.logger.Error("get task for logs", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	lastID := 0

	// If task is already terminal, send all logs and done immediately.
	if isTerminal(task.Status) {
		s.flushLogs(r.Context(), w, flusher, id, &lastID)
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.flushLogs(r.Context(), w, flusher, id, &lastID)

			task, err = s.store.GetTask(r.Context(), id)
			if err != nil {
				return
			}
			if isTerminal(task.Status) {
				s.flushLogs(r.Context(), w, flusher, id, &lastID)
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func (s *Server) flushLogs(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, taskID string, lastID *int) {
	logs, err := s.store.ListTaskLogsSince(ctx, taskID, *lastID)
	if err != nil {
		s.logger.Error("list logs since", "error", err)
		return
	}

	for _, l := range logs {
		data, _ := json.Marshal(l)
		fmt.Fprintf(w, "data: %s\n\n", data)
		*lastID = l.ID
	}
	if len(logs) > 0 {
		flusher.Flush()
	}
}

func isTerminal(status string) bool {
	return status == "complete" || status == "failed"
}
