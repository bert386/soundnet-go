// range.go range command code
package rangefilter

import (
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/spf13/cobra"
)

// Command creates the range parent command
func Command(settings *conf.Settings) *cobra.Command {
	rangeCmd := &cobra.Command{
		Use:   "range",
		Short: "Commands related to range operations in BirdNET-Go",
	}

	// Add subcommands here
	rangeCmd.AddCommand(PrintCommand(settings))

	return rangeCmd
}
