package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestFilterAlertCSVBySymbols(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "alerts.csv")
	output := filepath.Join(dir, "filtered.csv")

	if err := os.WriteFile(input, []byte("alert_id,symbol,price\n1,AMD,10\n2,NVDA,20\n3,TSLA,30\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := filterAlertCSVBySymbols(input, output, map[string]struct{}{
		"AMD":  {},
		"TSLA": {},
	})
	if err != nil {
		t.Fatalf("filterAlertCSVBySymbols error = %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want header + 2 records: %#v", len(rows), rows)
	}
	if rows[1][1] != "AMD" || rows[2][1] != "TSLA" {
		t.Fatalf("unexpected filtered rows: %#v", rows)
	}
}
