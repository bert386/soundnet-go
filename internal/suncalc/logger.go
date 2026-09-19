// Package suncalc provides structured logging for the suncalc package.
package suncalc

import (
	"github.com/bert386/soundnet-go/internal/logger"
)

// GetLogger returns the logger for the suncalc package.
// The logger is fetched from the global logger each time so it uses the
// current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("suncalc")
}
