# CLI 参考

单一 `alvus` 二进制管理所有操作（类 `git` 设计）。

## 全局标志

| 标志 | 说明 |
|------|------|
| `--local` | 绑定到 `127.0.0.1`（仅本地访问） |
| `--network-only` | 绑定到 `0.0.0.0`（局域网可访问） |
| `--tag` | 进程标识标签 |
| `--help` | 显示帮助信息 |

`--local` 和 `--network-only` 互斥，优先级 `--local` > `--network-only` > 默认（`0.0.0.0`）。

## `alvus start`

加载配置（自动检测 `config.toml` 或 `.env`），初始化 Key 池，启动 HTTP 代理服务。

```bash
alvus start [--local] [--network-only]
```

- **TOML 模式**：读取 `config.toml` 中所有 `[provider.*]` 段，每段启动一个独立实例
- **`.env` 模式**：向后兼容，单 provider 单实例
- 自动写入 `alvus.pid` 文件，`alvus stop` 通过此文件发送中断信号

### 启动顺序

1. 检测配置源（`config.toml` > `.env`）
2. 逐个加载 provider 配置和 Key
3. 绑定端口启动 HTTP 服务
4. 启动后台 goroutine（热重载、指标刷新、主动健康检查）
5. 等待中断信号 → 优雅关闭所有实例

## `alvus config`

```bash
alvus config init [-p <path>]   # 生成默认 config.toml
alvus config view                # 打印当前配置
```

`config init` 在 XDG 配置目录生成含两个占位 provider 的示例文件：

| 系统 | 路径 |
|------|------|
| Windows | `%APPDATA%\alvus\config.toml` |
| Linux | `~/.config/alvus/config.toml` |
| macOS | `~/Library/Application Support/alvus/config.toml` |

检测到 `.env` 存在时打印迁移提示。

## `alvus provider`

```bash
alvus provider add <name> -t <url> -p <port> [flags]  # 新增 provider
alvus provider list                                     # 列出所有 provider
alvus provider remove <name>                            # 删除 provider
```

### `provider add` 标志

| 标志 | 缩写 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `--target` | `-t` | 是 | — | 上游 API 基础 URL |
| `--port` | `-p` | 是 | — | HTTP 监听端口 |
| `--genai` | `-g` | 否 | — | GenAI 基础 URL（`/genai/` 路径路由） |
| `--cooldown-sec` | `-c` | 否 | `60` | 429 后 Key 冷却时长（秒） |
| `--max-retries` | `-r` | 否 | `3` | 每次请求的最大重试次数 |

示例：

```bash
# 最小配置
alvus provider add nvidia --target https://integrate.api.nvidia.com/v1 --port 3001

# 完整配置
alvus provider add sensenova \
  --target https://api.sensenova.com/v1 \
  --port 3002 -g https://api.sensenova.com \
  --cooldown-sec 30 --max-retries 5
```

### `provider list` 输出示例

```
Providers (from /home/user/.config/alvus/config.toml):
  NAME        TARGET                                            PORT
  nvidia      https://integrate.api.nvidia.com/v1               3001
  sensenova   https://api.sensenova.com/v1                      3002
```

## `alvus key`

```bash
alvus key add <provider> <key> [--name <name>]    # 添加 Key
alvus key list <provider>                           # 列出 Key（脱敏显示）
alvus key remove <provider> <index>                 # 删除 Key
alvus key disable <provider> <index>                # 禁用 Key
```

- Key 以 AES-256-GCM 加密存储在 `keys/<provider>.enc`
- 环境变量 `KEYS_ENCRYPTION_KEY` 设置加密密钥（32 字节 hex 字符串）
- 未设置加密密钥时以 base64 明文存储

示例：

```bash
# 添加 Key
alvus key add nvidia nvapi-xxxxxxxxxxxx

# 添加带名称的 Key
alvus key add nvidia nvapi-yyyyyyyyyyyy --name prod-key

# 列出 Key（脱敏）
alvus key list nvidia
# Keys for provider "nvidia" (from .../keys/nvidia.enc):
#   [0] nvap...xxxx  (active)
#   [1] nvap...yyyy  (active)  name: prod-key

# 删除 Key[0]
alvus key remove nvidia 0

# 禁用 Key[1]
alvus key disable nvidia 1
```

## `alvus status`

查询所有运行实例的健康状态和统计信息。

```bash
alvus status
```

输出示例：

```
PROVIDER       PORT   HEALTH     ACTIVE KEYS  REQUESTS   UPTIME
nvidia         3001   UP         3            42         360s
sensenova      3002   UP         2            18         360s
```

实例无响应时显示 `DOWN`，不崩溃。

## `alvus logs`

读取运行实例的请求日志。

```bash
alvus logs                     # 所有实例
alvus logs nvidia              # 仅 nvidia 实例
```

日志格式：

```
=== Provider "nvidia" (port 3001) ===
  [2026-07-01T12:00:00Z] POST /v1/chat/completions -> 200
  [2026-07-01T12:00:01Z] POST /v1/chat/completions -> 429
```

## `alvus stop`

读取 `alvus.pid` 文件，向运行中的进程发送中断信号实现优雅关闭。

```bash
alvus stop
```

两种停止方式，依次尝试：
1. **PID 文件**：读取 `alvus.pid` → 发送 `os.Interrupt` → 等待进程退出
2. **健康检查回退**：PID 文件不可用时，探测实例端点并打印运行状态

## `alvus version`

```bash
alvus version
```
