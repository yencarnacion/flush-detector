package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ServerPort  int               `yaml:"server_port" json:"server_port"`
	Alert       AlertConfig       `yaml:"alert" json:"alert"`
	Timezone    string            `yaml:"timezone" json:"timezone"`
	Persistence PersistenceConfig `yaml:"persistence" json:"persistence"`
	UI          UIConfig          `yaml:"ui" json:"ui"`
	Flush       FlushConfig       `yaml:"flush" json:"flush"`
	Gapper      GapperConfig      `yaml:"gapper" json:"gapper"`
	News        NewsConfig        `yaml:"news" json:"news"`
	Filings     FilingsConfig     `yaml:"filings" json:"filings"`
	Logging     LoggingConfig     `yaml:"logging" json:"logging"`
}

type AlertConfig struct {
	SoundFile       string `yaml:"sound_file" json:"sound_file"`
	UpSoundFile     string `yaml:"up_sound_file" json:"up_sound_file"`
	DownSoundFile   string `yaml:"down_sound_file" json:"down_sound_file"`
	FlushSoundFile  string `yaml:"flush_sound_file" json:"flush_sound_file"`
	EnableSound     bool   `yaml:"enable_sound" json:"enable_sound"`
	CooldownSeconds int    `yaml:"cooldown_seconds" json:"cooldown_seconds"`
}

type PersistenceConfig struct {
	StateFile string `yaml:"state_file" json:"state_file"`
}

type UIConfig struct {
	ChartOpenerBaseURL string `yaml:"chart_opener_base_url" json:"chart_opener_base_url"`
}

type FlushConfig struct {
	Enabled                   bool    `yaml:"enabled" json:"enabled"`
	Session                   string  `yaml:"session" json:"session"`
	StartTime                 string  `yaml:"start_time" json:"start_time"`
	EndTime                   string  `yaml:"end_time" json:"end_time"`
	MinVolumeSince4AM         float64 `yaml:"min_volume_since_4am" json:"min_volume_since_4am"`
	MinBarsBeforeAlerts       int     `yaml:"min_bars_before_alerts" json:"min_bars_before_alerts"`
	MinAlertScore             float64 `yaml:"min_alert_score" json:"min_alert_score"`
	BackfillBars              int     `yaml:"backfill_bars" json:"backfill_bars"`
	WarmupLookbackBars        int     `yaml:"warmup_lookback_bars" json:"warmup_lookback_bars"`
	RequireBelowVWAP          bool    `yaml:"require_below_vwap" json:"require_below_vwap"`
	RequireDropFromRecentHigh bool    `yaml:"require_drop_from_recent_high" json:"require_drop_from_recent_high"`
	MaxAlertsPerSymbolPerDay  int     `yaml:"max_alerts_per_symbol_per_day" json:"max_alerts_per_symbol_per_day"`
}

type GapperConfig struct {
	Enabled      bool    `yaml:"enabled" json:"enabled"`
	GapPercent   float64 `yaml:"gap_percent" json:"gap_percent"`
	LookbackDays int     `yaml:"lookback_days" json:"lookback_days"`
}

type NewsConfig struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`
	MaxItems        int  `yaml:"max_items" json:"max_items"`
	LookbackDays    int  `yaml:"lookback_days" json:"lookback_days"`
	CacheTTLSeconds int  `yaml:"cache_ttl_seconds" json:"cache_ttl_seconds"`
}

type FilingsConfig struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`
	MaxItems        int  `yaml:"max_items" json:"max_items"`
	LookbackDays    int  `yaml:"lookback_days" json:"lookback_days"`
	CacheTTLSeconds int  `yaml:"cache_ttl_seconds" json:"cache_ttl_seconds"`
}

type LoggingConfig struct {
	Level string `yaml:"level" json:"level"`
}

