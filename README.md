# Alvus — API Key 轮转代理

> 专注单 provider 内 API Key 的智能轮转与熔断，与 [ccswitch](https://github.com/farion1231/cc-switch) 互补。
> ccswitch 负责 provider 级路由与故障转移，Alvus 负责 provider 内 key 级轮转与限流处理。

![Go Version](https://img.shields.io/badge/Go-1.23-blue)
![Build Status](https://img.shields.io/badge/build-passing-brightgreen)
![Tests](https://img.shields.io/badge/tests-115%20passing-brightgreen)
![Docker](https://img.shields.io/badge/docker-ready-blue)

---

## 目录

- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [API 文档](#api-文档)
- [熔断器行为](#熔断器行为)
- [压测与基准测试](#压测与基准测试)
- [测试策略](#测试策略)
- [Docker 部署](#docker-部署)
- [管理模式](#管理模式多实例)

---

## 快速开始

```bash
# 1. 从模板创建配置
cp .env.example .env
# 编辑 .env，填入 API_KEYS、TARGET_BASE_URL、GENAI_BASE_URL

# 2. 启动
go run .

# 3. 验证
curl http://localhost:3000/health
```

---

## 配置说明

所有配置通过 `.env` 文件或环境变量设置。环境变量优先级高于 `.env` 文件。

### 基础配置

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `API_KEYS` | 是 | - | API Key 列表，逗号分隔。支持 `key==name` 命名格式 |
| `TARGET_BASE_URL` | 是 | - | 上游 API 基础 URL（如 `https://integrate.api.nvidia.com/v1`） |
| `GENAI_BASE_URL` | 是 | - | GenAI 模型基础 URL（/genai/ 路径路由到此地址） |
| `PORT` | 否 | `8080` | HTTP 监听端口 |
| `ADMIN_TOKEN` | 否 | 空 | 管理 API 鉴权令牌。设置后所有写操作需 `X-Admin-Token` 请求头 |

### Key 配置（多种格式兼容）

| 变量 | 优先级 | 说明 |
|------|--------|------|
| `API_KEYS` | 最高 | 主配置，支持 `key1==name1,key2,key3==name3` 格式 |
| `KEY` | 次高 | 单个或逗号分隔的 Key |
| `KEY1`-`KEY5` | 第三 | 逐个指定 Key，最多 5 个 |
| `KEYA` / `KEYB` | 最低 | 兼容旧配置 |

### 熔断器配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `COOLDOWN_SEC` | `60` | 429 后 Key 冷却的基础时长（秒） |
| `BACKOFF_CAP_SEC` | `120` | Key 指数退避上限（秒），达到此值自动判定日额度耗尽并永久禁用 |
| `BACKOFF_MULTIPLIER` | `2` | Key 指数退避倍数 |
| `CB_RESET_SEC` | `30` | 上游熔断器 OPEN -> HALF_OPEN 超时（秒） |
| `UPSTREAM_CB_THRESHOLD` | `5` | 上游熔断器连续失败触发阈值 |

### 其他配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MAX_RETRIES` | `3` | 每次请求的最大重试次数 |
| `LOG_LEVEL` | `info` | 日志级别（debug / info / warn / error） |
| `DISABLE_THINKING` | `false` | 禁用思考模式 |
| `GENAI_MODEL` | 空 | GenAI 模型名 |

### Key 命名格式

`API_KEYS` 支持 `key==name` 格式为 Key 命名，名称会贯穿日志、API 响应和 Dashboard 显示：

```
API_KEYS=nvapi-xxx==nvidia-prod,nvapi-yyy==nvidia-dev,sk-key3
```

未命名的 Key 显示为空名称。

### 配置热重载

Alvus 会每秒监控 `.env` 文件的修改时间。检测到变更后自动热重载配置，并在日志中输出变更 diff（Key 值脱敏）。重载失败则保持旧配置继续运行。

---

## API 文档

### 代理端点

| 方法 | 路径 | 说明 |
|------|------|------|
| `*` | `/` | 代理所有请求到上游。`/genai/` 路径路由到 `GENAI_BASE_URL`，其余路由到 `TARGET_BASE_URL` |

### 管理端点

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/health` | 可选 | 健康检查，返回 Key 详情 |
| `GET` | `/api/config` | 否 | 获取当前配置（Key 脱敏） |
| `POST` | `/api/config` | 是 | 更新配置并热重载 |
| `GET` | `/api/keys` | 否 | 列出所有 Key 状态 |
| `POST` | `/api/keys` | 是 | 添加新 Key |
| `DELETE` | `/api/keys` | 是 | 删除 Key（通过 body `{"index": n}`） |
| `POST` | `/api/keys/{index}/disable` | 是 | 禁用指定 Key |
| `PUT` | `/api/keys/{index}/cooldown` | 是 | 冷却指定 Key |
| `DELETE` | `/api/keys/{index}` | 是 | 按索引删除 Key |
| `GET` | `/api/stats` | 否 | 请求统计（成功率、Key 状态、运行时长） |
| `POST` | `/api/reload` | 是 | 从 `.env` 重新加载配置 |
| `GET` | `/logs` | 否 | 获取请求日志（最多 1000 条） |
| `POST` | `/clear` | 是 | 清空日志 |
| `GET` | `/metrics` | 否 | Prometheus 指标 |
| `GET` | `/dashboard` | 否 | Web Dashboard |

> **鉴权**: 当 `ADMIN_TOKEN` 设置后，标记为"是"的端点需在请求头中提供 `X-Admin-Token`。未设置时向后兼容（无需鉴权）。

### 错误码

代理请求失败时返回统一 JSON 格式：

```json
{"error": {"code": "ERROR_CODE", "message": "错误描述"}}
```

| 错误码 | HTTP 状态码 | 触发条件 |
|--------|------------|----------|
| `BAD_REQUEST` | 400 | 请求体过大或不可读 |
| `UPSTREAM_ERROR` | 500 | 构建上游请求失败 |
| `ALL_KEYS_INVALID` | 503 | 所有 Key 额度耗尽或已失效 |
| `EXHAUSTED_RETRIES` | 503 | 重试全部耗尽 |

---

## 熔断器行为

Alvus 采用**两层熔断器架构**，分别处理 Key 级和上游级故障。

### 架构概览

```
请求进入
  │
  ▼
┌─────────────────────────────────────┐
│ UpstreamCircuitBreaker              │  ← 502/503/网络错误影响此层
│  CLOSED → OPEN → HALF_OPEN → CLOSED │
└────────────┬────────────────────────┘
             │ 允许请求
             ▼
┌─────────────────────────────────────┐
│ KeyPool (轮询选择可用 Key)          │  ← 跳过冷却/禁用中的 Key
└────────────┬────────────────────────┘
             │ 选中一个 Key
             ▼
┌─────────────────────────────────────┐
│ KeyCircuitBreaker (每个 Key 一个)    │  ← 429 影响此层
│  CLOSED → OPEN → PERMA              │
└────────────┬────────────────────────┘
             │ 通过
             ▼
        上游请求
```

### KeyCircuitBreaker（Key 级）

每个 API Key 对应一个独立熔断器，跟踪 429（限流）响应。

- **CLOSED**（正常）：Key 可用，直接通过
- **OPEN**（退避冷却）：收到 429 后进入指数退避，公式为 `base * multiplier^attempt + jitter`，jitter 为 0~50% 随机。冷却期内 Key 不会被选中
- **PERMA**（永久禁用）：当退避计算值达到 `BACKOFF_CAP_SEC` 时，自动判定日额度耗尽，永久禁用该 Key。401/403 直接进入 PERMA
- **恢复**：请求成功（2xx/3xx）后自动重置为 CLOSED，attempt 归零

### UpstreamCircuitBreaker（上游级）

跟踪上游服务 502/503 和网络错误。

- **CLOSED**：正常状态，请求直通
- **OPEN**：连续失败达到 `UPSTREAM_CB_THRESHOLD` 时进入，所有请求直接跳过，不发往上游。等待 `CB_RESET_SEC` 后 HALF_OPEN
- **HALF_OPEN**：允许单次探测请求。成功则恢复 CLOSED，失败则回到 OPEN

### HTTP 状态码响应矩阵

| 状态码 | Key 处理 | 上游熔断器 | 重试行为 |
|--------|---------|-----------|---------|
| 429 | 指数退避 + 可能 PERMA | 不记录 | 重试下一个 Key |
| 401/403 | 直接 PERMA + Disable | 不记录 | 重试下一个 Key |
| 502/503 | 不惩罚（Key 无辜） | 记录失败 | 重试同一或其他 Key |
| 网络错误 | 不惩罚 | 记录失败 | 重试 |
| 其他 4xx | 不惩罚 | 不记录 | 直接返回，不重试 |
| 2xx/3xx | 重置 CLOSED | 记录成功 | 返回响应 |

### 全冷却时的行为

当所有 Key 都处于冷却状态时：
- `TimeUntilAvailable()` 返回最短等待时长
- 等待时长为 `最短冷却时间 + 随机 jitter (0~500ms)`
- 避免多个请求同时恢复引起的 thundering herd

---

## 压测与基准测试

### Go Benchmark（单元级）

| 场景 | 结果 | 说明 |
|------|------|------|
| `BenchmarkKeyPoolNext/keys-1` | ~50 ns/op, 0 allocs/op | 单 Key 轮询 |
| `BenchmarkKeyPoolNext/keys-5` | ~50 ns/op, 0 allocs/op | 5 Key 轮询 |
| `BenchmarkKeyPoolNext/keys-10` | ~50 ns/op, 0 allocs/op | 10 Key 轮询 |
| `BenchmarkProxyRequest` | ~165µs/op, 130KB/op | 正常代理请求 |
| `BenchmarkProxyAllKeysCooldown` | ~2.5s/op | 全 Key 冷却等待 |
| `BenchmarkProxyFlakyUpstream` | ~425ms/op | 上游抖动重试 |

```powershell
# 运行基准测试
go test -bench=BenchmarkKeyPoolNext -benchmem ./internal/keypool/
go test -bench=BenchmarkProxy -benchmem .
```

### Vegeta 负载压测（HTTP 级）

使用 [Vegeta](https://github.com/tsenart/vegeta) 进行 HTTP 负载压测。

| 场景 | 速率 | 结果 |
|------|------|------|
| 低负载冒烟测试 | 50 QPS, 10s | 100% 成功，p99 15.8ms，均值 1.4ms |
| 正常并发 | 500 QPS, 60s | 32% 成功（i5 笔记本饱和，需生产级机器验证） |
| 中等负载 | 200 QPS, 30s | 1.6% 成功（大量超时，需要性能调优） |
| 全 Key 冷却 | 100 QPS, 120s | 正确返回熔断降级，无崩溃 |

完整压测脚本及使用说明见 [test/load/README.md](test/load/README.md)。

```powershell
# 运行正常并发场景
.\test\load\run-load-test.ps1 -Scenario normal-concurrent -Target http://localhost:8080
```

---

## 测试策略

本项目采用 **Testing Trophy** 模型（与 Go 生态、Kubernetes、Prometheus 共识一致）：

```
优先级排序：
1. 静态分析（go vet, go build, 类型检查）— 零成本，收益最大
2. 集成验收测试（mock upstream + 真实代理请求）— 主力，最高 ROI
3. 单元测试（纯内部逻辑）— 只在复杂逻辑时写
4. HTTP Handler 测试 — 仅 API 契约变更时才写
```

### 测试分布

| 文件 | 测试数 | 类型 |
|------|--------|------|
| `internal/config/config_test.go` | 23 | 单元测试 |
| `internal/keypool/keypool_test.go` | 12 | 单元测试 |
| `internal/logstore/logstore_test.go` | 4 | 单元测试 |
| `internal/circuitbreaker/key_test.go` | 10 | 单元测试 |
| `internal/circuitbreaker/upstream_test.go` | 9 | 单元测试 |
| `handlers_test.go` | 14 | Handler 测试 |
| `logstore_test.go` | 4 | Handler 测试 |
| `proxy_test.go` | 25 | **集成验收测试** |
| `integration_test.go` | 4 | **集成测试** |
| `metrics_verification_test.go` | 6 | **集成验收测试** |
| **总计** | **115** | |

```powershell
# 运行所有测试
go test -race ./...

# 运行集成验收测试（含 mock upstream）
go test -run TestProxy -v .
```

### 验收测试三原则

每次测试涵盖：
1. **用户可见行为** — 外部可观察的变化是什么？
2. **生效证明** — 测试能在改动被删除时失败吗？
3. **破坏测试** — 故意捣乱（坏配置、空数据、上游挂了）时程序怎么死？

---

## Docker 部署

### 快速启动

```bash
# 1. 创建 .env 配置文件
cp .env.example .env

# 2. 构建并启动容器
docker compose up -d

# 3. 检查健康状态
curl http://localhost:3000/health

# 4. 查看日志
docker compose logs -f
```

### 自定义端口

```bash
PORT=8080 docker compose up -d
```

### 仅构建

```bash
docker build -t alvus .
docker run --rm alvus --help
```

### 健康检查

容器内置 Docker HEALTHCHECK（每 30 秒探测 `/health` 端点），不健康时自动重启。

### 完整监控栈

Alvus 自带 Prometheus 指标，搭配预置的 Grafana 面板开箱即用：

```bash
# 启动完整栈（Alvus + Prometheus + Grafana）
docker compose up -d

# 访问 Grafana 面板
open http://localhost:3001
```

| 服务 | 端口 | 说明 |
|------|------|------|
| Alvus | `3000` | API Key 代理 |
| Prometheus | `9090` | 指标采集（`alvus:3000/metrics`） |
| Grafana | `3001` | 预置监控面板 |

**预置 Grafana 面板包含：**
- 请求速率（按状态码/Key 分布）
- 请求延迟 p50 / p95 / p99
- Key 池状态（活跃/冷却/禁用）
- 上游熔断器状态
- 上游错误按类型分布
- 健康检查成功率与延迟

自定义端口：
```bash
# 改变映射端口
PORT=3000 PROMETHEUS_PORT=9090 GRAFANA_PORT=3001 docker compose up -d
```

> 注意：Grafana 面板无需登录（匿名只读访问），首次启动自动加载预置数据源和仪表盘。

---

## 管理模式（多实例）

Alvus 支持通过 `--manage` 参数启动多供应商管理。一个 `manage.json` 文件管理多个独立子进程，每个子进程运行一个独立的 Alvus 实例。

### 原理

```
./alvus.exe --manage manage.json

┌─ alvus.exe（管理器）──────────────────────────┐
│                                                  │
│  ├─ 子进程: NVIDIA（端口 4000）→ Key 轮换 → API │
│  ├─ 子进程: SenseNova（端口 4002）→ Key 轮换 → API │
│  ├─ 子进程: DeepSeek（端口 4003）→ Key 轮换 → API │
│                                                  │
│  挂了自动重启 ✓  Ctrl+C 全关 ✓                 │
└──────────────────────────────────────────────────┘
```

### 配置 manage.json

```json
{
  "providers": [
    {
      "name": "nvidia",
      "target_url": "https://integrate.api.nvidia.com/v1",
      "api_keys": ["nvapi-key1", "nvapi-key2"],
      "port": 4000
    },
    {
      "name": "deepseek",
      "target_url": "https://api.deepseek.com/v1",
      "api_keys": ["sk-key1"],
      "port": 4001,
      "disabled": true
    }
  ]
}
```

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | 是 | 供应商名称 |
| `target_url` | 是 | 上游 API 地址 |
| `genai_url` | 否 | NVIDIA GenAI 专用地址 |
| `api_keys` | 是 | API Key 列表，至少一个 |
| `port` | 否 | 监听端口，不填自动分配（从 4000 开始） |
| `disabled` | 否 | `true` 时跳过此供应商 |

### 启动 / 停止

```powershell
# 启动
.\alvus.exe --manage manage.json

# 停止
Ctrl+C 自动关闭所有子进程并清理临时文件
```

### 特性

- **自动重启**：子进程意外退出后，管理器每 3 秒检查一次并自动拉起
- **日志持久化**：自动写入 `logs/alvus-manage.log`，同时输出到终端
- **进程隔离**：每个子进程独立工作目录，互不干扰
- **一键全关**：Ctrl+C 传播到所有子进程

> `manage.json` 已加入 `.gitignore`，不会误提交到 git。模板见 `manage.example.json`。

---

## 目录结构

```
alvus/
├── main.go                          # CLI 入口 + Server 启动
├── manage.go                        # 管理模式（多实例管理）
├── manage.example.json              # 管理模式配置模板
├── dashboard.html                   # Web Dashboard（通过 //go:embed 嵌入）
├── Dockerfile                       # 多阶段构建
├── docker-compose.yml               # Docker Compose 部署
├── .env.example                     # 环境变量模板
├── go.mod
├── internal/
│   ├── server/                      # HTTP 服务（Handler / Proxy / 生命周期）
│   ├── config/config.go             # 配置加载、校验、Diff、脱敏
│   ├── keypool/keypool.go           # Key 池（轮询、冷却、禁用、统计）
│   ├── circuitbreaker/
│   │   ├── key.go                   # Key 级熔断器（三态 + 指数退避）
│   │   └── upstream.go              # 上游级熔断器（三态 + 半开探测）
│   ├── logstore/logstore.go         # 请求日志环形缓冲区
│   ├── metrics/metrics.go           # Prometheus 指标定义
│   └── utils/utils.go               # 工具函数（Key 脱敏、Header 复制）
└── test/load/                       # Vegeta 压测脚本与场景
```

---

## 项目定位

Alvus 专注于 API Key 轮转这一垂直领域：

- **不做** provider 级路由（ccswitch 已成熟）
- **不做** 请求整流、格式化、响应变换
- **不做** 多 provider 流量分发
- **专注** 单 provider 内多 Key 的智能轮转、限流处理、自动熔断
