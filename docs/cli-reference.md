# CLI 参考

单一 `akswitch` 二进制管理所有操作（类 `git` 设计）。

## 全局标志

| 标志 | 说明 |
|------|------|
| `--provider` | 只启动指定 provider |
| `--all` | 启动所有 provider |
| `--tag` | 进程标识标签 |
| `--help` | 显示帮助信息 |

## `akswitch start`

读取 `config.toml`，初始化 Key 池，启动 HTTP 代理服务。

```bash
akswitch start                    # 启动默认 provider（或全部，若未设置 default_provider）
akswitch start --all              # 启动所有 provider
akswitch start --provider <name>  # 只启动指定 provider
```

- 读取 `config.toml` 中所有 `[provider.*]` 段
- 默认只启动 `default_provider` 指定的 provider（若未设置则启动全部）
- `--all` 强制启动所有 provider（忽略 `default_provider` 设置）
- `--provider <name>` 只启动指定 provider（优先级最高）
- 自动写入 `akswitch.pid` 文件，`akswitch stop` 通过此文件发送中断信号

### 启动顺序

1. 读取 `config.toml`
2. 逐个加载 provider 配置和 Key
3. 绑定端口启动 HTTP 服务
4. 启动后台 goroutine（热重载、指标刷新、主动健康检查）
5. 等待中断信号 → 优雅关闭所有实例

## `akswitch config`

```bash
akswitch config init [-p <path>]   # 生成默认 config.toml
akswitch config view                # 打印当前配置
```

`config init` 在 XDG 配置目录生成含两个占位 provider 的示例文件：

| 系统 | 路径 |
|------|------|
| Windows | `%APPDATA%\akswitch\config.toml` |
| Linux | `~/.config/akswitch/config.toml` |
| macOS | `~/Library/Application Support/akswitch/config.toml` |

## `akswitch provider`

```bash
akswitch provider add <name> -t <url> -p <port> [flags]  # 新增 provider
akswitch provider list                                     # 列出所有 provider
akswitch provider remove <name>                            # 删除 provider
```

### `provider add` 标志

| 标志 | 缩写 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `--target` | `-t` | 是 | — | 上游 API 基础 URL |
| `--port` | `-p` | 首个 provider 必填 | — | HTTP 监听端口（后续 provider 复用） |
| `--genai` | `-g` | 否 | — | GenAI 基础 URL（`/genai/` 路径路由） |
| `--cooldown-sec` | `-c` | 否 | `60` | 429 后 Key 冷却时长（秒） |
| `--max-retries` | `-r` | 否 | `3` | 每次请求的最大重试次数 |

示例：

```bash
# 最小配置
akswitch provider add nvidia --target https://integrate.api.nvidia.com/v1 --port 3001

# 完整配置（port 复用第一个 provider 的）
akswitch provider add sensenova \
  --target https://api.sensenova.com/v1 \
  --cooldown-sec 30 --max-retries 5
```

### `provider list` 输出示例

```
Providers (from /home/user/.config/akswitch/config.toml):
  NAME        TARGET                                            PORT
  nvidia      https://integrate.api.nvidia.com/v1               3001
  sensenova   https://api.sensenova.com/v1                      3002
```

## `akswitch key`

```bash
akswitch key add <provider> <key> [--name <name>]    # 添加 Key
akswitch key list <provider>                           # 列出 Key（脱敏显示）
akswitch key remove <provider> <index>                 # 删除 Key
akswitch key disable <provider> <index>                # 禁用 Key
```

- Key 以 AES-256-GCM 加密存储在 `keys/<provider>.enc`
- 环境变量 `KEYS_ENCRYPTION_KEY` 设置加密密钥（32 字节 hex 字符串）
- 未设置加密密钥时以 base64 明文存储

示例：

```bash
# 添加 Key
akswitch key add nvidia nvapi-xxxxxxxxxxxx

# 添加带名称的 Key
akswitch key add nvidia nvapi-yyyyyyyyyyyy --name prod-key

# 列出 Key（脱敏）
akswitch key list nvidia
# Keys for provider "nvidia" (from .../keys/nvidia.enc):
#   [0] nvap...xxxx  (active)
#   [1] nvap...yyyy  (active)  name: prod-key

# 删除 Key[0]
akswitch key remove nvidia 0

# 禁用 Key[1]
akswitch key disable nvidia 1
```

## `akswitch status`

查询所有运行实例的健康状态和统计信息。

```bash
akswitch status
```

输出示例：

```
Server: http://127.0.0.1:4000
Status: ok
PROVIDER       KEYS  CB_STATE
nvidia         6     closed
sensenova      6     closed
Requests: 2588 (success: 2577, failed: 11)
Active keys: 12, Cooling: 0, Disabled: 0
Uptime: 32559s
```

实例无响应时显示错误信息，不崩溃。

## `akswitch logs`

读取运行实例的请求日志。

```bash
akswitch logs                     # 所有实例
akswitch logs nvidia              # 仅 nvidia 实例
```

日志格式：

```
=== Provider "nvidia" (port 3001) ===
  [2026-07-01T12:00:00Z] POST /v1/chat/completions -> 200
  [2026-07-01T12:00:01Z] POST /v1/chat/completions -> 429
```

## `akswitch stop`

读取 `akswitch.pid` 文件，向运行中的进程发送中断信号实现优雅关闭。

```bash
akswitch stop
```

两种停止方式，依次尝试：
1. **PID 文件**：读取 `akswitch.pid` → 发送 `os.Interrupt` → 等待进程退出
2. **健康检查回退**：PID 文件不可用时，探测实例端点并打印运行状态

## `akswitch version`

```bash
akswitch version
```