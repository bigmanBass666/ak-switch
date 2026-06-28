# Alvus — TODO & 项目建议

> 基于对项目现有架构、代码和目标的综合分析，整理出的后续可做事项。
> 按优先级和影响范围分组，具体实施时可根据当前聚焦点选择。

## ✅ 已完成（当前分支已含）

- [x] **配置管理增强** — `internal/config` 包，含 `Validate()`/`Diff()`/`Sanitized()`，启动校验 + 热重载校验 + diff 日志
- [x] **配置管理集成测试** — `integration_test.go`（启动校验 / 热重载 diff / 热重载回滚 3 个场景）
- [x] **日志系统迁移为结构化日志** — 全部 `log.Printf` → `log/slog`，分级 Info/Warn/Error
- [x] **配置变更 diff 日志** — 热重载时记录变更字段（Key 脱敏）
- [x] **关键 Bug 修复** — KeyPool 空池 panic、竞态条件、敏感 Header 泄露、未鉴权端点等 23 项
- [x] **项目结构重构** — `internal/keypool/`、`internal/logstore/`、`internal/utils/` 子包拆分
- [x] **Docker 容器化** — 多阶段构建 `Dockerfile` + `.dockerignore` + `docker-compose.yml`
- [x] **压测与基准测试** — Go Benchmark（KeyPool + Proxy 三场景）+ Vegeta 压测脚本 + mock 上游
- [x] **压测基础设施验证** — Vegeta 安装、冒烟测试（50 QPS 100% 成功）、全量压测（500 QPS 执行通过）、`run-load-test.ps1` 脚本修复
- [x] **性能基线数据** — 50 QPS: p99 15.8ms, 100% 成功 / 500 QPS: 代理开始饱和（32% 成功, p99 34s）

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
- **`DISABLE_THINKING` 支持** — 以通用 `x-provider-options: disable-thinking=key` 头实现（非 OpenAI 特定）
- **多 provider 代理模式** — 检查 ccswitch 如何分发流量到各 provider，确保 Alvus 的 KeyPool 能平滑接入
- **动态 provider 配置 API** — `/providers` 端点管理多 upstream

### 管理 API 增强

当前 `/keys`、`/health`、`/clear`、`/logs` 已提供基础功能。可以扩展：

- `POST /keys` — 添加单个 Key
- `DELETE /keys/:index` — 删除指定 Key
- `POST /keys/:index/disable` — 手动禁用 Key
- `PUT /keys/:index/cooldown` — 手动触发 Key 冷却
- `GET /stats` — 代理运行时统计（请求量、成功率、平均延迟）
- `POST /reload` — 手动触发配置热重载
- API 响应标准化（统一 JSON 格式、错误码）

### 可观测性：Metrics + Tracing

- `/metrics` 端点暴露 Prometheus 指标：
  - `alvus_requests_total{method, status, key_index}`
  - `alvus_request_duration_seconds{method, status}`
  - `alvus_keypool_active_keys` / `alvus_keypool_cooldown_keys`
- 可选 OpenTelemetry tracing

### 日志系统进一步增强

- 日志级别动态调整（通过 API 或信号）
- Debug 级别可选请求/响应体日志（注意敏感数据清洗）
- 结构化字段命名标准化

## P2 — 值得做（优化打磨）

### Docker Compose 完整部署

- （可选）Prometheus + Grafana 监控面板
- （可选）Dashboard 独立服务
- 持久化数据卷（日志、配置）
- 网络配置（内部通信、外部暴露）

### Error Handling 统一

- 所有代理错误统一 JSON 格式：`{"error": {"code": "...", "message": "..."}}`
- 上游超时、上游 5xx、Key 不可用等各场景定义错误码
- 客户端 SDK 直接解析

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

## 你关心的方向

1. **先验证压测和 Docker** — 已有的工作成果需要落地（验证→提交）
2. **ccswitch 集成** — 按项目定位补齐协作接口
3. **按优先级做** — 从 P1 开始逐个击破
4. **挑感兴趣的先做** — 每个项目都有独立价值
5. **告诉我聚焦方向** — 我帮你拆成可执行的 spec + tasks

---

- 给 api key 添加名称支持,现在的key都没有名称, 就很难分辨.
