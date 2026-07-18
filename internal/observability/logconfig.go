package observability

import (
	"log/slog"
	"os"
	"strings"
)

// LogConfig controls the process-wide structured logger.
type LogConfig struct {
	Level  slog.Level // minimum level to emit (default Info)
	Format string     // "json" or "text" (default "json")
}

// LogConfigFromEnv reads GATEWAY_LOG_LEVEL and GATEWAY_LOG_FORMAT from the environment.
// Unknown values are silently ignored; the defaults (Info / json) apply.
func LogConfigFromEnv() LogConfig {
	cfg := LogConfig{
		Level:  slog.LevelInfo,
		Format: "json",
	}

	if v := strings.ToLower(os.Getenv("GATEWAY_LOG_LEVEL")); v != "" {
		switch v {
		case "debug":
			cfg.Level = slog.LevelDebug
		case "info":
			cfg.Level = slog.LevelInfo
		case "warn":
			cfg.Level = slog.LevelWarn
		case "error":
			cfg.Level = slog.LevelError
		}
	}

	if v := strings.ToLower(os.Getenv("GATEWAY_LOG_FORMAT")); v != "" {
		switch v {
		case "text", "console":
			cfg.Format = "text"
		case "json":
			cfg.Format = "json"
		}
	}

	return cfg
}
