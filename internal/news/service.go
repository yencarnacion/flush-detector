package news

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"flush-detector/internal/config"

	massiverest "github.com/massive-com/client-go/v3/rest"
	"github.com/massive-com/client-go/v3/rest/gen"
)

type Item struct {
	Title        string `json:"title"`
	PublishedUTC string `json:"published_utc"`
	Source       string `json:"source"`
	URL          string `json:"url"`
	Summary      string `json:"summary"`
}

type Service struct {
	client *massiverest.Client
	cfg    config.NewsConfig
	log    *slog.Logger
	sem    chan struct{}

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	expires time.Time
	items   []Item
}

func New(client *massiverest.Client, cfg config.NewsConfig, log *slog.Logger, maxConcurrent int) *Service {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &Service{
		client: client,
		cfg:    cfg,
		log:    log,
		sem:    make(chan struct{}, maxConcurrent),
		cache:  make(map[string]cacheEntry),
	}
}

func (s *Service) Get(ctx context.Context, ticker string, date time.Time, days int) ([]Item, error) {
	if !s.cfg.Enabled {
		return nil, nil
	}
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return nil, nil
	}
	if days <= 0 {
		days = s.cfg.LookbackDays
	}
	key := strings.Join([]string{ticker, date.Format("2006-01-02"), fmt.Sprintf("%d", days)}, "|")
	if items, ok := s.fromCache(key); ok {
		return items, nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.sem <- struct{}{}:
	}
	defer func() { <-s.sem }()

	limit := s.cfg.MaxItems * 5
	if limit < 20 {
		limit = 20
	}
	order := gen.ListNewsParamsOrderDesc
	sort := gen.PublishedUtc
	params := &gen.ListNewsParams{
		Ticker: &ticker,
		Order:  &order,
		Sort:   &sort,
		Limit:  massiverest.Int(limit),
	}

	resp, err := s.client.ListNewsWithResponse(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := massiverest.CheckResponse(resp); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Results == nil {
		return nil, nil
	}

	cutoff := date.AddDate(0, 0, -days)
	items := make([]Item, 0, s.cfg.MaxItems)
	for _, result := range *resp.JSON200.Results {
		if result.PublishedUtc.Before(cutoff) {
			continue
		}
		items = append(items, Item{
			Title:        result.Title,
			PublishedUTC: result.PublishedUtc.UTC().Format(time.RFC3339),
			Source:       result.Publisher.Name,
			URL:          result.ArticleUrl,
			Summary:      derefString(result.Description),
		})
		if len(items) >= s.cfg.MaxItems {
			break
		}
	}
	s.toCache(key, items)
	return items, nil
}

func (s *Service) fromCache(key string) ([]Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(s.cache, key)
		}
		return nil, false
	}
	out := make([]Item, len(entry.items))
	copy(out, entry.items)
	return out, true
}

func (s *Service) toCache(key string, items []Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Item, len(items))
	copy(cp, items)
	s.cache[key] = cacheEntry{
		expires: time.Now().Add(time.Duration(s.cfg.CacheTTLSeconds) * time.Second),
		items:   cp,
	}
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
