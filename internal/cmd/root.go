package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	adminPort = 8080
)

var dashHTML string

var rootCmd = &cobra.Command{
	Use:           "akswitch",
	Short:         "API Key rotation proxy for AI providers",
	SilenceUsage:  true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("akswitch version %s\n", rootCmd.Version)
	},
}

func Execute(dashboardHTML string) error {
	dashHTML = dashboardHTML
	return rootCmd.Execute()
}

func init() {
	rootCmd.Version = "dev"
	rootCmd.AddCommand(versionCmd)
}