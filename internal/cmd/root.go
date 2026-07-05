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
	rootCmd.AddCommand(versionCmd)
}