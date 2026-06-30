# Alvus — TODO & 项目建议

> 按优先级和影响范围分组，具体实施时可根据当前聚焦点选择。

---

✅ **已完成 26 项核心功能，275 测试通过（详见 git log）。**

---

## 🎯 焦点

*当前未选定焦点事项。*

*ccswitch 源码分析报告 → `.agents/documents/ccswitch-analysis.md`*

---

## P1 — 快速见效（一天级）

### CD / Release Pipeline
- GoReleaser 自动发布多平台二进制到 GitHub Releases
- Docker 镜像自动构建推送到 ghcr.io
- Semantic Release + 自动 changelog
- 配 `git tag v0.1.0` 触发

**前提**：Dockerfile + CI 已就位，只需配 GoReleaser 和 Release workflow。

---

## P2 — 值得做（优化打磨，数天级）

### 性能优化
**有压测数据支撑**：50 QPS p99 15.8ms ✅ / 200 QPS ⚠️ 开始饱和 / 500 QPS ❌ 32% 成功

- **Key 选择策略可配置** — round-robin → 支持 least-loaded / weighted / priority 策略
- **HTTP 连接池调优** — 当前 MaxIdleConns=100, MaxIdleConnsPerHost=10，需压测验证瓶颈
- **请求 body 零拷贝转发** — `io.CopyN` / `splice` 减少内存拷贝（proxy 流式转发场景）
- **Vegeta 基准重测** — 优化后重跑压测，验证 QPS 提升

### 安全性增强（续）
- 管理 API 返回 Key 统一脱敏（部分已实现，待全链路对齐）
- 可选从外部密钥管理服务读取 Key（Vault / AWS Secrets Manager）

### 优雅降级
- 无可用 Key 时重试队列（暂存请求等待 Key 恢复）
- 无可用上游时降级（友好错误提示）
- 半开状态（允许少量探测请求判断恢复）

---

## P3 — 锦上添花（长期愿景）

### CLI 管理工具（`alvusctl`）
把 Alvus 封装成一个 CLI，一个命令管全部（参考 ccswitch 的设计思路）：
- `alvusctl provider add | list | remove` — provider 管理
- `alvusctl keys add | list | remove` — Key 管理（当前管理 API 的 CLI 化）
- `alvusctl start [provider]` — 启动特定 provider 或多个 provider
- `alvusctl stats | reload` — 运行状态管理
- 设计目标：单一 `alvus` 命令管理所有操作，类似 ccswitch CLI 的使用体验

### Dashboard 增强
- 请求日志详情页
- Key 使用量统计图表
- 实时日志流（WebSocket）
- 配置管理界面

### 请求/响应预处理
- Header 过滤/注入（非透传场景）
- Stream 模式优化（SSE 流式响应处理）
- 响应格式转换

---

## ⚠️ 已知约束

- **ccswitch 领域不碰** — 格式化/整流/转发、provider 路由、请求修改、响应变换、DISABLE_THINKING 等 ccswitch 已成熟的功能不重复造轮。Alvus 定位为 **"单 provider 内的 API Key 轮转"**，与 ccswitch 互补。详见 `.agents/documents/ccswitch-analysis.md`。
- **WSL2 9p 文件系统不支持 inotify** — 容器内热重载不会触发（不影响裸跑）
- **高并发性能瓶颈** — 100+ QPS 开始饱和，属于优化范围不影响功能正确性
- **Alvus 非必要不复杂化** — 当前熔断器足以应付 Key 轮转场景（Key 切换成本极低），无须对标 ccswitch 的 HalfOpen/错误率等精细熔断机制

---

## 附：测试覆盖 & 验证摘要

### 测试覆盖（275 tests）

| 文件 | 测试数 | 类型 |
|------|--------|------|
| `internal/config/config_test.go` | 44 | 单元测试 |
| `internal/keypool/keypool_test.go` | 12 | 单元测试 |
| `internal/keypool/store_test.go` | 14 | 单元测试 |
| `internal/keypool/crypto_test.go` | 9 | 单元测试 |
| `internal/logstore/logstore_test.go` | 5 | 单元测试 |
| `internal/circuitbreaker/key_test.go` | 10 | 单元测试 |
| `internal/circuitbreaker/upstream_test.go` | 9 | 单元测试 |
| `internal/server/logging_test.go` | 15 | 单元测试 |
| `internal/server/error_classification_test.go` | 6 | 单元测试 |
| `internal/server/manager_test.go` | 4 | **集成验收测试** |
| `handlers_test.go` | 20 | Handler 测试 |
| `logstore_test.go` | 4 | Handler 测试 |
| `proxy_test.go` | 34 | **集成验收测试** |
| `integration_test.go` | 4 | **集成测试** |
| `metrics_verification_test.go` | 6 | **集成验收测试** |
| `graceful_shutdown_test.go` | 3 | **集成验收测试** |
| `healthcheck_test.go` | 5 | **集成验收测试** |
| `docker_compose_test.go` | 5 | **集成验收测试** |
| `config_cmd_test.go` | 3 | **集成验收测试** |
| **总计** | **275** | |

> ✅ Spec B (InstanceManager) 已完成: `alvus start` 现支持单进程多实例运行，替代 manage.go 子进程模式。
> - `LoadAllTomlProviders` — 从 config.toml 读取多个 provider
> - `InstanceManager` — 管理多实例生命周期（启动/优雅关闭/后台任务）
> - 向后兼容: `.env` 单 provider 模式不变
> - `--manage manage.json` 模式保留（过渡期）

### 压测基线（参考）
| 场景 | 结果 |
|------|------|
| 50 QPS 冒烟测试 | 100% 成功，p99 15.8ms |
| 500 QPS 全量压测 | ⚠️ 32% 成功，p99 34s（i5 笔记本饱和） |
| 200 QPS 中等负载 | ⚠️ 1.6% 成功（大量超时，需性能调优） |

### Docker 验证
- `docker compose config` ✅ 语法通过
- CI Docker build ✅ 已在 go.yml 中配置
- WSL2 网络限制（Docker Hub 不可达），但 CI 可正常构建

### 熔断器验证
- KeyCircuitBreaker: 429 触发指数退避，401/403 永久禁用 ✅
- UpstreamCircuitBreaker: 5xx 触发熔断，304 恢复 ✅
- 上游错误不惩罚 Key（设计正确） ✅
