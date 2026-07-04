package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	adminPort = 8080
)

var PidFileName = "akswitch.pid"

var dashHTML string

var rootCmd = &cobra.Command{
	Use:   "akswitch",
	Short: "API Key rotation proxy for AI providers",
	Run: func(cmd *cobra.Command, args []string) {
		providerFilter, _ := cmd.Flags().GetString("provider")
		startAll, _ := cmd.Flags().GetBool("all")
		startServer(dashHTML, providerFilter, startAll)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("akswitch version unknown")
	},
}

func Execute(dashboardHTML string) error {
	dashHTML = dashboardHTML
	return rootCmd.Execute()
}

func init() {
	rootCmd.Flags().String("provider", "", "Only start the specified provider")
	rootCmd.Flags().Bool("all", false, "Start all providers (default: first provider alphabetically, or error if none configured)")
	rootCmd.AddCommand(versionCmd)
}