package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const workDirName = "manage-work"

// logFile is the file writer for all manager logs (terminal + file).
var logFile *os.File

// ── Config ─────────────────────────────────────

type ManageConfig struct {
	Providers []ProviderDef `json:"providers"`
}

type ProviderDef struct {
	Name      string   `json:"name"`
	TargetURL string   `json:"target_url"`
	GenaiURL  string   `json:"genai_url,omitempty"`
	APIKeys   []string `json:"api_keys"`
	Port      int      `json:"port"`
	Disabled  bool     `json:"disabled,omitempty"`

	// Per-provider overrides (optional)
	CooldownSec *int   `json:"cooldown_sec,omitempty"`
	MaxRetries  *int   `json:"max_retries,omitempty"`
	AdminToken  string `json:"admin_token,omitempty"`
}

// ── Managed Instance ───────────────────────────

type ManagedInstance struct {
	Name    string
	Dir     string
	Port    int
	Cmd     *exec.Cmd
	Running bool
	mu      sync.Mutex
}

// writeEnvFile generates and writes the .env file for a managed instance.
func (m *ManagedInstance) writeEnvFile(cfg ProviderDef) error {
	if err := os.MkdirAll(m.Dir, 0755); err != nil {
		return fmt.Errorf("create dir %q: %w", m.Dir, err)
	}
	cooldownSec := 60
	if cfg.CooldownSec != nil {
		cooldownSec = *cfg.CooldownSec
	}
	lines := []string{
		fmt.Sprintf("PORT=%d", m.Port),
		fmt.Sprintf("TARGET_BASE_URL=%s", strings.TrimRight(cfg.TargetURL, "/")),
		fmt.Sprintf("API_KEYS=%s", strings.Join(cfg.APIKeys, ",")),
		fmt.Sprintf("COOLDOWN_SEC=%d", cooldownSec),
	}
	if cfg.GenaiURL != "" {
		lines = append(lines, fmt.Sprintf("GENAI_BASE_URL=%s", strings.TrimRight(cfg.GenaiURL, "/")))
	}
	if cfg.MaxRetries != nil {
		lines = append(lines, fmt.Sprintf("MAX_RETRIES=%d", *cfg.MaxRetries))
	}
	if cfg.AdminToken != "" {
		lines = append(lines, fmt.Sprintf("ADMIN_TOKEN=%s", cfg.AdminToken))
	}
	content := strings.Join(lines, "\n") + "\n"
	envPath := filepath.Join(m.Dir, ".env")
	if err := os.WriteFile(envPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	return nil
}

func (m *ManagedInstance) Start(binary string, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Running {
		return nil
	}
	absDir, err := filepath.Abs(m.Dir)
	if err != nil {
		return fmt.Errorf("bad dir %q: %w", m.Dir, err)
	}
	if _, err := os.Stat(filepath.Join(absDir, ".env")); os.IsNotExist(err) {
		return fmt.Errorf(".env not found in %s — writeEnvFile() was not called", absDir)
	}
	args := []string{"-local"}
	if tag != "" {
		args = append(args, "-tag", tag)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = absDir

	// Capture both stdout and stderr in real-time
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", m.Name, err)
	}
	m.Cmd = cmd
	m.Running = true

	// Real-time stdout reader
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			log.Printf("[%s] %s", m.Name, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Printf("⚠️ [%s] stdout scanner error: %v", m.Name, err)
		}
	}()

	// Real-time stderr reader
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			log.Printf("⚠️ [%s] %s", m.Name, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			log.Printf("⚠️ [%s] stderr scanner error: %v", m.Name, err)
		}
	}()

	// Wait and log exit code
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		m.Running = false
		m.Cmd = nil
		m.mu.Unlock()

		code := cmd.ProcessState.ExitCode()
		if err != nil {
			log.Printf("❌ [%s] exited with error (PID %d, code %d): %v", m.Name, cmd.Process.Pid, code, err)
		} else {
			log.Printf("⚠️ [%s] exited normally (PID %d, code %d)", m.Name, cmd.Process.Pid, code)
		}
	}()

	log.Printf("✅ [%s] started (PID %d, port %d, dir: %s)", m.Name, cmd.Process.Pid, m.Port, absDir)
	return nil
}

func (m *ManagedInstance) Stop() {
	m.mu.Lock()
	if !m.Running || m.Cmd == nil || m.Cmd.Process == nil {
		m.mu.Unlock()
		return
	}
	pid := m.Cmd.Process.Pid
	log.Printf("🛑 [%s] stopping (PID %d)...", m.Name, pid)
	m.Cmd.Process.Kill()

	// 等待进程退出，最多 5 秒
	done := make(chan struct{})
	go func() {
		m.Cmd.Wait()
		close(done)
	}()
	m.mu.Unlock() // 先解锁，避免 Wait 期间死锁
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("⚠️ [%s] 进程 %d 未在 5s 内退出", m.Name, pid)
	}
	m.mu.Lock()
	m.Running = false
	m.Cmd = nil
	m.mu.Unlock()
	log.Printf("🛑 [%s] stopped", m.Name)
}

// ── Manager ────────────────────────────────────

type Manager struct {
	instances       []*ManagedInstance
	config          ManageConfig
	workBase        string
	tag             string
	healthFailures  map[string]int // name -> consecutive failure count
}

// detectOldFormat checks if the config JSON has old-format fields (e.g. "dir").
func detectOldFormat(data []byte) error {
	var rawMap map[string]interface{}
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return nil
	}
	rawProviders, ok := rawMap["providers"]
	if !ok {
		return nil
	}
	providers, ok := rawProviders.([]interface{})
	if !ok {
		return nil
	}
	for _, p := range providers {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasDir := pm["dir"]; hasDir {
			return fmt.Errorf("manage.json is in the old format. Check manage.example.json for the new format")
		}
	}
	return nil
}

