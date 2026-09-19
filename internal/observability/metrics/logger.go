// Package metrics provides Prometheus metrics for observability.
package metrics

import "github.com/bert386/soundnet-go/internal/logger"

// GetLogger returns the metrics package logger scoped to the telemetry module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("telemetry")
}
