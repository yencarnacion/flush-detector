package dashboard

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const timeLayout = "2006-01-02 15:04:05"
const defaultSessionStartTime = "09:40"

var nyLocation = mustLocation("America/New_York")

type Alert struct {
	SourceOrder             int       `json:"source_order"`
	AlertID                 string    `json:"alert_id"`
	AlertTimeET             time.Time `json:"-"`
	AlertTimeText           string    `json:"alert_time_et"`
	SessionDate             string    `json:"session_date"`
	Symbol                  string    `json:"symbol"`
	Name                    string    `json:"name"`
	Sources                 string    `json:"sources"`
	Price                   float64   `json:"price"`
	FlushScore              float64   `json:"flush_score"`
	Tier                    string    `json:"tier"`
	DropFromPrior30mHighPct float64   `json:"drop_from_prior_30m_high_pct"`
	DistanceBelowVWAPPct    float64   `json:"distance_below_vwap_pct"`
	ROC5mPct                float64   `json:"roc_5m_pct"`
	ROC10mPct               float64   `json:"roc_10m_pct"`
	DownSlope20mPctPerBar   float64   `json:"down_slope_20m_pct_per_bar"`
	RangeExpansion          float64   `json:"range_expansion"`
	VolumeExpansion         float64   `json:"volume_expansion"`
	VolumeSince4AM          float64   `json:"volume_since_4am"`
	Summary                 string    `json:"summary"`
	Signal                  string    `json:"signal"`
	SignalTime              string    `json:"signal_time"`
	SignalTimeDisplay       string    `json:"signal_time_display"`
	OpenChartURL            string    `json:"open_chart_url"`
	ScoreBucket             int       `json:"score_bucket"`
	MinutesFromOpen         int       `json:"minutes_from_open"`
	MinutesFromSessionStart int       `json:"minutes_from_session_start"`
	RelativeStrengthLabel   string    `json:"relative_strength_label"`
	SetupQuality            string    `json:"setup_quality"`
}

type ConversionResult struct {
	Alerts         []Alert
	SessionDate    string
	SignalCSVPath  string
	DashboardPath  string
	SourceFile     string
	GeneratedAt    time.Time
	ChartBaseURL   string
	SignalType     string
	UniqueSymbols  int
	AverageScore   float64
	MaxScore       float64
	MinScore       float64
	TopSymbol      string
	TopSymbolCount int
}

type pageModel struct {
	Title         string
	SessionDate   string
	SourceFile    string
	GeneratedAt   string
	SignalCSVPath string
	ChartBaseURL  string
	AlertCount    int
	UniqueSymbols int
	AverageScore  string
	MaxScore      string
	MinScore      string
	TopSymbol     string
	AlertsJSON    template.JS
}

type DashboardResult struct {
	SessionDate   string
	AlertCount    int
	DashboardPath string
	SignalCSVPath string
}

// GenerateDashboard renders the flush2polygon-style dashboard from a flush-detector alert CSV.
func GenerateDashboard(inputCSVPath, outputHTMLPath, chartBaseURL string) (*DashboardResult, error) {
	return GenerateDashboardWithSessionStart(inputCSVPath, outputHTMLPath, chartBaseURL, defaultSessionStartTime)
}

