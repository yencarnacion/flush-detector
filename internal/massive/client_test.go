package massive

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWSLoggerFormatsTemplate(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	w := wsLogger{log: logger}

	w.Errorf("r/w threads closed: %v", "boom")

	got := buf.String()
	if !strings.Contains(got, "r/w threads closed: boom") {
		t.Fatalf("expected formatted log message, got: %q", got)
	}
	if strings.Contains(got, "%v") {
		t.Fatalf("did not expect unresolved %%v token in log output: %q", got)
	}
}

func TestWSLoggerSuppressesForceDisconnectNoise(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	w := wsLogger{log: logger}

	w.Infof("unknown status message '%v': %v", "force_disconnect", "The server has forcefully disconnected.")

	if got := buf.String(); got != "" {
		t.Fatalf("expected no info/error log output for force_disconnect noise, got: %q", got)
	}
}

func TestWSLoggerSuppressesTransientClose1012Noise(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	w := wsLogger{log: logger}

	w.Errorf("r/w threads closed: %v", "connection closed unexpectedly: websocket: close 1012")

	if got := buf.String(); got != "" {
		t.Fatalf("expected no info/error log output for transient close 1012, got: %q", got)
	}
}
