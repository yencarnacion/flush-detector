package filings

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
	Form        string `json:"form"`
	FiledAt     string `json:"filed_at"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

type Service struct {
	client *massiverest.Client
	cfg    config.FilingsConfig
	log    *slog.Logger
	sem    chan struct{}

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	expires time.Time
	items   []Item
}

func New(client *massiverest.Client, cfg config.FilingsConfig, log *slog.Logger, maxConcurrent int) *Service {
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
	key := fmt.Sprintf("%s|%s|%d", ticker, date.Format("2006-01-02"), days)
	if items, ok := s.fromCache(key); ok {
		return items, nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.sem <- struct{}{}:
	}
	defer func() { <-s.sem }()

	start := date.AddDate(0, 0, -days).Format("2006-01-02")
	end := date.Format("2006-01-02")
	sort := "filing_date.desc"
	params := &gen.GetStocksFilingsVXIndexParams{
		Ticker:        &ticker,
		FilingDateGte: &start,
		FilingDateLte: &end,
		Limit:         massiverest.Int(s.cfg.MaxItems),
		Sort:          &sort,
	}

	resp, err := s.client.GetStocksFilingsVXIndexWithResponse(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := massiverest.CheckResponse(resp); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	items := make([]Item, 0, len(resp.JSON200.Results))
	for _, result := range resp.JSON200.Results {
		items = append(items, Item{
			Form:        derefString(result.FormType),
			FiledAt:     dateString(result.FilingDate),
			Description: derefString(result.IssuerName),
			URL:         derefString(result.FilingUrl),
		})
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

func dateString(v interface{ String() string }) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