func Default() Config {
	return Config{
		ServerPort: 8087,
		Alert: AlertConfig{
			SoundFile:       "./web/sounds/flush.wav",
			UpSoundFile:     "./web/sounds/alert_up.wav",
			DownSoundFile:   "./web/sounds/alert_down.wav",
			FlushSoundFile:  "./web/sounds/flush.wav",
			EnableSound:     true,
			CooldownSeconds: 10,
		},
		Timezone: "America/New_York",
		Persistence: PersistenceConfig{
			StateFile: "state.json",
		},
		UI: UIConfig{
			ChartOpenerBaseURL: "http://localhost:8081",
		},
		Flush: FlushConfig{
			Enabled:                   true,
			Session:                   "rth",
			StartTime:                 "09:40",
			EndTime:                   "15:30",
			MinVolumeSince4AM:         500_000,
			MinBarsBeforeAlerts:       10,
			MinAlertScore:             60,
			BackfillBars:              60,
			WarmupLookbackBars:        30,
			RequireBelowVWAP:          true,
			RequireDropFromRecentHigh: true,
			MaxAlertsPerSymbolPerDay:  3,
		},
		Gapper: GapperConfig{
			Enabled:      true,
			GapPercent:   4,
			LookbackDays: 7,
		},
		News: NewsConfig{
			Enabled:         true,
			MaxItems:        5,
			LookbackDays:    2,
			CacheTTLSeconds: 300,
		},
		Filings: FilingsConfig{
			Enabled:         true,
			MaxItems:        5,
			LookbackDays:    5,
			CacheTTLSeconds: 300,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) Normalize() {
	def := Default()
	if c.ServerPort == 0 {
		c.ServerPort = def.ServerPort
	}
	if strings.TrimSpace(c.Timezone) == "" {
		c.Timezone = def.Timezone
	}
	if strings.TrimSpace(c.Persistence.StateFile) == "" {
		c.Persistence.StateFile = def.Persistence.StateFile
	}
	if strings.TrimSpace(c.UI.ChartOpenerBaseURL) == "" {
		c.UI.ChartOpenerBaseURL = def.UI.ChartOpenerBaseURL
	}
	if strings.TrimSpace(c.Alert.SoundFile) == "" {
		c.Alert.SoundFile = def.Alert.SoundFile
	}
	if strings.TrimSpace(c.Alert.UpSoundFile) == "" {
		c.Alert.UpSoundFile = def.Alert.UpSoundFile
	}
	if strings.TrimSpace(c.Alert.DownSoundFile) == "" {
		c.Alert.DownSoundFile = def.Alert.DownSoundFile
	}
	if strings.TrimSpace(c.Alert.FlushSoundFile) == "" {
		c.Alert.FlushSoundFile = def.Alert.FlushSoundFile
	}
	if c.Alert.CooldownSeconds <= 0 {
		c.Alert.CooldownSeconds = def.Alert.CooldownSeconds
	}
	if strings.TrimSpace(c.Flush.Session) == "" {
		c.Flush.Session = def.Flush.Session
	}
	if strings.TrimSpace(c.Flush.StartTime) == "" {
		c.Flush.StartTime = def.Flush.StartTime
	}
	if strings.TrimSpace(c.Flush.EndTime) == "" {
		c.Flush.EndTime = def.Flush.EndTime
	}
	if c.Flush.MinVolumeSince4AM <= 0 {
		c.Flush.MinVolumeSince4AM = def.Flush.MinVolumeSince4AM
	}
	if c.Flush.MinBarsBeforeAlerts <= 0 {
		c.Flush.MinBarsBeforeAlerts = def.Flush.MinBarsBeforeAlerts
	}
	if c.Flush.MinAlertScore <= 0 {
		c.Flush.MinAlertScore = def.Flush.MinAlertScore
	}
	if c.Flush.BackfillBars <= 0 {
		c.Flush.BackfillBars = def.Flush.BackfillBars
	}
	if c.Flush.WarmupLookbackBars <= 0 {
		c.Flush.WarmupLookbackBars = def.Flush.WarmupLookbackBars
	}
	if c.Flush.MaxAlertsPerSymbolPerDay <= 0 {
		c.Flush.MaxAlertsPerSymbolPerDay = def.Flush.MaxAlertsPerSymbolPerDay
	}
	if c.Gapper.LookbackDays <= 0 {
		c.Gapper.LookbackDays = def.Gapper.LookbackDays
	}
	if c.News.MaxItems <= 0 {
		c.News.MaxItems = def.News.MaxItems
	}
	if c.News.LookbackDays <= 0 {
		c.News.LookbackDays = def.News.LookbackDays
	}
	if c.News.CacheTTLSeconds <= 0 {
		c.News.CacheTTLSeconds = def.News.CacheTTLSeconds
	}
	if c.Filings.MaxItems <= 0 {
		c.Filings.MaxItems = def.Filings.MaxItems
	}
	if c.Filings.LookbackDays <= 0 {
		c.Filings.LookbackDays = def.Filings.LookbackDays
	}
	if c.Filings.CacheTTLSeconds <= 0 {
		c.Filings.CacheTTLSeconds = def.Filings.CacheTTLSeconds
	}
	if strings.TrimSpace(c.Logging.Level) == "" {
		c.Logging.Level = def.Logging.Level
	}
}

func (c Config) Validate() error {
	if _, err := time.Parse("15:04", c.Flush.StartTime); err != nil {
		return fmt.Errorf("flush.start_time: %w", err)
	}
	if _, err := time.Parse("15:04", c.Flush.EndTime); err != nil {
		return fmt.Errorf("flush.end_time: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(c.Flush.Session)) {
	case "pre", "rth", "pm":
	default:
		return fmt.Errorf("flush.session must be one of pre|rth|pm")
	}
	if c.ServerPort <= 0 {
		return fmt.Errorf("server_port must be > 0")
	}
	if c.Gapper.LookbackDays < 1 {
		return fmt.Errorf("gapper.lookback_days must be >= 1")
	}
	return nil
}

func APIKeyFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MASSIVE_API_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("POLYGON_API_KEY"))
}

func MustLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		name = "America/New_York"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		fallback, _ := time.LoadLocation("America/New_York")
		return fallback
	}
	return loc
}
