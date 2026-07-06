package cmd

import (
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// 自监控重启相关全局变量
var (
	restartExePath string         // 当前二进制路径
	restartSigCh   chan os.Signal // 指向 startServer 的信号通道
	restartTicker  *time.Ticker   // 文件修改时间轮询
)

// SetupSelfRestart 启动二进制自监控 goroutine。
// 每 5 秒检查一次 .exe 文件的 ModTime，若已变化则通知主 goroutine 优雅重启。
// 仅在开发模式中由 startServer 调用。
func SetupSelfRestart(exePath string, sigCh chan os.Signal) {
	if exePath == "" {
		return
	}
	// 非 Windows 系统暂不启用（Windows 是主要开发平台）
	// 后续可按需扩展
	if len(exePath) < 4 || exePath[len(exePath)-4:] != ".exe" {
		return
	}

	restartExePath = exePath
	restartSigCh = sigCh

	fi, err := os.Stat(exePath)
	if err != nil {
		return
	}
	lastMod := fi.ModTime()

	restartTicker = time.NewTicker(5 * time.Second)
	go func() {
		for range restartTicker.C {
			fi, err := os.Stat(exePath)
			if err != nil {
				continue
			}
			mod := fi.ModTime()
			if mod.After(lastMod) {
				slog.Info("检测到二进制更新，正在热重启...")
				// 先删除 PID 文件，让新进程能正常启动
				_ = os.Remove(pidFilePath())
				// 通知主 goroutine 执行优雅关闭
				// 缓冲区大小为 1，不会阻塞
				select {
				case sigCh <- os.Interrupt:
				default:
				}
				return
			}
			lastMod = mod
		}
	}()
}

// ExecRestart 在 shutdown 完成后启动新进程。
// 新进程会继承当前控制台，在同一个终端窗口中运行。
func ExecRestart() {
	if restartExePath == "" {
		return
	}
	if restartTicker != nil {
		restartTicker.Stop()
		restartTicker = nil
	}

	slog.Info("正在启动新进程...")
	cmd := exec.Command(restartExePath, "start")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		slog.Error("启动新进程失败", "error", err)
		return
	}
	slog.Info("新进程已启动", "pid", cmd.Process.Pid)
}