// GenerateDashboardWithSessionStart renders the dashboard and computes session-relative timing
// using the provided HH:MM start (for example the live detector flush.start_time).
func GenerateDashboardWithSessionStart(inputCSVPath, outputHTMLPath, chartBaseURL, sessionStartTime string) (*DashboardResult, error) {
	alerts, err := parseAlerts(inputCSVPath, "buy", chartBaseURL, sessionStartTime)
	if err != nil {
		return nil, err
	}
	if len(alerts) == 0 {
		return nil, errors.New("input file contains no alerts")
	}

	if err := os.MkdirAll(filepath.Dir(outputHTMLPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	base := strings.TrimSuffix(outputHTMLPath, filepath.Ext(outputHTMLPath))
	signalCSVPath := base + "_polygon_signals.csv"
	if err := writeSignalCSV(signalCSVPath, alerts); err != nil {
		return nil, err
	}
	if err := writeDashboardHTML(outputHTMLPath, inputCSVPath, signalCSVPath, chartBaseURL, alerts); err != nil {
		return nil, err
	}

	return &DashboardResult{
		SessionDate:   alerts[0].SessionDate,
		AlertCount:    len(alerts),
		DashboardPath: outputHTMLPath,
		SignalCSVPath: signalCSVPath,
	}, nil
}

func main() {
	var (
		inputPath    string
		outputDir    string
		signalType   string
		chartBaseURL string
	)

	flag.StringVar(&inputPath, "input", "", "Path to flush-detector alert CSV")
	flag.StringVar(&outputDir, "out-dir", ".", "Directory for generated files")
	flag.StringVar(&signalType, "signal", "buy", "Signal type for polygon-charts rows: buy or sell")
	flag.StringVar(&chartBaseURL, "chart-base-url", "http://localhost:8081", "Base URL for polygon-charts deep links")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "flush to polygon signal converter\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  %s [flags] <alerts.csv>\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if inputPath == "" && flag.NArg() > 0 {
		inputPath = flag.Arg(0)
	}
	if strings.TrimSpace(inputPath) == "" {
		flag.Usage()
		exitWithError(errors.New("missing alert CSV path"))
	}

	result, err := convertFile(inputPath, outputDir, signalType, chartBaseURL)
	if err != nil {
		exitWithError(err)
	}

	fmt.Printf("Converted %d alerts for %s\n", len(result.Alerts), result.SessionDate)
	fmt.Printf("Signal CSV: %s\n", result.SignalCSVPath)
	fmt.Printf("Dashboard : %s\n", result.DashboardPath)
}

func convertFile(inputPath, outputDir, signalType, chartBaseURL string) (*ConversionResult, error) {
	signalType = strings.ToLower(strings.TrimSpace(signalType))
	if signalType != "buy" && signalType != "sell" {
		return nil, fmt.Errorf("signal must be buy or sell, got %q", signalType)
	}

	alerts, err := parseAlerts(inputPath, signalType, chartBaseURL, defaultSessionStartTime)
	if err != nil {
		return nil, err
	}
	if len(alerts) == 0 {
		return nil, errors.New("input file contains no alerts")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	sessionDate := alerts[0].SessionDate
	signalCSVPath := filepath.Join(outputDir, base+"_polygon_signals.csv")
	dashboardPath := filepath.Join(outputDir, base+"_dashboard.html")

	if err := writeSignalCSV(signalCSVPath, alerts); err != nil {
		return nil, err
	}
	if err := writeDashboardHTML(dashboardPath, inputPath, signalCSVPath, chartBaseURL, alerts); err != nil {
		return nil, err
	}

	uniqueSymbols, avgScore, minScore, maxScore, topSymbol, topCount := summarizeAlerts(alerts)
	return &ConversionResult{
		Alerts:         alerts,
		SessionDate:    sessionDate,
		SignalCSVPath:  signalCSVPath,
		DashboardPath:  dashboardPath,
		SourceFile:     inputPath,
		GeneratedAt:    time.Now(),
		ChartBaseURL:   chartBaseURL,
		SignalType:     signalType,
		UniqueSymbols:  uniqueSymbols,
		AverageScore:   avgScore,
		MaxScore:       maxScore,
		MinScore:       minScore,
		TopSymbol:      topSymbol,
		TopSymbolCount: topCount,
	}, nil
}

func parseAlerts(path, signalType, chartBaseURL, sessionStartTime string) ([]Alert, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open input file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	index := map[string]int{}
	for i, field := range header {
		index[strings.TrimSpace(field)] = i
	}

	required := []string{
		"alert_id",
		"alert_time_et",
		"session_date",
		"symbol",
		"sources",
		"price",
		"flush_score",
		"tier",
		"drop_from_prior_30m_high_pct",
		"distance_below_vwap_pct",
		"roc_5m_pct",
		"roc_10m_pct",
		"down_slope_20m_pct_per_bar",
		"range_expansion",
		"volume_expansion",
		"volume_since_4am",
	}
	for _, key := range required {
		if _, ok := index[key]; !ok {
			return nil, fmt.Errorf("missing required column %q", key)
		}
	}

	alerts := make([]Alert, 0, 512)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv row: %w", err)
		}

		alertTimeText := field(record, index, "alert_time_et")
		alertTime, err := time.ParseInLocation(timeLayout, alertTimeText, nyLocation)
		if err != nil {
			return nil, fmt.Errorf("parse alert_time_et %q: %w", alertTimeText, err)
		}

		symbol := strings.ToUpper(strings.TrimSpace(field(record, index, "symbol")))
		sessionDate := strings.TrimSpace(field(record, index, "session_date"))
		alert := Alert{
			SourceOrder:             len(alerts),
			AlertID:                 strings.TrimSpace(field(record, index, "alert_id")),
			AlertTimeET:             alertTime,
			AlertTimeText:           alertTimeText,
			SessionDate:             sessionDate,
			Symbol:                  symbol,
			Name:                    strings.TrimSpace(field(record, index, "name")),
			Sources:                 strings.TrimSpace(field(record, index, "sources")),
			Price:                   parseFloat(field(record, index, "price")),
			FlushScore:              roundTo(parseFloat(field(record, index, "flush_score")), 1),
			Tier:                    normalizedTier(field(record, index, "tier"), parseFloat(field(record, index, "flush_score"))),
			DropFromPrior30mHighPct: roundTo(parseFloat(field(record, index, "drop_from_prior_30m_high_pct")), 1),
			DistanceBelowVWAPPct:    roundTo(parseFloat(field(record, index, "distance_below_vwap_pct")), 1),
			ROC5mPct:                roundTo(parseFloat(field(record, index, "roc_5m_pct")), 1),
			ROC10mPct:               roundTo(parseFloat(field(record, index, "roc_10m_pct")), 1),
			DownSlope20mPctPerBar:   roundTo(parseFloat(field(record, index, "down_slope_20m_pct_per_bar")), 3),
			RangeExpansion:          roundTo(parseFloat(field(record, index, "range_expansion")), 1),
			VolumeExpansion:         roundTo(parseFloat(field(record, index, "volume_expansion")), 1),
			VolumeSince4AM:          roundTo(parseFloat(field(record, index, "volume_since_4am")), 0),
			Summary:                 strings.TrimSpace(field(record, index, "summary")),
			Signal:                  signalType,
			SignalTime:              alertTime.Format("1504"),
			SignalTimeDisplay:       alertTime.Format("03:04 PM"),
			ScoreBucket:             scoreBucket(parseFloat(field(record, index, "flush_score"))),
			MinutesFromOpen:         minutesFromRegularOpen(alertTime),
			MinutesFromSessionStart: minutesFromSessionStart(alertTime, sessionStartTime),
		}
		if alert.Name == "" {
			alert.Name = symbol
		}
		if alert.Summary == "" {
			alert.Summary = buildSummary(alert)
		}
		alert.OpenChartURL = buildOpenChartURL(chartBaseURL, alert)
		alert.RelativeStrengthLabel = relativeStrengthLabel(alert.FlushScore)
		alert.SetupQuality = setupQuality(alert)
		alerts = append(alerts, alert)
	}

	sort.Slice(alerts, func(i, j int) bool {
		return compareAlertsChronological(alerts[i], alerts[j]) < 0
	})

	return alerts, nil
}

func compareAlertsChronological(a, b Alert) int {
	aMinute := a.AlertTimeET.Truncate(time.Minute)
	bMinute := b.AlertTimeET.Truncate(time.Minute)
	if !aMinute.Equal(bMinute) {
		if aMinute.Before(bMinute) {
			return -1
		}
		return 1
	}
	if a.FlushScore != b.FlushScore {
		if a.FlushScore > b.FlushScore {
			return -1
		}
		return 1
	}
	if !a.AlertTimeET.Equal(b.AlertTimeET) {
		if a.AlertTimeET.Before(b.AlertTimeET) {
			return -1
		}
		return 1
	}
	if a.SourceOrder < b.SourceOrder {
		return -1
	}
	if a.SourceOrder > b.SourceOrder {
		return 1
	}
	return 0
}

func writeSignalCSV(path string, alerts []Alert) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create signal csv: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	header := []string{
		"ticker",
		"time",
		"signal",
		"session_date",
		"alert_time_et",
		"price",
		"flush_score",
		"tier",
		"alert_id",
		"name",
		"sources",
		"drop_from_prior_30m_high_pct",
		"distance_below_vwap_pct",
		"roc_5m_pct",
		"roc_10m_pct",
		"down_slope_20m_pct_per_bar",
		"range_expansion",
		"volume_expansion",
		"volume_since_4am",
		"summary",
		"open_chart_url",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("write signal header: %w", err)
	}

	for _, alert := range alerts {
		row := []string{
			alert.Symbol,
			alert.SignalTime,
			alert.Signal,
			alert.SessionDate,
			alert.AlertTimeText,
			formatFloat(alert.Price, 2),
			formatFloat(alert.FlushScore, 1),
			alert.Tier,
			alert.AlertID,
			alert.Name,
			alert.Sources,
			formatFloat(alert.DropFromPrior30mHighPct, 1),
			formatFloat(alert.DistanceBelowVWAPPct, 1),
			formatFloat(alert.ROC5mPct, 1),
			formatFloat(alert.ROC10mPct, 1),
			formatFloat(alert.DownSlope20mPctPerBar, 3),
			formatFloat(alert.RangeExpansion, 1),
			formatFloat(alert.VolumeExpansion, 1),
			formatFloat(alert.VolumeSince4AM, 0),
			alert.Summary,
			alert.OpenChartURL,
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write signal row: %w", err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush signal csv: %w", err)
	}
	return nil
}

