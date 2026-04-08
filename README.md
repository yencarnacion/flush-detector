# flush-detector

`flush-detector` is a Go web app for a shared intraday stage-1 mean-reversion scanner. It watches a YAML watchlist, consumes live Massive stock minute aggregates, computes a 0-100 flush score on completed 1-minute bars, and pushes ranked alerts to a browser over websocket. It mirrors the operational style of `alertcat`: one Go binary, static web assets, `.env`, `config.yaml`, `watchlist.yaml`, live reload, recent history replay, and alert sounds.

## What it does

- Loads `MASSIVE_API_KEY` from `.env`
- Loads runtime settings from `config.yaml`
- Loads one or more watchlists from YAML
- Backfills recent 1-minute bars at startup for warmup
- Streams live stock minute aggregates from Massive
- Calculates the shared flush score on completed 1-minute bars only
- Emits live browser alerts with score tiers, metric breakdown, news, and SEC filings
- Appends each triggered alert to a daily CSV log under `./log/alerts_YYYYMMDD.csv`
- Replays recent alert history to new websocket clients
- Supports historical day replay from the UI with per-day calendar selection
- Supports watchlist reload without restarting
- Supports live threshold/window changes from the UI

## What it is not yet

This repo only implements stage 1: the shared flush detector and flush score. It does not yet classify:

- flush-base (FB)
- rubberband (RB)
- right side of the V (V)

The code is intentionally layered so future stage-2 logic can be added under:

- `internal/flush/fb.go`
- `internal/flush/rb.go`
- `internal/flush/v.go`

## How it differs from alertcat

- `alertcat` is centered on alert/rvol workflows; `flush-detector` is centered on stretched downside flush scoring.
- This project uses Massive v3 naming throughout.
- The primary env var is `MASSIVE_API_KEY` instead of legacy Polygon naming.
- Alert cards focus on flush metrics: drop from prior 30m high, distance below VWAP, short-horizon downside ROC, slope, and expansion factors.

## Setup

1. Copy `.env.example` to `.env`
2. Set `MASSIVE_API_KEY`
3. Edit `config.yaml`
4. Edit `watchlist.yaml`
5. Run `go mod tidy`
6. Run `make run`
7. Open `http://localhost:8087` unless you changed the port

`.env.example`

```bash
MASSIVE_API_KEY=your_api_key_here
```

## Config

The app reads `config.yaml` on startup. Important fields:

- `server_port`: HTTP port, default `8087`
- `alert.cooldown_seconds`: per-symbol cooldown
- `ui.chart_opener_base_url`: used for ticker click/open-chart behavior
- `flush.start_time` and `flush.end_time`: active ET alert window
- `flush.min_volume_since_4am`: cumulative volume required from 04:00 ET to alert time
- `flush.min_alert_score`: live threshold
- `flush.backfill_bars`: startup warmup depth
- `news` and `filings`: enrichment toggles, item counts, and cache TTL

`Apply Live` in the UI updates the running detector without restart. That is an in-memory runtime change; edit `config.yaml` if you want the same values on next boot.

## Watchlists

Default file:

```yaml
watchlist:
  - symbol: "AAPL"
    name: "Apple Inc."
  - symbol: "TSLA"
  - symbol: "NVDA"
```

You can also merge multiple files:

```bash
go run . -watchlists watchlist.yaml,watchlist-02.yaml
```

Behavior:

- duplicate symbols are merged
- the first non-empty company name wins
- alert cards show source tags derived from filename stems

Use the browser `Reload Watchlists` control or `POST /api/watchlist/reload` to re-read the files without restart.

## Flush score

The stage-1 detector scores downside stretch on completed 1-minute bars using only information available at that bar.

Metrics:

- drop from prior 30-minute high
- distance below session VWAP
- 5-minute downside ROC
- 10-minute downside ROC
- 20-minute downside regression slope
- 3-bar vs prior-10-bar range expansion
- 3-bar vs prior-10-bar volume expansion

Score formula:

```text
25 * clip(drop_from_prior_30m_high_pct / 4.0, 0, 1) +
20 * clip(distance_below_vwap_pct       / 2.0, 0, 1) +
15 * clip(roc_5m_pct                    / 1.5, 0, 1) +
10 * clip(roc_10m_pct                   / 2.5, 0, 1) +
10 * clip(down_slope_20m_pct_per_bar    / 0.15, 0, 1) +
10 * clip((range_expansion - 1.0)       / 1.5, 0, 1) +
10 * clip((volume_expansion - 1.0)      / 2.0, 0, 1)
```

Tier labels:

- `0-39`: Low
- `40-59`: Notable
- `60-74`: Candidate
- `75-89`: Strong
- `90-100`: Extreme

Browser-only metric bands:

The alert card UI also assigns each flush metric a `1-5` severity score. That banding is UI-only: it does not change the raw metric values in websocket payloads or the CSV log output under `./log`.

| Metric | 1 | 2 | 3 | 4 | 5 |
| --- | --- | --- | --- | --- | --- |
| Drop from prior 30m high | `< 1.0%` noise | `1.0% to < 2.0%` pullback | `2.0% to < 3.0%` flush | `3.0% to 4.0%` hard flush | `> 4.0%` washout |
| Distance below VWAP | `< 0.5%` near value | `0.5% to < 1.0%` weak | `1.0% to < 2.0%` stretched | `2.0% to 3.0%` deeply stretched | `> 3.0%` dislocated |
| 5m downside ROC | `< 0.3%` slow | `0.3% to < 0.7%` selling | `0.7% to < 1.2%` aggressive selling | `1.2% to 1.8%` flush impulse | `> 1.8%` panic burst |
| 10m downside ROC | `< 0.5%` drift | `0.5% to < 1.0%` sustained weakness | `1.0% to < 2.0%` strong pressure | `2.0% to 3.0%` trend flush | `> 3.0%` one-sided pressure |
| 20m downside slope | `< 0.03% / bar` drift | `0.03% to < 0.07% / bar` controlled bleed | `0.07% to < 0.12% / bar` trend pressure | `0.12% to 0.18% / bar` heavy pressure | `> 0.18% / bar` relentless trend |
| Range expansion | `<= x1.0` normal | `> x1.0 to < x1.3` building | `x1.3 to < x1.6` expanding | `x1.6 to x2.0` emotional | `> x2.0` washout-like |
| Volume expansion | `<= x1.0` routine | `> x1.0 to < x1.5` active | `x1.5 to < x2.0` crowded | `x2.0 to x3.0` forced | `> x3.0` capitulation-like |

## Browser UI

The UI provides:

- pinned alerts
- live stream
- historical replay calendar with month navigation
- websocket connection/status banner
- search/filter by ticker or name
- sort by time or score
- per-metric `1-5` severity badges on alert cards
- sound toggle
- live settings apply
- chart open button
- async news/SEC filings enrichment via `/api/extra`

## HTTP endpoints

- `GET /`
- `GET /news.html`
- `GET /ws`
- `GET /api/health`
- `GET /api/config`
- `POST /api/config/apply`
- `GET /api/watchlist`
- `POST /api/watchlist/reload`
- `GET /api/history`
- `GET /api/replay-calendar?month=YYYY-MM`
- `POST /api/replay-day`
- `POST /api/replay-live`
- `GET /api/extra?ticker=XYZ&date=YYYY-MM-DD&days=2`
- `GET /alert.wav`
- `GET /alert-up.wav`
- `GET /alert-down.wav`
- `GET /flush.wav`

If sound files are missing, the app synthesizes a simple fallback WAV beep in memory so browser alerts still work.

## Development

Targets:

```bash
make run
make test
make build
```

## Notes

- Massive is used as the market-data foundation through the official Go v3 client.
- Startup backfill uses Massive REST aggregates.
- Live streaming uses Massive stock minute aggregate websocket subscriptions.
- Historical replay rewrites `./log/alerts_YYYYMMDD.csv` for the replayed session date before regenerating alerts for that day.
- News and SEC filings are loaded on demand for each alert card and cached server-side.
- Triggered alerts are also written to one CSV file per day in `./log`. The CSV includes alert time, symbol, score, tier, price, source tags, cumulative 04:00 ET volume, and the core flush metrics, but it does not include news or SEC filing enrichment.
