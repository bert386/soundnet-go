package audiocore

import "github.com/bert386/soundnet-go/internal/logger"

// GetLogger returns the audiocore module logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("audiocore")
}