func writeDashboardHTML(path, sourceFile, signalCSVPath, chartBaseURL string, alerts []Alert) error {
	payload, err := json.Marshal(alerts)
	if err != nil {
		return fmt.Errorf("marshal alert payload: %w", err)
	}

	uniqueSymbols, avgScore, minScore, maxScore, topSymbol, _ := summarizeAlerts(alerts)
	model := pageModel{
		Title:         "Flush To Polygon Signal Converter",
		SessionDate:   alerts[0].SessionDate,
		SourceFile:    filepath.Base(sourceFile),
		GeneratedAt:   time.Now().In(nyLocation).Format("2006-01-02 15:04:05 MST"),
		SignalCSVPath: filepath.Base(signalCSVPath),
		ChartBaseURL:  chartBaseURL,
		AlertCount:    len(alerts),
		UniqueSymbols: uniqueSymbols,
		AverageScore:  formatFloat(avgScore, 1),
		MaxScore:      formatFloat(maxScore, 1),
		MinScore:      formatFloat(minScore, 1),
		TopSymbol:     topSymbol,
		AlertsJSON:    template.JS(payload),
	}

	tmpl, err := template.New("dashboard").Parse(dashboardTemplate)
	if err != nil {
		return fmt.Errorf("parse dashboard template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, model); err != nil {
		return fmt.Errorf("render dashboard template: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write dashboard html: %w", err)
	}
	return nil
}

func summarizeAlerts(alerts []Alert) (uniqueSymbols int, avgScore, minScore, maxScore float64, topSymbol string, topCount int) {
	symbolCounts := map[string]int{}
	if len(alerts) == 0 {
		return 0, 0, 0, 0, "", 0
	}
	minScore = alerts[0].FlushScore
	maxScore = alerts[0].FlushScore
	var total float64
	for _, alert := range alerts {
		total += alert.FlushScore
		symbolCounts[alert.Symbol]++
		if alert.FlushScore < minScore {
			minScore = alert.FlushScore
		}
		if alert.FlushScore > maxScore {
			maxScore = alert.FlushScore
		}
	}
	avgScore = total / float64(len(alerts))
	uniqueSymbols = len(symbolCounts)
	for symbol, count := range symbolCounts {
		if count > topCount || (count == topCount && symbol < topSymbol) {
			topSymbol = symbol
			topCount = count
		}
	}
	return uniqueSymbols, roundTo(avgScore, 1), roundTo(minScore, 1), roundTo(maxScore, 1), topSymbol, topCount
}

func buildOpenChartURL(chartBaseURL string, alert Alert) string {
	base := strings.TrimRight(strings.TrimSpace(chartBaseURL), "/")
	if base == "" {
		base = "http://localhost:8081"
	}
	return fmt.Sprintf("%s/api/open-chart/%s/%s/%s?signal=%s", base, alert.Symbol, alert.SessionDate, alert.SignalTime, alert.Signal)
}

func buildSummary(alert Alert) string {
	summary := fmt.Sprintf(
		"%.1f%% below prior 30m high, %.1f%% below VWAP, 5m ROC -%.1f%%, range x%.1f, volume x%.1f",
		alert.DropFromPrior30mHighPct,
		alert.DistanceBelowVWAPPct,
		alert.ROC5mPct,
		alert.RangeExpansion,
		alert.VolumeExpansion,
	)
	if alert.VolumeSince4AM > 0 {
		return summary + ", 04:00 ET vol " + formatWholeNumber(alert.VolumeSince4AM)
	}
	return summary
}

func normalizedTier(raw string, score float64) string {
	value := strings.TrimSpace(raw)
	if value != "" {
		return value
	}
	switch {
	case score <= 39:
		return "Low"
	case score <= 59:
		return "Notable"
	case score <= 74:
		return "Candidate"
	case score <= 89:
		return "Strong"
	default:
		return "Extreme"
	}
}

func scoreBucket(score float64) int {
	switch {
	case score < 40:
		return 0
	case score < 60:
		return 1
	case score < 75:
		return 2
	case score < 90:
		return 3
	default:
		return 4
	}
}

func relativeStrengthLabel(score float64) string {
	switch {
	case score >= 90:
		return "capitulation"
	case score >= 75:
		return "stretched"
	case score >= 60:
		return "actionable"
	default:
		return "monitor"
	}
}

func setupQuality(alert Alert) string {
	var tags []string
	if alert.DropFromPrior30mHighPct >= 4 {
		tags = append(tags, "high-dislocation")
	}
	if alert.DistanceBelowVWAPPct >= 2 {
		tags = append(tags, "under-vwap")
	}
	if alert.RangeExpansion >= 1.5 {
		tags = append(tags, "range-expanding")
	}
	if alert.VolumeExpansion >= 1.5 {
		tags = append(tags, "volume-expanding")
	}
	if len(tags) == 0 {
		return "early"
	}
	return strings.Join(tags, ", ")
}

func minutesFromRegularOpen(t time.Time) int {
	open := time.Date(t.Year(), t.Month(), t.Day(), 9, 30, 0, 0, nyLocation)
	return int(t.Sub(open).Minutes())
}

func minutesFromSessionStart(t time.Time, sessionStartHHMM string) int {
	startHour := 9
	startMinute := 40
	if parsed, err := time.Parse("15:04", strings.TrimSpace(sessionStartHHMM)); err == nil {
		startHour = parsed.Hour()
		startMinute = parsed.Minute()
	}
	start := time.Date(t.Year(), t.Month(), t.Day(), startHour, startMinute, 0, 0, nyLocation)
	return int(t.Sub(start).Minutes())
}

func field(record []string, index map[string]int, name string) string {
	i, ok := index[name]
	if !ok || i >= len(record) {
		return ""
	}
	return record[i]
}

func parseFloat(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return f
}

func formatFloat(value float64, decimals int) string {
	format := "%." + strconv.Itoa(decimals) + "f"
	return fmt.Sprintf(format, roundTo(value, decimals))
}

func formatWholeNumber(value float64) string {
	rounded := int64(math.Round(value))
	negative := rounded < 0
	if negative {
		rounded = -rounded
	}

	raw := strconv.FormatInt(rounded, 10)
	if len(raw) <= 3 {
		if negative {
			return "-" + raw
		}
		return raw
	}

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}

	prefixLen := len(raw) % 3
	if prefixLen == 0 {
		prefixLen = 3
	}
	b.WriteString(raw[:prefixLen])
	for i := prefixLen; i < len(raw); i += 3 {
		b.WriteByte(',')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

func roundTo(value float64, decimals int) float64 {
	pow := math.Pow10(decimals)
	return math.Round(value*pow) / pow
}

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func exitWithError(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

const dashboardTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} - {{.SessionDate}}</title>
  <style>
    :root {
      --bg: #07111b;
      --panel: rgba(12, 27, 41, 0.88);
      --panel-strong: rgba(17, 39, 59, 0.96);
      --border: rgba(145, 175, 197, 0.24);
      --text: #edf5fb;
      --muted: #8ea5b7;
      --accent: #f2c14e;
      --accent-2: #68d391;
      --accent-3: #ff8f5a;
      --candidate: #66c3ff;
      --strong: #ffb347;
      --extreme: #ff6b57;
      --shadow: 0 18px 50px rgba(0,0,0,0.35);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: "Aptos", "Segoe UI", "Helvetica Neue", sans-serif;
      color: var(--text);
      background:
        radial-gradient(circle at top left, rgba(242, 193, 78, 0.14), transparent 32%),
        radial-gradient(circle at top right, rgba(104, 211, 145, 0.12), transparent 28%),
        linear-gradient(180deg, #08131e 0%, #050b12 100%);
    }
    a { color: inherit; text-decoration: none; }
    .shell {
      width: min(1480px, calc(100vw - 32px));
      margin: 22px auto 40px;
    }
    .hero, .panel, .card, .detail-card, .timeline, .table-wrap {
      border: 1px solid var(--border);
      background: var(--panel);
      backdrop-filter: blur(20px);
      box-shadow: var(--shadow);
      border-radius: 22px;
    }
    .hero {
      padding: 24px 26px;
      display: grid;
      gap: 18px;
    }
    .hero-top {
      display: flex;
      justify-content: space-between;
      gap: 18px;
      flex-wrap: wrap;
      align-items: center;
    }
    .title-wrap h1 {
      margin: 0;
      font-size: clamp(2rem, 3.4vw, 3.4rem);
      letter-spacing: -0.05em;
      line-height: 0.94;
    }
    .title-wrap p {
      margin: 10px 0 0;
      color: var(--muted);
      max-width: 800px;
      font-size: 1rem;
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
    }
    .button, button {
      border: 1px solid rgba(255,255,255,0.14);
      background: linear-gradient(180deg, rgba(242,193,78,0.18), rgba(242,193,78,0.1));
      color: var(--text);
      padding: 11px 16px;
      border-radius: 999px;
      cursor: pointer;
      font: inherit;
    }
    .button.secondary, button.secondary {
      background: rgba(255,255,255,0.06);
    }
    button:disabled {
      opacity: 0.45;
      cursor: not-allowed;
    }
    .meta-grid, .stat-grid {
      display: grid;
      gap: 14px;
    }
    .meta-grid { grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); }
    .stat-grid { grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); }
    .meta-chip, .card {
      padding: 14px 16px;
    }
    .meta-chip label, .card label {
      display: block;
      color: var(--muted);
      text-transform: uppercase;
      letter-spacing: 0.08em;
      font-size: 0.72rem;
      margin-bottom: 8px;
    }
    .meta-chip strong, .card strong {
      font-size: 1rem;
      font-weight: 650;
    }
    .stat-value {
      font-size: clamp(1.5rem, 3vw, 2.4rem);
      font-weight: 700;
      letter-spacing: -0.04em;
      margin-top: 4px;
    }
    .controls {
      margin-top: 18px;
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
      gap: 12px;
    }
    .control {
      padding: 12px 14px;
      border-radius: 16px;
      background: rgba(255,255,255,0.045);
      border: 1px solid rgba(255,255,255,0.08);
    }
    .control label {
      display: block;
      font-size: 0.72rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--muted);
      margin-bottom: 8px;
    }
    .control input, .control select {
      width: 100%;
      color: var(--text);
      border: 0;
      outline: 0;
      border-radius: 12px;
      padding: 10px 12px;
      font: inherit;
      background: rgba(5, 12, 19, 0.85);
    }
    .layout {
      margin-top: 20px;
      display: grid;
      gap: 20px;
      grid-template-columns: minmax(0, 1.2fr) minmax(320px, 0.8fr);
      align-items: start;
    }
    .panel {
      padding: 18px;
    }
    .detail-panel {
      position: static;
      top: auto;
      align-self: start;
      height: fit-content;
      will-change: transform;
    }
    .panel-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
      margin-bottom: 12px;
    }
    .panel h2, .timeline h2 {
      margin: 0 0 12px;
      font-size: 1rem;
      letter-spacing: 0.04em;
      text-transform: uppercase;
      color: var(--muted);
    }
    .panel-head h2 {
      margin: 0;
    }
    .panel-head button {
      padding: 9px 14px;
      font-size: 0.92rem;
    }
    .timeline {
      margin-top: 20px;
      padding: 18px;
    }
    .bars {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(18px, 1fr));
      gap: 8px;
      align-items: end;
      min-height: 150px;
    }
    .bar-wrap {
      display: grid;
      gap: 6px;
      align-items: end;
      justify-items: center;
    }
    .bar {
      width: 100%;
      border-radius: 999px;
      background: linear-gradient(180deg, rgba(242,193,78,0.95), rgba(255,107,87,0.92));
      min-height: 6px;
    }
    .bar-label, .bar-count {
      font-size: 0.7rem;
      color: var(--muted);
      writing-mode: vertical-rl;
      transform: rotate(180deg);
    }
    .table-wrap {
      overflow: hidden;
    }
    table {
      width: 100%;
      border-collapse: collapse;
    }
    thead th {
      position: sticky;
      top: 0;
      background: rgba(8, 18, 28, 0.98);
      color: var(--muted);
      text-transform: uppercase;
      letter-spacing: 0.08em;
      font-size: 0.72rem;
      text-align: left;
      padding: 14px 16px;
      border-bottom: 1px solid var(--border);
    }
    tbody tr {
      cursor: pointer;
      transition: background 140ms ease, transform 140ms ease;
    }
    tbody tr:hover {
      background: rgba(255,255,255,0.035);
    }
    tbody tr.active {
      background: linear-gradient(90deg, rgba(242,193,78,0.16), rgba(255,107,87,0.08));
    }
    tbody tr.minute-group {
      cursor: default;
    }
    tbody tr.minute-group:hover {
      background: transparent;
    }
    tbody tr.minute-group td {
      padding: 12px 16px;
      background: rgba(255,255,255,0.025);
      border-bottom: 1px solid rgba(255,255,255,0.04);
    }
    tbody td {
      padding: 14px 16px;
      border-bottom: 1px solid rgba(255,255,255,0.05);
      vertical-align: top;
      font-size: 0.95rem;
    }
    td.row-number {
      width: 1%;
      white-space: nowrap;
      color: var(--muted);
      font-variant-numeric: tabular-nums;
    }
    .minute-group-row {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
    }
    .minute-group-meta {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 10px;
    }
    .minute-label {
      font-size: 0.92rem;
      font-weight: 700;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: var(--text);
    }
    .minute-summary {
      color: var(--muted);
      font-size: 0.82rem;
    }
    .minute-toggle {
      padding: 7px 12px;
      font-size: 0.84rem;
      background: rgba(255,255,255,0.05);
    }
    .symbol {
      font-size: 1.08rem;
      font-weight: 700;
      letter-spacing: 0.02em;
    }
    .subtext {
      display: block;
      margin-top: 4px;
      color: var(--muted);
      font-size: 0.82rem;
    }
    .score-pill, .tier-pill, .tag {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border-radius: 999px;
      padding: 6px 10px;
      font-size: 0.78rem;
      white-space: nowrap;
    }
    .score-pill {
      font-weight: 700;
      background: rgba(255,255,255,0.08);
    }
    .score-0 { color: #a9bbca; }
    .score-1 { color: #77c5ff; }
    .score-2 { color: #66d6c2; }
    .score-3 { color: var(--strong); }
    .score-4 { color: var(--extreme); }
    .tier-pill {
      border: 1px solid rgba(255,255,255,0.12);
      color: var(--text);
      background: rgba(255,255,255,0.04);
    }
    .detail-stack {
      display: grid;
      gap: 14px;
    }
    .detail-card {
      padding: 18px;
      background: var(--panel-strong);
    }
    .detail-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
      margin-bottom: 14px;
    }
    .detail-header h3 {
      margin: 0;
      font-size: clamp(1.3rem, 2vw, 1.8rem);
      letter-spacing: -0.03em;
    }
    .detail-header p {
      margin: 4px 0 0;
      color: var(--muted);
    }
    .metric-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 10px;
    }
    .metric {
      padding: 12px;
      border-radius: 16px;
      background: rgba(255,255,255,0.045);
      border: 1px solid rgba(255,255,255,0.06);
    }
    .metric label {
      display: block;
      color: var(--muted);
      font-size: 0.72rem;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      margin-bottom: 8px;
    }
    .metric strong {
      font-size: 1.15rem;
      letter-spacing: -0.03em;
    }
    .metric-meta {
      margin-top: 8px;
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 8px;
    }
    .metric-score {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border-radius: 999px;
      min-width: 46px;
      padding: 3px 9px;
      font-size: 0.72rem;
      font-weight: 700;
      border: 1px solid rgba(255,255,255,0.18);
      background: rgba(255,255,255,0.08);
    }
    .metric-score-1 { color: #a9bbca; }
    .metric-score-2 { color: #77c5ff; }
    .metric-score-3 { color: #66d6c2; }
    .metric-score-4 { color: var(--strong); }
    .metric-score-5 { color: var(--extreme); }
    .metric-tone {
      color: var(--muted);
      font-size: 0.78rem;
      text-transform: capitalize;
      letter-spacing: 0.02em;
    }
    .tag-row {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin-top: 14px;
    }
    .tag {
      background: rgba(255,255,255,0.07);
      color: var(--muted);
      border: 1px solid rgba(255,255,255,0.08);
    }
    .summary-box {
      margin-top: 16px;
      padding: 14px;
      border-radius: 18px;
      background: rgba(242,193,78,0.08);
      border: 1px solid rgba(242,193,78,0.16);
      line-height: 1.45;
    }
    .empty {
      color: var(--muted);
      text-align: center;
      padding: 26px;
    }
    .footer {
      margin-top: 18px;
      color: var(--muted);
      font-size: 0.86rem;
      text-align: right;
    }
    @media (max-width: 1100px) {
      .layout { grid-template-columns: 1fr; }
    }
    @media (max-width: 700px) {
      .shell { width: min(100vw - 20px, 100%); margin-top: 10px; }
      .hero, .panel, .timeline, .detail-card { border-radius: 18px; }
      .metric-grid { grid-template-columns: 1fr; }
      .bars { grid-template-columns: repeat(auto-fit, minmax(28px, 1fr)); }
      .bar-label, .bar-count { writing-mode: horizontal-tb; transform: none; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="hero-top">
        <div class="title-wrap">
          <h1>Flush To Polygon Signal Converter</h1>
          <p>Single-day flush-detector alerts converted into a polygon-charts upload CSV and a daytrader review board with sortable signal quality, timing, and setup context.</p>
        </div>
        <div class="actions">
          <a class="button" href="{{.SignalCSVPath}}" download>Download Signal CSV</a>
          <button id="openSelected">Open Selected Chart</button>
          <button class="secondary" id="copySelected">Copy Chart Link</button>
        </div>
      </div>
      <div class="meta-grid">
        <div class="meta-chip"><label>Session Date</label><strong>{{.SessionDate}}</strong></div>
        <div class="meta-chip"><label>Alert File</label><strong>{{.SourceFile}}</strong></div>
        <div class="meta-chip"><label>Signal File</label><strong>{{.SignalCSVPath}}</strong></div>
        <div class="meta-chip"><label>Chart Base URL</label><strong>{{.ChartBaseURL}}</strong></div>
      </div>
      <div class="stat-grid">
        <div class="card"><label>Total Alerts</label><div class="stat-value" id="statAlerts">{{.AlertCount}}</div></div>
        <div class="card"><label>Unique Symbols</label><div class="stat-value" id="statSymbols">{{.UniqueSymbols}}</div></div>
        <div class="card"><label>Average Score</label><div class="stat-value" id="statAverage">{{.AverageScore}}</div></div>
        <div class="card"><label>Strongest Score</label><div class="stat-value" id="statMax">{{.MaxScore}}</div></div>
        <div class="card"><label>Weakest Score</label><div class="stat-value" id="statMin">{{.MinScore}}</div></div>
        <div class="card"><label>Most Active Symbol</label><div class="stat-value" id="statTop">{{.TopSymbol}}</div></div>
      </div>
      <div class="controls">
        <div class="control">
          <label for="search">Filter Ticker / Source</label>
          <input id="search" type="text" placeholder="NVDA, biotech, strong">
        </div>
        <div class="control">
          <label for="minScore">Minimum Score</label>
          <input id="minScore" type="range" min="0" max="100" step="5" value="0">
          <div class="subtext"><span id="minScoreValue">0</span>+</div>
        </div>
        <div class="control">
          <label for="sortBy">Sort Alerts</label>
          <select id="sortBy">
            <option value="time-asc">Oldest first</option>
            <option value="time-desc">Newest first</option>
            <option value="score-desc">Score high to low</option>
            <option value="score-asc">Score low to high</option>
            <option value="symbol-asc">Ticker A-Z</option>
          </select>
        </div>
      </div>
    </section>

    <section class="timeline">
      <h2>Alert Pressure Timeline</h2>
      <div class="bars" id="timeline"></div>
    </section>

    <section class="layout" id="layoutSection">
      <div class="panel table-wrap" id="tablePanel">
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>Ticker</th>
              <th>Time</th>
              <th>Score</th>
              <th>Price</th>
              <th>Summary</th>
            </tr>
          </thead>
          <tbody id="tableBody"></tbody>
        </table>
      </div>
      <div class="panel detail-panel" id="detailPanel">
        <div class="panel-head">
          <h2>Selected Setup</h2>
          <button id="openSelectedDetail" type="button">Open Selected Chart</button>
        </div>
        <div class="detail-stack" id="detailPane"></div>
      </div>
    </section>

    <div class="footer">Generated {{.GeneratedAt}}. For polygon-charts upload, set the trade date to {{.SessionDate}} before importing the CSV.</div>
  </div>

  <script id="payload" type="application/json">{{.AlertsJSON}}</script>
  <script>
    const alerts = JSON.parse(document.getElementById('payload').textContent);
    let filtered = [];
    let selected = null;

    const dom = {
      search: document.getElementById('search'),
      minScore: document.getElementById('minScore'),
      minScoreValue: document.getElementById('minScoreValue'),
      sortBy: document.getElementById('sortBy'),
      layoutSection: document.getElementById('layoutSection'),
      tablePanel: document.getElementById('tablePanel'),
      detailPanel: document.getElementById('detailPanel'),
      tableBody: document.getElementById('tableBody'),
      detailPane: document.getElementById('detailPane'),
      timeline: document.getElementById('timeline'),
      openSelected: document.getElementById('openSelected'),
      openSelectedDetail: document.getElementById('openSelectedDetail'),
      copySelected: document.getElementById('copySelected'),
      statAlerts: document.getElementById('statAlerts'),
      statSymbols: document.getElementById('statSymbols'),
      statAverage: document.getElementById('statAverage'),
      statMax: document.getElementById('statMax'),
      statMin: document.getElementById('statMin'),
      statTop: document.getElementById('statTop'),
    };
    const expandedMinutes = new Set();

    function scoreClass(score) {
      if (score < 40) return 'score-0';
      if (score < 60) return 'score-1';
      if (score < 75) return 'score-2';
      if (score < 90) return 'score-3';
      return 'score-4';
    }

    function metricValue(value) {
      const num = Number(value);
      if (!Number.isFinite(num)) return 0;
      return Math.max(0, num);
    }

    function rateDropFromPrior30mHigh(value) {
      if (value < 1.0) return { score: 1, label: 'noise' };
      if (value < 2.0) return { score: 2, label: 'pullback' };
      if (value < 3.0) return { score: 3, label: 'flush' };
      if (value <= 4.0) return { score: 4, label: 'hard flush' };
      return { score: 5, label: 'washout' };
    }

    function rateDistanceBelowVWAP(value) {
      if (value < 0.5) return { score: 1, label: 'near value' };
      if (value < 1.0) return { score: 2, label: 'weak' };
      if (value < 2.0) return { score: 3, label: 'stretched' };
      if (value <= 3.0) return { score: 4, label: 'deeply stretched' };
      return { score: 5, label: 'dislocated' };
    }

    function rateRoc5m(value) {
      if (value < 0.3) return { score: 1, label: 'slow' };
      if (value < 0.7) return { score: 2, label: 'selling' };
      if (value < 1.2) return { score: 3, label: 'aggressive selling' };
      if (value <= 1.8) return { score: 4, label: 'flush impulse' };
      return { score: 5, label: 'panic burst' };
    }

    function rateRoc10m(value) {
      if (value < 0.5) return { score: 1, label: 'drift' };
      if (value < 1.0) return { score: 2, label: 'sustained weakness' };
      if (value < 2.0) return { score: 3, label: 'strong pressure' };
      if (value <= 3.0) return { score: 4, label: 'trend flush' };
      return { score: 5, label: 'one-sided pressure' };
    }

    function rateDownSlope20m(value) {
      if (value < 0.03) return { score: 1, label: 'drift' };
      if (value < 0.07) return { score: 2, label: 'controlled bleed' };
      if (value < 0.12) return { score: 3, label: 'trend pressure' };
      if (value <= 0.18) return { score: 4, label: 'heavy pressure' };
      return { score: 5, label: 'relentless trend' };
    }

    function rateRangeExpansion(value) {
      if (value <= 1.0) return { score: 1, label: 'normal' };
      if (value <= 1.3) return { score: 2, label: 'building' };
      if (value <= 1.6) return { score: 3, label: 'expanding' };
      if (value <= 2.0) return { score: 4, label: 'emotional' };
      return { score: 5, label: 'washout-like' };
    }

    function rateVolumeExpansion(value) {
      if (value <= 1.0) return { score: 1, label: 'routine' };
      if (value <= 1.5) return { score: 2, label: 'active' };
      if (value <= 2.0) return { score: 3, label: 'crowded' };
      if (value <= 3.0) return { score: 4, label: 'forced' };
      return { score: 5, label: 'capitulation-like' };
    }

    function metricRatings(alert) {
      return {
        dropFromPrior30mHigh: rateDropFromPrior30mHigh(metricValue(alert.drop_from_prior_30m_high_pct)),
        distanceBelowVWAP: rateDistanceBelowVWAP(metricValue(alert.distance_below_vwap_pct)),
        roc5m: rateRoc5m(metricValue(alert.roc_5m_pct)),
        roc10m: rateRoc10m(metricValue(alert.roc_10m_pct)),
        downSlope20m: rateDownSlope20m(metricValue(alert.down_slope_20m_pct_per_bar)),
        rangeExpansion: rateRangeExpansion(metricValue(alert.range_expansion)),
        volumeExpansion: rateVolumeExpansion(metricValue(alert.volume_expansion)),
      };
    }

    function renderRatedMetric(label, valueText, rating) {
      return [
        '<div class="metric">',
        '<label>' + escapeHTML(label) + '</label>',
        '<strong>' + escapeHTML(valueText) + '</strong>',
        '<div class="metric-meta">',
        '<span class="metric-score metric-score-' + rating.score + '">' + rating.score + '/5</span>',
        '<span class="metric-tone">' + escapeHTML(rating.label) + '</span>',
        '</div>',
        '</div>',
      ].join('');
    }

    function escapeHTML(value) {
      return String(value ?? '')
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#39;');
    }

    function formatWholeNumber(value) {
      const num = Number(value ?? 0);
      if (!Number.isFinite(num)) return '0';
      return Math.round(num).toLocaleString('en-US');
    }

    function alertMinuteKey(alert) {
      return String(alert.alert_time_et || '').slice(0, 16);
    }

    function compareChronological(a, b) {
      const minuteCompare = alertMinuteKey(a).localeCompare(alertMinuteKey(b));
      if (minuteCompare !== 0) {
        return a.alert_time_et.localeCompare(b.alert_time_et) || a.source_order - b.source_order;
      }
      return b.flush_score - a.flush_score ||
        a.alert_time_et.localeCompare(b.alert_time_et) ||
        a.source_order - b.source_order;
    }

    function compareReverseChronological(a, b) {
      const minuteCompare = alertMinuteKey(b).localeCompare(alertMinuteKey(a));
      if (minuteCompare !== 0) {
        return b.alert_time_et.localeCompare(a.alert_time_et) || a.source_order - b.source_order;
      }
      return b.flush_score - a.flush_score ||
        b.alert_time_et.localeCompare(a.alert_time_et) ||
        a.source_order - b.source_order;
    }

    function compareBySort(a, b, sortBy) {
      switch (sortBy) {
        case 'time-asc':
          return compareChronological(a, b);
        case 'score-desc':
          return b.flush_score - a.flush_score ||
            a.alert_time_et.localeCompare(b.alert_time_et) ||
            a.source_order - b.source_order;
        case 'score-asc':
          return a.flush_score - b.flush_score ||
            a.alert_time_et.localeCompare(b.alert_time_et) ||
            a.source_order - b.source_order;
        case 'symbol-asc':
          return a.symbol.localeCompare(b.symbol) ||
            b.flush_score - a.flush_score ||
            a.alert_time_et.localeCompare(b.alert_time_et) ||
            a.source_order - b.source_order;
        case 'time-desc':
        default:
          return compareReverseChronological(a, b);
      }
    }

    function groupAlertsByMinute(rows) {
      const groups = [];
      const seen = new Map();
      rows.forEach(alert => {
        const key = alertMinuteKey(alert);
        let group = seen.get(key);
        if (!group) {
          group = {
            key,
            minuteLabel: String(alert.alert_time_et || '').slice(11, 16) || '--:--',
            alerts: [],
          };
          seen.set(key, group);
          groups.push(group);
        }
        group.alerts.push(alert);
      });
      return groups;
    }

    function syncSelectionButtons() {
      const disabled = !selected;
      dom.openSelected.disabled = disabled;
      dom.openSelectedDetail.disabled = disabled;
      dom.copySelected.disabled = disabled;
    }

    function openSelectedChart() {
      if (!selected) return;
      window.open(selected.open_chart_url, '_blank', 'noopener,noreferrer');
    }

    function syncDetailPanelPosition() {
      const layout = dom.layoutSection;
      const tablePanel = dom.tablePanel;
      const detailPanel = dom.detailPanel;
      if (!layout || !tablePanel || !detailPanel) return;

      const layoutRect = layout.getBoundingClientRect();
      const passedTop = Math.max(0, -layoutRect.top);
      const tableScroll = Math.max(0, tablePanel.scrollTop || 0);
      const desiredOffset = Math.max(passedTop, tableScroll);
      const maxOffset = Math.max(0, tablePanel.scrollHeight - detailPanel.offsetHeight - 24);
      const offset = Math.min(desiredOffset, maxOffset);
      detailPanel.style.transform = offset > 0 ? 'translateY(' + offset + 'px)' : 'translateY(0)';
    }

    function applyFilters() {
      const query = dom.search.value.trim().toLowerCase();
      const minScore = Number(dom.minScore.value);
      filtered = alerts.filter(alert => {
        if (alert.flush_score < minScore) return false;
        if (!query) return true;
        return [
          alert.symbol,
          alert.name,
          alert.sources,
          alert.tier,
          alert.summary,
          alert.relative_strength_label,
          alert.setup_quality,
        ].some(value => String(value || '').toLowerCase().includes(query));
      });

      const sortBy = dom.sortBy.value;
      filtered.sort((a, b) => compareBySort(a, b, sortBy));

      if (!selected || !filtered.some(alert => alert.alert_id === selected.alert_id)) {
        selected = filtered[0] || null;
      }

      updateStats(filtered);
      renderTable();
      renderDetail();
      renderTimeline(filtered);
      syncDetailPanelPosition();
    }

    function updateStats(rows) {
      dom.minScoreValue.textContent = dom.minScore.value;
      dom.statAlerts.textContent = rows.length;
      const uniqueSymbols = new Set(rows.map(row => row.symbol));
      dom.statSymbols.textContent = uniqueSymbols.size;
      if (!rows.length) {
        dom.statAverage.textContent = '0.0';
        dom.statMax.textContent = '0.0';
        dom.statMin.textContent = '0.0';
        dom.statTop.textContent = 'n/a';
        return;
      }

      const scores = rows.map(row => row.flush_score);
      const avg = scores.reduce((sum, score) => sum + score, 0) / rows.length;
      const counts = rows.reduce((map, row) => {
        map[row.symbol] = (map[row.symbol] || 0) + 1;
        return map;
      }, {});
      const top = Object.entries(counts).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))[0];
      dom.statAverage.textContent = avg.toFixed(1);
      dom.statMax.textContent = Math.max(...scores).toFixed(1);
      dom.statMin.textContent = Math.min(...scores).toFixed(1);
      dom.statTop.textContent = top ? top[0] : 'n/a';
    }

    function renderTable() {
      if (!filtered.length) {
        dom.tableBody.innerHTML = '<tr><td class="empty" colspan="6">No alerts match the current filters.</td></tr>';
        syncSelectionButtons();
        return;
      }

      const groups = groupAlertsByMinute(filtered);
      const rowNumberById = new Map(filtered.map((alert, index) => [alert.alert_id, index + 1]));

      dom.tableBody.innerHTML = groups.map(group => {
        const hiddenCount = Math.max(group.alerts.length - 2, 0);
        const expanded = expandedMinutes.has(group.key);
        const visibleAlerts = expanded ? group.alerts : group.alerts.slice(0, 2);
        const header = [
          '<tr class="minute-group">',
          '<td colspan="6">',
          '<div class="minute-group-row">',
          '<div class="minute-group-meta">',
          '<span class="minute-label">' + escapeHTML(group.minuteLabel) + '</span>',
          '<span class="minute-summary">' + group.alerts.length + ' alerts this minute' + (hiddenCount ? ' · ' + hiddenCount + ' hidden' : '') + '</span>',
          '</div>',
          hiddenCount ? '<button class="minute-toggle" type="button" data-minute-key="' + escapeHTML(group.key) + '">' + (expanded ? 'Hide extras' : 'Show ' + hiddenCount + ' more') + '</button>' : '',
          '</div>',
          '</td>',
          '</tr>',
        ].join('');

        const rows = visibleAlerts.map(alert => {
          const active = selected && alert.alert_id === selected.alert_id ? 'active' : '';
          const timeOnly = alert.alert_time_et.slice(11);
          const rowNumber = rowNumberById.get(alert.alert_id) || '';
          return [
            '<tr class="' + active + '" data-alert-id="' + escapeHTML(alert.alert_id) + '">',
            '<td class="row-number">' + rowNumber + '</td>',
            '<td>',
            '<span class="symbol">' + escapeHTML(alert.symbol) + '</span>',
            '<span class="subtext">' + escapeHTML(alert.sources || 'single source') + '</span>',
            '</td>',
            '<td>',
            escapeHTML(timeOnly),
            '<span class="subtext">' + escapeHTML(alert.signal_time_display) + '</span>',
            '</td>',
            '<td>',
            '<span class="score-pill ' + scoreClass(alert.flush_score) + '">' + alert.flush_score.toFixed(1) + '</span>',
            '<span class="subtext"><span class="tier-pill">' + escapeHTML(alert.tier) + '</span></span>',
            '</td>',
            '<td>',
            Number(alert.price).toFixed(2),
            '<span class="subtext">' + escapeHTML(alert.relative_strength_label) + ' · 04:00 vol ' + escapeHTML(formatWholeNumber(alert.volume_since_4am)) + '</span>',
            '</td>',
            '<td>' + escapeHTML(alert.summary) + '</td>',
            '</tr>'
          ].join('');
        }).join('');

        return header + rows;
      }).join('');

      dom.tableBody.querySelectorAll('button[data-minute-key]').forEach(button => {
        button.addEventListener('click', event => {
          event.stopPropagation();
          const minuteKey = button.getAttribute('data-minute-key');
          if (!minuteKey) return;
          if (expandedMinutes.has(minuteKey)) {
            expandedMinutes.delete(minuteKey);
          } else {
            expandedMinutes.add(minuteKey);
          }
          renderTable();
        });
      });

      dom.tableBody.querySelectorAll('tr[data-alert-id]').forEach(row => {
        row.addEventListener('click', () => {
          const alertId = row.getAttribute('data-alert-id');
          selected = filtered.find(alert => alert.alert_id === alertId) || selected;
          renderTable();
          renderDetail();
        });
      });

      syncSelectionButtons();
    }

    function renderDetail() {
      if (!selected) {
        dom.detailPane.innerHTML = '<div class="detail-card empty">Select an alert to inspect the setup breakdown.</div>';
        syncSelectionButtons();
        return;
      }

      const ratings = metricRatings(selected);
      const metricCards = [
        renderRatedMetric('Drop From Prior 30m High', selected.drop_from_prior_30m_high_pct.toFixed(1) + '%', ratings.dropFromPrior30mHigh),
        renderRatedMetric('Distance Below VWAP', selected.distance_below_vwap_pct.toFixed(1) + '%', ratings.distanceBelowVWAP),
        renderRatedMetric('5m Downside ROC', selected.roc_5m_pct.toFixed(1) + '%', ratings.roc5m),
        renderRatedMetric('10m Downside ROC', selected.roc_10m_pct.toFixed(1) + '%', ratings.roc10m),
        renderRatedMetric('20m Downside Slope', selected.down_slope_20m_pct_per_bar.toFixed(3) + '% / bar', ratings.downSlope20m),
        renderRatedMetric('Range Expansion', 'x' + selected.range_expansion.toFixed(1), ratings.rangeExpansion),
        renderRatedMetric('Volume Expansion', 'x' + selected.volume_expansion.toFixed(1), ratings.volumeExpansion),
        [
          '<div class="metric">',
          '<label>Volume Since 04:00</label>',
          '<strong>' + formatWholeNumber(selected.volume_since_4am) + '</strong>',
          '</div>',
        ].join(''),
        [
          '<div class="metric">',
          '<label>Minutes From 09:30</label>',
          '<strong>' + selected.minutes_from_open + '</strong>',
          '</div>',
        ].join(''),
      ].join('');

      dom.detailPane.innerHTML = [
        '<div class="detail-card">',
        '<div class="detail-header">',
        '<div>',
        '<h3>' + escapeHTML(selected.symbol) + ' <span class="tier-pill">' + escapeHTML(selected.tier) + '</span></h3>',
        '<p>' + escapeHTML(selected.name) + ' · ' + escapeHTML(selected.alert_time_et) + ' ET · $' + Number(selected.price).toFixed(2) + '</p>',
        '</div>',
        '<div class="score-pill ' + scoreClass(selected.flush_score) + '">' + selected.flush_score.toFixed(1) + '</div>',
        '</div>',
        '<div class="metric-grid">',
        metricCards,
        '</div>',
        '<div class="tag-row">',
        '<span class="tag">' + escapeHTML(selected.signal.toUpperCase()) + ' signal</span>',
        '<span class="tag">' + escapeHTML(selected.sources || 'single source') + '</span>',
        '<span class="tag">' + escapeHTML(selected.relative_strength_label) + '</span>',
        '<span class="tag">' + escapeHTML(selected.setup_quality) + '</span>',
        '</div>',
        '<div class="summary-box">' + escapeHTML(selected.summary) + '</div>',
        '</div>'
      ].join('');
      syncSelectionButtons();
    }

    function renderTimeline(rows) {
      const buckets = rows.reduce((map, row) => {
        const minute = row.alert_time_et.slice(11, 16);
        map[minute] = (map[minute] || 0) + 1;
        return map;
      }, {});

      const entries = Object.entries(buckets).sort((a, b) => a[0].localeCompare(b[0]));
      if (!entries.length) {
        dom.timeline.innerHTML = '<div class="empty">No alerts match the current filters.</div>';
        return;
      }

      const max = Math.max(...entries.map(([, count]) => count));
      dom.timeline.innerHTML = entries.map(([minute, count]) => {
        const height = Math.max(8, Math.round((count / max) * 120));
        return [
          '<div class="bar-wrap">',
          '<div class="bar-count">' + count + '</div>',
          '<div class="bar" style="height:' + height + 'px"></div>',
          '<div class="bar-label">' + escapeHTML(minute) + '</div>',
          '</div>'
        ].join('');
      }).join('');
    }

    dom.search.addEventListener('input', applyFilters);
    dom.minScore.addEventListener('input', applyFilters);
    dom.sortBy.addEventListener('change', applyFilters);
    window.addEventListener('scroll', syncDetailPanelPosition, { passive: true });
    window.addEventListener('resize', syncDetailPanelPosition);
    dom.tablePanel.addEventListener('scroll', syncDetailPanelPosition, { passive: true });

    dom.openSelected.addEventListener('click', openSelectedChart);
    dom.openSelectedDetail.addEventListener('click', openSelectedChart);

    dom.copySelected.addEventListener('click', async () => {
      if (!selected) return;
      try {
        await navigator.clipboard.writeText(selected.open_chart_url);
        dom.copySelected.textContent = 'Chart Link Copied';
        setTimeout(() => { dom.copySelected.textContent = 'Copy Chart Link'; }, 1200);
      } catch (err) {
        console.error('copy failed', err);
      }
    });

    applyFilters();
  </script>
</body>
</html>
`
