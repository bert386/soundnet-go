package support

import (
	"github.com/bert386/soundnet-go/internal/conf"
	"github.com/spf13/cobra"
)

// Command creates the support parent command
func Command(settings *conf.Settings) *cobra.Command {
	supportCmd := &cobra.Command{
		Use:   "support",
		Short: "Commands related to support operations in BirdNET-Go",
	}

	// Add subcommands here
	supportCmd.AddCommand(CollectCommand())
	supportCmd.AddCommand(OpenVINOProbeCommand(settings))

	return supportCmd
}