func LoadManagerConfig(path string) (ManageConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ManageConfig{}, fmt.Errorf("读取 %s 失败: %w", path, err)
	}

	if err := detectOldFormat(data); err != nil {
		return ManageConfig{}, err
	}

	var cfg ManageConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ManageConfig{}, fmt.Errorf("解析 %s 失败: %w", path, err)
	}

	usedPorts := make(map[int]string)
	for i, p := range cfg.Providers {
		if p.Name == "" {
			return ManageConfig{}, fmt.Errorf("providers[%d]: name 不能为空", i)
		}
		if p.TargetURL == "" {
			return ManageConfig{}, fmt.Errorf("providers[%d] %q: target_url 不能为空", i, p.Name)
		}
		if len(p.APIKeys) == 0 {
			return ManageConfig{}, fmt.Errorf("providers[%d] %q: 至少需要一个 api_key", i, p.Name)
		}
		if p.Port == 0 {
			p.Port = 4000 + i
			cfg.Providers[i] = p
		}
		if existing, ok := usedPorts[p.Port]; ok {
			return ManageConfig{}, fmt.Errorf("❌ 端口 %d 冲突：%q 和 %q 都用了同一个端口", p.Port, existing, p.Name)
		}
		usedPorts[p.Port] = p.Name
	}
	return cfg, nil
}

func NewManager(cfg ManageConfig, tag string) *Manager {
	m := &Manager{
		config:         cfg,
		workBase:       filepath.Join(workDirName),
		tag:            tag,
		healthFailures: make(map[string]int),
	}
	for _, p := range cfg.Providers {
		if p.Disabled {
			continue
		}
		if strings.Contains(p.Name, "..") || strings.Contains(p.Name, "/") || strings.Contains(p.Name, "\\") {
			log.Printf("Provider name %q contains invalid characters — skipping", p.Name)
			continue
		}
		workDir := filepath.Join(m.workBase, p.Name)
		inst := &ManagedInstance{
			Name: p.Name,
			Dir:  workDir,
			Port: p.Port,
		}
		if err := inst.writeEnvFile(p); err != nil {
			log.Printf("❌ [%s] 创建配置失败: %v", p.Name, err)
			continue
		}
		m.instances = append(m.instances, inst)
	}
	return m
}

func (m *Manager) StartAll() int {
	count := 0
	self, _ := os.Executable()
	if self == "" {
		self = "alvus.exe"
	}
	for _, inst := range m.instances {
		if err := inst.Start(self, m.tag); err != nil {
			log.Printf("❌ [%s] 启动失败: %v", inst.Name, err)
		} else {
			count++
		}
	}
	return count
}

func (m *Manager) StopAll() {
	for _, inst := range m.instances {
		inst.Stop()
	}
}

func (m *Manager) WatchAndRestart(stop <-chan struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			self, _ := os.Executable()
			if self == "" {
				self = "alvus.exe"
			}
			for _, inst := range m.instances {
				inst.mu.Lock()
				if !inst.Running {
					m.healthFailures[inst.Name] = 0
					inst.mu.Unlock()
					log.Printf("🔄 [%s] 重启中...", inst.Name)
					if err := inst.Start(self, m.tag); err != nil {
						log.Printf("❌ [%s] 重启失败: %v", inst.Name, err)
					}
				} else {
					inst.mu.Unlock()
					// Health check — only on 127.0.0.1, never localhost
					resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", inst.Port))
					if err == nil && resp.StatusCode == http.StatusOK {
						resp.Body.Close()
						m.healthFailures[inst.Name] = 0
					} else {
						if err != nil {
							log.Printf("⚠️ [%s] 健康检查网络错误: %v", inst.Name, err)
						} else if resp != nil {
							resp.Body.Close()
						}
						m.healthFailures[inst.Name]++
						log.Printf("⚠️ [%s] 健康检查失败 (%d/3)", inst.Name, m.healthFailures[inst.Name])
						if m.healthFailures[inst.Name] >= 3 {
							log.Printf("🔴 [%s] 健康检查连续失败 %d 次 — 重启", inst.Name, m.healthFailures[inst.Name])
							inst.Stop()
							m.healthFailures[inst.Name] = 0
						}
					}
				}
			}
		}
	}
}

// ── Log File ────────────────────────────────────

func openLogFile() (*os.File, error) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	path := filepath.Join(logDir, "alvus-manage.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return f, nil
}

// ── RunMode: Manager ──────────────────────────

func runManager(managePath string, tag string, stop <-chan struct{}) {
	// Set up file logging (in addition to terminal output)
	f, err := openLogFile()
	if err == nil {
		logFile = f
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
		log.Printf("📝 日志文件: logs/alvus-manage.log")
	} else {
		log.Printf("⚠️ 无法创建日志文件: %v", err)
	}
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	cfg, err := LoadManagerConfig(managePath)
	if err != nil {
		log.Printf("❌ %v", err)
		os.Exit(1)
	}

	mgr := NewManager(cfg, tag)
	n := mgr.StartAll()
	log.Printf("🚀 已启动 %d/%d 个实例", n, len(mgr.instances))

	go mgr.WatchAndRestart(stop)

	<-stop
	log.Printf("🛑 管理器关闭中...")
	mgr.StopAll()

	workBase := filepath.Join(workDirName)
	if fi, err := os.Stat(workBase); err == nil && fi.IsDir() {
		if err := os.RemoveAll(workBase); err != nil {
			log.Printf("⚠️ 清理工作目录失败: %v", err)
		}
	}
}