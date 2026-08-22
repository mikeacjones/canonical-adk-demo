package travelagent

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.temporal.io/sdk/client"
)

type Config struct {
	TaskQueue         string
	TemporalAddress   string
	TemporalNamespace string
	TemporalAPIKey    string
	TemporalTLSCert   string
	TemporalTLSKey    string
	DBURL             string
	ADKModel          string
	GoogleAPIKey      string
	GeminiAPIBase     string
	ResearchSearches  int
	WebSearchFailRate float64
	CheckoutFailHotel bool
	CheckoutStepDelay time.Duration
	ToolDelay         time.Duration
}

func LoadConfig() Config {
	// The runbook starts the worker from go/, so ../.env is the shared config.
	// A local .env is loaded second as a convenient SDK-specific override.
	_ = godotenv.Load("../.env")
	_ = godotenv.Overload(".env")

	return Config{
		TaskQueue:         firstEnv("TEMPORAL_TASK_QUEUE", "TASK_QUEUE", "travel-agent"),
		TemporalAddress:   envOr("TEMPORAL_ADDRESS", "localhost:7233"),
		TemporalNamespace: envOr("TEMPORAL_NAMESPACE", "default"),
		TemporalAPIKey:    os.Getenv("TEMPORAL_API_KEY"),
		TemporalTLSCert:   os.Getenv("TEMPORAL_TLS_CERT"),
		TemporalTLSKey:    os.Getenv("TEMPORAL_TLS_KEY"),
		DBURL:             databaseURL(),
		ADKModel:          envOr("ADK_MODEL", "gemini-3.6-flash"),
		GoogleAPIKey:      firstEnv("GOOGLE_API_KEY", "GEMINI_API_KEY", ""),
		GeminiAPIBase:     envOr("GEMINI_API_BASE", "https://generativelanguage.googleapis.com/v1beta"),
		ResearchSearches:  envInt("RESEARCH_SEARCHES", 6),
		WebSearchFailRate: envFloat("WEB_SEARCH_FAIL_RATE", 0.4),
		CheckoutFailHotel: envBool("CHECKOUT_FAIL_HOTEL", true),
		CheckoutStepDelay: durationSeconds("CHECKOUT_STEP_DELAY_SECONDS", 1.0),
		ToolDelay:         durationSeconds("TOOL_DELAY_SECONDS", 1.0),
	}
}

func DialTemporal(cfg Config) (client.Client, error) {
	opts := client.Options{
		HostPort:  cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	}
	if cfg.TemporalAPIKey != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(cfg.TemporalAPIKey)
		opts.ConnectionOptions.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	} else if cfg.TemporalTLSCert != "" && cfg.TemporalTLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TemporalTLSCert, cfg.TemporalTLSKey)
		if err != nil {
			return nil, fmt.Errorf("load Temporal mTLS certificate: %w", err)
		}
		opts.ConnectionOptions.TLS = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
			RootCAs:      x509.NewCertPool(),
		}
	}
	return client.Dial(opts)
}

func firstEnv(names ...string) string {
	for _, name := range names[:len(names)-1] {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return names[len(names)-1]
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func envFloat(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func durationSeconds(name string, fallback float64) time.Duration {
	seconds := envFloat(name, fallback)
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func databaseURL() string {
	if value := os.Getenv("DB_URL"); value != "" {
		return value
	}
	if host := os.Getenv("DB_HOST"); host != "" {
		return fmt.Sprintf(
			"postgresql://%s:%s@%s:%s/%s",
			envOr("DB_USER", "demo"),
			envOr("DB_PASSWORD", "demo"),
			host,
			envOr("DB_PORT", "5432"),
			envOr("DB_NAME", "travel"),
		)
	}
	return "postgresql://demo:demo@localhost:5432/travel"
}
