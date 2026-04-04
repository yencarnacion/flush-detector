package watchlist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Entry struct {
	Symbol string `yaml:"symbol" json:"symbol"`
	Name   string `yaml:"name,omitempty" json:"name,omitempty"`
}

type File struct {
	Watchlist []Entry `yaml:"watchlist" json:"watchlist"`
}

type Symbol struct {
	Symbol  string   `json:"symbol"`
	Name    string   `json:"name,omitempty"`
	Sources []string `json:"sources,omitempty"`
}

func ParsePaths(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func SourceLabel(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	if base == "" {
		return ""
	}
	ext := filepath.Ext(base)
	label := strings.TrimSuffix(base, ext)
	if label == "" {
		return base
	}
	return label
}

func Load(paths []string) ([]Symbol, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no watchlist files provided")
	}
	merged := make([]Symbol, 0, 128)
	index := make(map[string]int, 128)
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var file File
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		source := SourceLabel(path)
		for _, item := range file.Watchlist {
			sym := strings.ToUpper(strings.TrimSpace(item.Symbol))
			if sym == "" {
				continue
			}
			pos, ok := index[sym]
			if !ok {
				merged = append(merged, Symbol{
					Symbol:  sym,
					Name:    strings.TrimSpace(item.Name),
					Sources: appendUnique(nil, source),
				})
				index[sym] = len(merged) - 1
				continue
			}
			if merged[pos].Name == "" && strings.TrimSpace(item.Name) != "" {
				merged[pos].Name = strings.TrimSpace(item.Name)
			}
			merged[pos].Sources = appendUnique(merged[pos].Sources, source)
		}
	}
	if len(merged) == 0 {
		return nil, fmt.Errorf("watchlist is empty")
	}
	return merged, nil
}

func Symbols(items []Symbol) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Symbol != "" {
			out = append(out, item.Symbol)
		}
	}
	return out
}

func ByTicker(items []Symbol) map[string]Symbol {
	out := make(map[string]Symbol, len(items))
	for _, item := range items {
		out[item.Symbol] = item
	}
	return out
}

func appendUnique(in []string, value string) []string {
	if value == "" {
		return in
	}
	for _, existing := range in {
		if existing == value {
			return in
		}
	}
	return append(in, value)
}
