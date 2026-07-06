// restart-helper 是 ExecRestart 测试用的辅助二进制。
// 被 ExecRestart 以 "start" 参数启动，通过 marker 文件向测试报告。
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	markerDir := os.Getenv("RESTART_HELPER_MARKER_DIR")
	if markerDir == "" {
		os.Exit(1)
	}
	marker := filepath.Join(markerDir, "started.marker")
	_ = os.MkdirAll(markerDir, 0755)
	_ = os.WriteFile(marker, []byte(fmt.Sprintf("args:%v", os.Args)), 0644)
}
