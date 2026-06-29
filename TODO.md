# Alvus — TODO & 项目建议

> 基于对项目现有架构、代码和目标的综合分析，整理出的后续可做事项。
> 按优先级和影响范围分组，具体实施时可根据当前聚焦点选择。

## ✅ 已完成（已合并到 main 或 PR 中）

### 核心功能
- [x] **配置管理增强** — `internal/config` 包，含 `Validate()`/`Diff()`/`Sanitized()`，启动校验 + 热重载校验 + diff 日志
- [x] **配置管理集成测试** — `integration_test.go`（启动校验 / 热重载 diff / 热重载回滚）
- [x] **日志系统迁移为结构化日志** — 全部 `log.Printf` → `log/slog`，分级 Info/Warn/Error
- [x] **配置变更 diff 日志** — 热重载时记录变更字段（Key 脱敏）
- [x] **关键 Bug 修复** — KeyPool 空池 panic、竞态条件、敏感 Header 泄露、未鉴权端点等 23 项
- [x] **项目结构重构** — `internal/keypool/`、`internal/logstore/`、`internal/utils/` 子包拆分
- [x] **Docker 容器化** — 多阶段构建 `Dockerfile` + `.dockerignore` + `docker-compose.yml`
- [x] **压测与基准测试** — Go Benchmark（KeyPool + Proxy 三场景）+ Vegeta 压测脚本 + mock 上游
- [x] **压测基础设施验证** — Vegeta 安装、冒烟测试（50 QPS 100% 成功）、全量压测（500 QPS 执行通过）
- [x] **性能基线数据** — 50 QPS: p99 15.8ms, 100% 成功 / 500 QPS: 代理开始饱和（32% 成功, p99 34s）
- [x] **API Key 名称支持** — `key==name` 格式解析，名称在日志 / API 响应 / Dashboard 全链路展示（8 个新测试）
- [x] **管理 API 增强** — 5 个新端点（POST disable / PUT cooldown / DELETE {index} / GET stats / POST reload）+ KeyPool 边界检查 + LogStore 统计计数（7 个新测试）
- [x] **可观测性：Prometheus Metrics** — 4 个指标（requests_total / request_duration_seconds / keypool_keys / upstream_errors_total），`/metrics` 端点，proxyHandler 全埋点，自定义 Registry 隔离
- [x] **Metrics 验收测试** — 6 个集成验收测试，mock upstream 真实代理请求验证所有 4 个指标增量正确
- [x] **CLAUDE.md 测试策略对齐** — 明确 Testing Trophy 模型，集成验收测试为主力
- [x] **Error Handling 统一** — 4 个错误码（BAD_REQUEST / UPSTREAM_ERROR / ALL_KEYS_INVALID / EXHAUSTED_RETRIES），proxyHandler 错误响应统一 JSON 格式 `{"error":{"code":"...","message":"..."}}`，4 个集成验收测试覆盖所有错误路径
- [x] **两层熔断器（智能重试与退避 + 日限额自动检测）** — KeyCircuitBreaker（CLOSED/OPEN/PERMA 三态 + 指数退避）+ UpstreamCircuitBreaker（CLOSED/OPEN/HALF_OPEN 三态），4 个集成验收测试
- [x] **main.go 拆包** — 977 行 → 98 行，`internal/server/` 包 5 文件拆分（server.go / handlers.go / proxy.go / middleware.go / lifecycle.go）
- [x] **启动配置友好校验** — 中文错误消息 + 标准退出码（配置错 exit 2、运行时错 exit 1、系统错 exit 3）
- [x] **README 重写** — 覆盖熔断器 / Metrics / 管理 API / 配置校验 / 压测 / 测试策略，450+ 行完整文档
- [x] **Key 持久化存储** — `internal/keypool/store.go` 模块（LoadKeysFromFile/SaveKeysFromFile/LoadFullStore/SaveFullStore），管理 API 写操作自动同步 `keys.json`，重启恢复状态，3 个集成验收测试
- [x] **Docker Compose 完整部署** — 三服务架构（Alvus + Prometheus + Grafana），持久化数据卷，预置监控面板，内部网络隔离

### 127 测试覆盖

| 文件 | 测试数 | 类型 |
|------|--------|------|
| `internal/config/config_test.go` | 24 | 单元测试 |
| `internal/keypool/keypool_test.go` | 12 | 单元测试 |
| `internal/keypool/store_test.go` | 9 | 单元测试 |
| `internal/logstore/logstore_test.go` | 5 | 单元测试 |
| `internal/circuitbreaker/key_test.go` | 10 | 单元测试 |
| `internal/circuitbreaker/upstream_test.go` | 9 | 单元测试 |
| `handlers_test.go` | 16 | Handler 测试 |
| `logstore_test.go` | 4 | Handler 测试 |
| `proxy_test.go` | 28 | **集成验收测试** |
| `integration_test.go` | 4 | **集成测试** |
| `metrics_verification_test.go` | 6 | **集成验收测试** |
| **总计** | **127** | |

## 🔜 短期计划

### E. 优雅关闭（Graceful Shutdown）

**现状：** 收到 SIGINT/SIGTERM 直接 `os.Exit`，正在处理的请求被掐断。对流式响应（SSE）尤其不友好。

