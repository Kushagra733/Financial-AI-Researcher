// Package config loads application configuration from a .env file and
// environment variables (env vars take precedence over the file).
//
// Usage:
//
//	cfg, err := config.Load(".env")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(cfg.OpenAIKey)
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for Sentinel-Go.
// Add new fields here as the project grows; Load() will pick them up
// from the .env file automatically once you add the os.Getenv call.
type Config struct {
	// LLM
	OpenAIKey    string
	AnthropicKey string
	GeminiKey    string
	LLMProvider  string // "openai" | "anthropic" | "gemini"

	// UPI / PSP
	UPIClientID     string
	UPIClientSecret string
	UPIMerchantID   string

	// Database
	RedisURL    string
	PostgresURL string

	// Safety
	TransactionThresholdINR float64

	// App
	MaxWorkers int
	LogLevel   string
}

// Load reads key=value pairs from envFile, sets them as environment variables
// (existing env vars are NOT overwritten), then builds and returns a Config.
//
// Passing an empty string or a path that doesn't exist is not an error —
// the function falls back to whatever is already in the environment.
func Load(envFile string) (*Config, error) {
	if err := loadDotEnv(envFile); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg := &Config{
		OpenAIKey:    os.Getenv("OPENAI_API_KEY"),
		AnthropicKey: os.Getenv("ANTHROPIC_API_KEY"),
		GeminiKey:    os.Getenv("GEMINI_API_KEY"),
		LLMProvider:  getEnvOr("LLM_PROVIDER", "gemini"),

		UPIClientID:     os.Getenv("UPI_CLIENT_ID"),
		UPIClientSecret: os.Getenv("UPI_CLIENT_SECRET"),
		UPIMerchantID:   os.Getenv("UPI_MERCHANT_ID"),

		RedisURL:    getEnvOr("REDIS_URL", "redis://localhost:6379"),
		PostgresURL: os.Getenv("POSTGRES_URL"),

		TransactionThresholdINR: getEnvFloat("TRANSACTION_THRESHOLD_INR", 500.0),
		MaxWorkers:              getEnvInt("MAX_WORKERS", 4),
		LogLevel:                getEnvOr("LOG_LEVEL", "info"),
	}

	return cfg, nil
}

// Validate returns an error if any required field is missing.
// Call this after Load() when you want a hard failure at startup rather than
// a confusing error deep inside an agent.
func (c *Config) Validate() error {
	required := map[string]string{
		"OPENAI_API_KEY, ANTHROPIC_API_KEY, or GEMINI_API_KEY": c.OpenAIKey + c.AnthropicKey + c.GeminiKey,
	}
	for name, val := range required {
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("config: %s is required but not set", name)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// loadDotEnv parses a .env file and calls os.Setenv for each key that is not
// already present in the environment. Lines starting with # and blank lines
// are ignored. Malformed lines return an error.
func loadDotEnv(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil // .env is optional
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("line %d: expected KEY=VALUE, got %q", lineNum, line)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Strip inline comments (e.g. VALUE  # comment)
		if idx := strings.Index(value, " #"); idx != -1 {
			value = strings.TrimSpace(value[:idx])
		}

		// Env vars already set in the shell take precedence over the file.
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("setenv %s: %w", key, err)
			}
		}
	}
	return scanner.Err()
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}
