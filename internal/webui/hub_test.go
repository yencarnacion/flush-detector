package webui

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"flush-detector/internal/flush"
)

func TestHubHistoryLimit(t *testing.T) {
	t.Parallel()

	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), 2)
	hub.AddAlert(flush.Alert{ID: "1", AlertTime: time.Now()})
	hub.AddAlert(flush.Alert{ID: "2", AlertTime: time.Now()})
	hub.AddAlert(flush.Alert{ID: "3", AlertTime: time.Now()})

	history := hub.History()
	if len(history) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(history))
	}
	if history[0].ID != "2" || history[1].ID != "3" {
		t.Fatalf("history = %+v, want last two alerts", history)
	}
}