**建议：** 使用 `http.Server.Shutdown()` 替代直接退出：
- 收到退出信号 → 停止监听 → 等待活跃请求完成（30s 超时）→ 退出
- 配合 Docker 容器的 `STOP_TIMEOUT` 行为更安全
- 与子进程管理模式下的信号传播兼容

**估算：** ~30 分钟，零逻辑改动。

## ⚠️ 已知约束

- **ccswitch 领域不碰** — 格式化/整流/转发相关（`DISABLE_THINKING`、请求修改、响应变换、provider 路由）ccswitch 已成熟，不重复造轮

## P0 — 验证结果摘要

### 压测与基准测试 ✅

| 项目 | 结果 |
|------|------|
| `BenchmarkKeyPoolNext` | ✅ 稳定输出，~90 ns/op，0 allocs/op |
| `BenchmarkProxyRequest` | ✅ ~215µs/op，331KB/op |
| `BenchmarkProxyAllKeysCooldown` | ✅ ~2.5s/op（等待冷却正常） |
| `BenchmarkProxyFlakyUpstream` | ✅ ~425ms/op（重试正常） |
| Vegeta 冒烟测试（50 QPS, 10s） | ✅ 100% 成功，p99 15.8ms，均值 1.4ms |
| Vegeta 全量压测（500 QPS, 60s） | ⚠️ 32% 成功，p99 34s（i5 笔记本饱和，需生产级机器验证） |
| Vegeta 中等负载（200 QPS, 30s） | ⚠️ 1.6% 成功（大量超时，需要性能调优） |
| `go vet ./...` | ✅ 零警告 |
| `run-load-test.ps1` 脚本 | ✅ 已修复适配新 Vegeta CLI |

### Docker 容器化 ✅

| 项目 | 结果 |
|------|------|
| Dockerfile 多阶段构建语法 | ✅ CI 构建通过 |
| docker-compose.yml 语法 | ✅ `docker compose config` 解析通过 |
| .dockerignore 排除规则 | ✅ |
| CI Docker build 步骤 | ✅ 已在 go.yml 中配置 |
| WSL2 docker build | ⚠️ 中国网络限制，Docker Hub 不可达 |
| Dockerfile 简化 | ✅ 移除 gcr.io/distroless 依赖，改用 alpine:3.19 runtime |

### 说明

- Docker Hub 在中国网络无法直接拉取，Dockerfile 已在 GitHub Actions CI 中成功构建
- Dockerfile 已简化：3 阶段（tools + builder + distroless）→ 2 阶段（builder + alpine），消除 gcr.io 依赖
- WSL2 9p 文件系统不支持 inotify，容器内热重载不会触发（不影响裸跑）
- 高并发性能瓶颈（100+ QPS 开始饱和）属于优化范围，不影响功能正确性

## P1 — 应该做（功能完善）

### ccswitch 集成与对齐

**原因：** 项目定位已明确为 **"单 provider 内的 api key 轮转"**，与 ccswitch 互补。

- **请求路由标准化** — 确保代理的接口设计与 ccswitch 的 IETF RFC 兼容
- **多 provider 代理模式** — 检查 ccswitch 如何分发流量到各 provider，确保 Alvus 的 KeyPool 能平滑接入
- **动态 provider 配置 API** — `/providers` 端点管理多 upstream
- ❌ 不做 `DISABLE_THINKING` 等整流功能（ccswitch 领域）

### 日志系统进一步增强

- 日志级别动态调整（通过 API 或信号）
- Debug 级别可选请求/响应体日志（注意敏感数据清洗）
- 结构化字段命名标准化

### Error Handling 统一

- 所有代理错误统一 JSON 格式：`{"error": {"code": "...", "message": "..."}}`
- 上游超时、上游 5xx、Key 不可用等各场景定义错误码
- 客户端 SDK 直接解析

## P2 — 值得做（优化打磨）

### ~Docker Compose 完整部署~ ✅ 已完成

### 安全性增强

- Key 加密存储（非明文内存）
- 管理 API 返回 Key 统一脱敏（部分已实现）
- 可选从外部密钥管理服务读取 Key（Vault / AWS Secrets Manager）

### 上游健康检查

- 被动健康检查（根据请求失败率判定）
- 主动健康检查（定期 PING 上游端点）
- 不健康上游自动摘除，恢复后自动加入

### 优雅降级

- 无可用 Key 时重试队列（暂存请求等待 Key 恢复）
- 无可用上游时降级（友好错误提示）
- 半开状态（允许少量探测请求判断恢复）

## P3 — 锦上添花（长期愿景）

### Dashboard 增强

- 请求日志详情页
- Key 使用量统计图表
- 实时日志流（WebSocket）
- 配置管理界面

### CLI 管理工具

- `alvusctl` — 独立 CLI，通过管理 API 操作运行中代理
  - `alvusctl keys list / add / remove`
  - `alvusctl stats / reload`

### 插件 / 中间件系统

- 请求前/后处理钩子
- 自定义请求/响应修改
- 速率限制策略可配置

### 请求/响应预处理

- Header 过滤/注入（非透传场景）
- Stream 模式优化（SSE 流式响应处理）
- 响应格式转换

### 性能优化

- HTTP 连接池复用
- 请求 body 零拷贝转发（io.CopyN / splice）
- 响应缓存
- Key 选择策略可配置（round-robin / least-loaded / priority）

### CD / Release Pipeline

- GoReleaser 自动发布多平台二进制
- Docker 镜像自动构建推送到 ghcr.io
- Semantic Release + 自动 changelog
