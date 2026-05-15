package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDashboardWithModeUsesUpWording(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "alerts_20260511.csv")
	outputPath := filepath.Join(dir, "dashboard_20260511.html")

	csv := strings.Join([]string{
		"alert_id,alert_time_et,session_date,symbol,name,sources,operating_mode,price,flush_score,tier,drop_from_prior_30m_high_pct,distance_below_vwap_pct,roc_5m_pct,roc_10m_pct,down_slope_20m_pct_per_bar,range_expansion,volume_expansion,volume_since_4am,summary,gap_percent",
		"NOW-1,2026-05-11 09:35:00,2026-05-11,NOW,NOW Inc,1000-list,up,93.70,80.2,Strong,4.2,2.0,3.0,1.7,0.100,1.7,1.7,2056098,,",
		"",
	}, "\n")
	if err := os.WriteFile(inputPath, []byte(csv), 0o644); err != nil {
		t.Fatalf("write input csv: %v", err)
	}

	if _, err := GenerateDashboardWithMode(inputPath, outputPath, "http://localhost:8081", "09:30", "up"); err != nil {
		t.Fatalf("GenerateDashboardWithMode: %v", err)
	}

	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output dashboard: %v", err)
	}
	html := string(body)

	required := []string{
		"Up To Polygon Signal Converter",
		"Rise From Prior 30m Low",
		"Distance Above VWAP",
		"5m Upside ROC",
		"20m Upside Slope",
		"hard rip",
		"panic chase",
		"over-vwap",
		"4.2% above prior 30m low, 2.0% above VWAP, 5m ROC +3.0%",
	}
	for _, text := range required {
		if !strings.Contains(html, text) {
			t.Fatalf("dashboard missing %q", text)
		}
	}

	if !strings.Contains(html, `"operating_mode":"up"`) {
		t.Fatalf("dashboard payload missing up operating mode")
	}
}
