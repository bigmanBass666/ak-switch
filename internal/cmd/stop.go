package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running akswitch server",
	Long:  `Stop the akswitch proxy server gracefully using the PID file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pidData, err := os.ReadFile(pidFilePath())
		if err != nil {
			fmt.Println("Could not read PID file. Try:")
			fmt.Println("  - Windows: taskkill /F /IM akswitch.exe")
			fmt.Println("  - Linux/macOS: kill $(pgrep akswitch)")
			return fmt.Errorf("failed to read PID file: %w", err)
		}

		pidStr := strings.TrimSpace(string(pidData))
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			fmt.Println("Invalid PID in file. Try:")
			fmt.Println("  - Windows: taskkill /F /IM akswitch.exe")
			fmt.Println("  - Linux/macOS: kill $(pgrep akswitch)")
			return fmt.Errorf("invalid PID in %s", pidFilePath())
		}

		fmt.Printf("Stopping akswitch (PID %d)...\n", pid)

		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find process: %w", err)
		}

		if err := proc.Signal(os.Interrupt); err != nil {
			// On Windows, os.Interrupt might not work for non-child processes
			fmt.Println("PID signal failed. Try:")
			fmt.Println("  - Windows: taskkill /F /PID", pid)
			fmt.Println("  - Linux/macOS: kill", pid)
			return fmt.Errorf("failed to send interrupt: %w", err)
		}

		// Wait for process to exit
		done := make(chan struct{})
		go func() {
			proc.Wait()
			close(done)
		}()
		select {
		case <-done:
			fmt.Println("AK Switch stopped gracefully")
			_ = os.Remove(pidFilePath())
			return nil
		case <-time.After(10 * time.Second):
			fmt.Println("Timed out waiting for graceful shutdown.")
			fmt.Println("Try: kill -9", pid)
			return fmt.Errorf("shutdown timed out")
		}
	},
}