package massive

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"flush-detector/internal/bars"
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

func TestClientCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	client := &Client{
		log:      slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		cacheDir: dir,
	}
	path := client.cachePath("minute-bars", "AAPL", "2026-04-10")
	want := []bars.Bar{{Symbol: "AAPL", Open: 10, Close: 11}}
	client.writeCache(path, want)

	var got []bars.Bar
	if !client.readCache(path, &got) {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 || got[0].Symbol != "AAPL" || got[0].Close != 11 {
		t.Fatalf("cached bars = %+v, want %+v", got, want)
	}
	if !strings.HasPrefix(path, filepath.Clean(dir)) {
		t.Fatalf("cache path = %q, want under %q", path, dir)
	}
}
