package massive

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"flush-detector/internal/bars"

	massiverest "github.com/massive-com/client-go/v3/rest"
	"github.com/massive-com/client-go/v3/rest/gen"
	massivews "github.com/massive-com/client-go/v3/websocket"
	wsmodels "github.com/massive-com/client-go/v3/websocket/models"
)

type Client struct {
	rest   *massiverest.Client
	ws     *massivews.Client
	log    *slog.Logger
	mu     sync.Mutex
	subs   map[string]struct{}
	status func(string)
}

func New(apiKey string, log *slog.Logger, status func(string)) (*Client, error) {
	wsClient, err := massivews.New(massivews.Config{
		APIKey: apiKey,
		Feed:   massivews.RealTime,
		Market: massivews.Stocks,
		Log:    wsLogger{log: log},
		ReconnectCallback: func(err error) {
			if err != nil {
				status("massive reconnecting")
				log.Warn("massive websocket reconnect failed", "error", err)
				return
			}
			status("massive websocket reconnected")
			log.Info("massive websocket reconnected")
		},
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		rest:   massiverest.NewWithOptions(apiKey, massiverest.WithPagination(false), massiverest.WithTrace(false)),
		ws:     wsClient,
		log:    log,
		subs:   make(map[string]struct{}),
		status: status,
	}, nil
}

func (c *Client) Close() {
	c.ws.Close()
}

func (c *Client) REST() *massiverest.Client {
	return c.rest
}

func (c *Client) Connect(ctx context.Context, symbols []string, out chan<- bars.Bar) error {
	if err := c.SyncSubscriptions(symbols); err != nil {
		return err
	}
	if err := c.ws.Connect(); err != nil {
		return err
	}
	c.status("massive websocket connected")
	go c.run(ctx, out)
	return nil
}

func (c *Client) run(ctx context.Context, out chan<- bars.Bar) {
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-c.ws.Error():
			if err == nil {
				continue
			}
			c.status("massive websocket error")
			c.log.Error("massive websocket error", "error", err)
		case msg, ok := <-c.ws.Output():
			if !ok {
				return
			}
			agg, ok := msg.(wsmodels.EquityAgg)
			if !ok {
				continue
			}
			bar := bars.Bar{
				Symbol: strings.ToUpper(agg.Symbol),
				Open:   agg.Open,
				High:   agg.High,
				Low:    agg.Low,
				Close:  agg.Close,
				Volume: agg.Volume,
				VWAP:   agg.VWAP,
				Start:  time.UnixMilli(agg.StartTimestamp),
				End:    time.UnixMilli(agg.EndTimestamp),
			}
			select {
			case <-ctx.Done():
				return
			case out <- bar:
			}
		}
	}
}

func (c *Client) SyncSubscriptions(symbols []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	next := make(map[string]struct{}, len(symbols))
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if symbol == "" {
			continue
		}
		next[symbol] = struct{}{}
	}

	var add []string
	var remove []string
	for symbol := range next {
		if _, ok := c.subs[symbol]; !ok {
			add = append(add, symbol)
		}
	}
	for symbol := range c.subs {
		if _, ok := next[symbol]; !ok {
			remove = append(remove, symbol)
		}
	}
	if len(remove) > 0 {
		if err := c.ws.Unsubscribe(massivews.StocksMinAggs, remove...); err != nil {
			return err
		}
	}
	if len(add) > 0 {
		if err := c.ws.Subscribe(massivews.StocksMinAggs, add...); err != nil {
			return err
		}
	}
	c.subs = next
	return nil
}

func (c *Client) BackfillBars(ctx context.Context, symbol string, from, to time.Time, limit int) ([]bars.Bar, error) {
	params := &gen.GetStocksAggregatesParams{
		Adjusted: massiverest.Ptr(true),
		Sort:     massiverest.String("asc"),
		Limit:    massiverest.Int(limit),
	}
	resp, err := c.rest.GetStocksAggregatesWithResponse(
		ctx,
		strings.ToUpper(symbol),
		1,
		gen.GetStocksAggregatesParamsTimespan("minute"),
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
		params,
	)
	if err != nil {
		return nil, err
	}
	if err := massiverest.CheckResponse(resp); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Results == nil {
		return nil, nil
	}
	out := make([]bars.Bar, 0, len(*resp.JSON200.Results))
	for _, item := range *resp.JSON200.Results {
		start := time.UnixMilli(int64(item.Timestamp))
		out = append(out, bars.Bar{
			Symbol: strings.ToUpper(symbol),
			Open:   item.O,
			High:   item.H,
			Low:    item.L,
			Close:  item.C,
			Volume: item.V,
			VWAP:   derefFloat(item.Vw),
			Start:  start,
			End:    start.Add(time.Minute),
		})
	}
	return out, nil
}

func (c *Client) TickerDetails(ctx context.Context, symbol string) (name string, err error) {
	resp, err := c.rest.GetTickerWithResponse(ctx, symbol, nil)
	if err != nil {
		return "", err
	}
	if err := massiverest.CheckResponse(resp); err != nil {
		return "", err
	}
	if resp.JSON200 == nil || resp.JSON200.Results == nil {
		return "", nil
	}
	return resp.JSON200.Results.Name, nil
}

type wsLogger struct {
	log *slog.Logger
}

func (w wsLogger) Debugf(template string, args ...any) {
	if w.log != nil {
		w.log.Debug(template, "args", args)
	}
}

func (w wsLogger) Infof(template string, args ...any) {
	if w.log != nil {
		w.log.Info(template, "args", args)
	}
}

func (w wsLogger) Errorf(template string, args ...any) {
	if w.log != nil {
		w.log.Error(template, "args", args)
	}
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
