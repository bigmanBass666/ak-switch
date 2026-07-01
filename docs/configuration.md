# 配置说明

## 推荐方式：TOML 配置（`config.toml`）

### 示例

```toml
[provider.nvidia]
target = "https://integrate.api.nvidia.com/v1"
port = 3001
genai = "https://api.nvidia.com"
cooldown_sec = 60
max_retries = 3

[provider.sensenova]
target = "https://api.sensenova.com/v1"
port = 3002
```

### 字段说明

| 字段 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `target` | 是 | — | 上游 API 基础 URL |
| `port` | 是 | — | HTTP 监听端口 |
| `genai` | 否 | `""` | GenAI 模型基础 URL（`/genai/` 路径路由到此地址） |
| `cooldown_sec` | 否 | `60` | 429 后 Key 冷却的基础时长（秒） |
| `max_retries` | 否 | `3` | 每次请求的最大重试次数 |
| `disable_thinking` | 否 | `false` | 禁用思考模式 |
| `genai_model` | 否 | `""` | GenAI 模型名 |
| `log_level` | 否 | `"info"` | 日志级别：`debug` / `info` / `warn` / `error` |
| `admin_token` | 否 | `""` | 管理 API 鉴权令牌 |
| `keys_file` | 否 | `""` | Key 文件路径（覆盖环境变量 Key） |
| `backoff_cap_sec` | 否 | `120` | Key 指数退避上限（秒），达到此值自动判定日额度耗尽 |
| `backoff_multiplier` | 否 | `2.0` | Key 指数退避倍数 |
| `cb_reset_sec` | 否 | `30` | 上游熔断器 OPEN → HALF_OPEN 超时（秒） |
| `upstream_cb_threshold` | 否 | `5` | 上游熔断器连续失败触发阈值 |
| `health_check_interval_sec` | 否 | `30` | 主动健康检查间隔（秒） |

### 自动生成

```bash
alvus config init
```

在 XDG 配置目录生成含占位 provider 的示例文件，直接编辑填充即可。

## 向后兼容：`.env` 配置

仍支持 `.env` 文件，但仅限单 provider 模式。

```bash
# .env
API_KEYS=nvapi-xxx==nvidia-prod,nvapi-yyy==nvidia-dev
TARGET_BASE_URL=https://integrate.api.nvidia.com/v1
GENAI_BASE_URL=https://api.nvidia.com
PORT=3001
```

### 环境变量一览

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `API_KEYS` | 是 | — | Key 列表，逗号分隔。支持 `key==name` 命名 |
| `TARGET_BASE_URL` | 是 | — | 上游 API 基础 URL |
| `GENAI_BASE_URL` | 是 | — | GenAI 基础 URL |
| `PORT` | 否 | `8080` | HTTP 监听端口 |
| `ADMIN_TOKEN` | 否 | `""` | 管理 API 鉴权令牌 |
| `COOLDOWN_SEC` | 否 | `60` | Key 冷却时长 |
| `MAX_RETRIES` | 否 | `3` | 最大重试次数 |
| `LOG_LEVEL` | 否 | `"info"` | 日志级别 |
| `DISABLE_THINKING` | 否 | `false` | 禁用思考模式 |
| `GENAI_MODEL` | 否 | `""` | 模型名 |
| `KEYS_FILE` | 否 | `""` | Key 文件路径 |
| `BACKOFF_CAP_SEC` | 否 | `120` | 退避上限 |
| `BACKOFF_MULTIPLIER` | 否 | `2` | 退避倍数 |
| `CB_RESET_SEC` | 否 | `30` | 熔断器重置秒数 |
| `UPSTREAM_CB_THRESHOLD` | 否 | `5` | 熔断器阈值 |
| `HEALTH_CHECK_INTERVAL_SEC` | 否 | `30` | 健康检查间隔 |
| `HEALTH_CHECK_PATH` | 否 | `/health` | 健康检查路径 |
| `HEALTH_CHECK_TIMEOUT_SEC` | 否 | `5` | 健康检查超时 |
| `KEYS_ENCRYPTION_KEY` | 否 | `""` | Key 加密密钥（32 字节 hex） |

### Key 命名格式

```text
API_KEYS=nvapi-xxx==nvidia-prod,nvapi-yyy==nvidia-dev,sk-key3
```

未命名的 Key 显示为空名称。名称贯穿日志、API 响应和 Dashboard 显示。

### Key 兼容格式（按优先级降序）

| 变量 | 说明 |
|------|------|
| `API_KEYS` | 主配置，支持 `key1==name1,key2` 格式 |
| `KEY` | 单个或逗号分隔的 Key |
| `KEY1` ~ `KEY5` | 逐个指定 Key，最多 5 个 |
| `KEYA` / `KEYB` | 兼容旧配置 |

## 配置源检测

优先级（高 → 低）：

1. `--config` 标志（目前未实现，预留）
2. `$XDG_CONFIG_HOME/alvus/config.toml`
3. 当前目录下的 `.env`

```bash
# 查看当前使用的配置源
alvus config view
```

## 配置热重载

Alvus 每秒监控 `.env` 文件的修改时间。检测到变更后：

1. 读取新配置
2. 生成变更 diff（Key 值脱敏）
3. 日志输出 diff
4. 应用新配置

重载失败（格式错误、缺少必填字段）则保持旧配置继续运行，不中断服务。
