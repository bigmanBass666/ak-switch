# 配置说明

## 推荐方式：TOML 配置（`config.toml`）

### 示例

```toml
port = 3001

[provider.nvidia]
target = "https://integrate.api.nvidia.com/v1"
genai = "https://api.nvidia.com"
cooldown_sec = 60
max_retries = 3

[provider.sensenova]
target = "https://api.sensenova.com/v1"
```

### 顶层字段

| 字段 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `port` | 否 | `8080` | HTTP 监听端口（所有 provider 共享） |
| `default_provider` | 否 | `""` | 默认启动的 provider 名称。设置后 `akswitch start` 只启动此 provider；未设置时自动使用第一个 provider（按名称字母序）|

### Provider 字段

| 字段 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `target` | 是 | — | 上游 API 基础 URL |
| `genai` | 否 | `""` | GenAI 模型基础 URL（`/genai/` 路径路由到此地址） |
| `cooldown_sec` | 否 | `60` | 429 后 Key 冷却的基础时长（秒） |
| `max_retries` | 否 | `3` | 每次请求的最大重试次数 |
| `disable_thinking` | 否 | `false` | 禁用思考模式 |
| `genai_model` | 否 | `""` | GenAI 模型名 |
| `log_level` | 否 | `"info"` | 日志级别：`debug` / `info` / `warn` / `error` |
| `admin_token` | 否 | `""` | 管理 API 鉴权令牌 |
| `keys_file` | 否 | `""` | Key 文件路径（覆盖内联 Key） |
| `backoff_cap_sec` | 否 | `120` | Key 指数退避上限（秒），达到此值自动判定日额度耗尽 |
| `backoff_multiplier` | 否 | `2.0` | Key 指数退避倍数 |
| `cb_reset_sec` | 否 | `30` | 上游熔断器 OPEN → HALF_OPEN 超时（秒） |
| `upstream_cb_threshold` | 否 | `5` | 上游熔断器连续失败触发阈值 |
| `health_check_interval_sec` | 否 | `30` | 主动健康检查间隔（秒） |
| `log_file` | 否 | `""` | 日志文件路径（空 = 不启用文件日志）。设置后 slog 同时输出到 stderr 和文件 |
| `log_max_size` | 否 | `100` | 日志文件轮转大小（MB），达到此值自动轮转 |
| `log_max_age` | 否 | `7` | 日志文件保留天数，超过此期限的轮转文件自动删除 |

### 自动生成

```bash
akswitch config init
```

在 XDG 配置目录生成含占位 provider 的示例文件，直接编辑填充即可。

## 配置热重载

AK Switch 每秒监控 `config.toml` 文件的修改时间。检测到变更后：

1. 读取新配置
2. 生成变更 diff（Key 值脱敏）
3. 日志输出 diff
4. 应用新配置

重载失败（格式错误、缺少必填字段）则保持旧配置继续运行，不中断服务。
