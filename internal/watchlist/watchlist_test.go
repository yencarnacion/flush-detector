package watchlist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergeWatchlists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "one.yaml")
	second := filepath.Join(dir, "two.yaml")
	if err := os.WriteFile(first, []byte(`
watchlist:
  - symbol: "AAPL"
    name: "Apple Inc."
  - symbol: "TSLA"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`
watchlist:
  - symbol: "tsla"
    name: "Tesla, Inc."
  - symbol: "NVDA"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := Load([]string{first, second})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[1].Symbol != "TSLA" || items[1].Name != "Tesla, Inc." {
		t.Fatalf("merged TSLA = %+v", items[1])
	}
	if len(items[1].Sources) != 2 {
		t.Fatalf("TSLA sources = %v, want 2 items", items[1].Sources)
	}
}
