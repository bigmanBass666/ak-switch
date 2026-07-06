package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

		// Poll for process exit with timeout instead of blocking on proc.Wait().
		// proc.Wait() on a non-child process can block indefinitely on some
		// platforms (e.g. Windows), causing a goroutine leak if wrapped in a
		// goroutine+select pattern. Polling avoids this entirely.
		deadline := time.Now().Add(10 * time.Second)
		exited := false
		for time.Now().Before(deadline) {
			if !processRunning(pid) {
				exited = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		proc.Release()

		if exited {
			fmt.Println("AK Switch stopped gracefully")
			_ = os.Remove(pidFilePath())
			return nil
		}

		fmt.Println("Timed out waiting for graceful shutdown.")
		fmt.Println("Try: kill -9", pid)
		return fmt.Errorf("shutdown timed out")
	},
}

// processRunning checks whether a process with the given PID is still alive.
func processRunning(pid int) bool {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
		out, err := cmd.Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	// Unix: signal 0 checks process existence without sending a signal
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer proc.Release()
	return proc.Signal(syscall.Signal(0)) == nil
}
