package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
server_port: 9001
flush:
  session: "rth"
  start_time: "09:45"
  end_time: "15:20"
  min_alert_score: 72.5
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ServerPort != 9001 {
		t.Fatalf("ServerPort = %d, want 9001", cfg.ServerPort)
	}
	if cfg.Flush.StartTime != "09:45" {
		t.Fatalf("StartTime = %s, want 09:45", cfg.Flush.StartTime)
	}
	if cfg.Flush.MinAlertScore != 72.5 {
		t.Fatalf("MinAlertScore = %.1f, want 72.5", cfg.Flush.MinAlertScore)
	}
	if cfg.Alert.CooldownSeconds != 10 {
		t.Fatalf("default cooldown not applied, got %d", cfg.Alert.CooldownSeconds)
	}
}
