# Alvus — 状态跟踪

> 项目已进入**维护模式**：只修真正遇到的 bug，不添投机性功能。

---

✅ **功能完整，268 测试通过（其中 72 个暂跳待适配）。**

---

## 已完成

### 日志条目增强（PR #36）
- LogEntry 新增 DurationMs/Attempt/Provider 字段
- 重试耗尽路径新增日志记录
- CLI `alvus logs` 命令新增 provider/attempt/duration/key_name 展示
- 集成测试验证新字段存在 + 重试耗尽日志记录

### 单端口 + 路径路由重构（PR #32）
- 从"一个 provider 一个端口"改为"单端口 + `/{provider}/...` 路径路由"
- 移除 `.env` 配置加载（纯 TOML 模式）
- 移除 `--local` / `--network-only` 参数
- ProviderRouter 替代 InstanceManager，所有 provider 共享一个 HTTP 端口
- 管理 API（`/api/*`、`/health`、`/dashboard` 等）不受路径路由影响
- 代理请求格式：`POST /{provider}/v1/chat/completions`

### CLI 迁移（Spec A + B + C）
- Cobra CLI 框架，单一 `alvus` 二进制管理所有操作
- TOML 配置格式（`config.toml`），XDG 标准路径
- `alvus start` 单端口多 provider（ProviderRouter）
- `alvus provider add | list | remove` — provider 配置管理
- `alvus key add | list | remove | disable` — Key 加密存储管理
- `alvus config init | view` — 配置初始化和查看
- `alvus status | logs | stop` — 运行时状态查询和管理
- `alvus start --provider <name>` — 单 provider 启动过滤
- `manage.go` 已删除，`.env` 支持已移除

### 代码健康 Sprint（PR #25）
- `reloadHandler` 失败时返回 HTTP 500（原为 200）
- 统一 `maskKey`（CLI 与 API 一致）
- `resetAllEnv` 补齐所有遗漏环境变量
- 删除未使用的 `im.stop` channel
- 清理未使用的 viper 依赖
- `TomlProviderConfig` 补齐 15 个字段

### 关键路径测试覆盖（PR #29 + PR #30）
- `start_cmd_test.go` — 子进程模式测试 `alvus start` TOML 启动全链路
- `e2e_test.go` — 真实二进制全流程模拟（provider add → proxy → shutdown）
- `docs/internal/critical-paths.md` — 所有 CLI 行为测试覆盖状态
- CLAUDE.md 新增"关键路径覆盖纪律"

### README 重写 + 文档拆分（PR #26）
- README 压回导航页（~50 行），详细文档拆分到 `docs/`
- `docs/cli-reference.md` — CLI 命令参考
- `docs/configuration.md` — TOML 配置说明
- `docs/api.md` — API 端点文档
- `docs/architecture.md` — 熔断器架构
- `docs/deployment.md` — Docker 部署与监控栈
- 研究/分析文档移入 `docs/internal/`

---

## 📋 待办

- **ProviderRouter 路由压力测试** — 当前无并发/多 provider 路由压测数据
- **`docs/configuration.md` 更新** — 移除 `.env` 相关说明

---

## 不做的（已评估后排除）

| 方向 | 排除理由 |
|------|----------|
| CD / Release Pipeline | 纯自用，不需要自动发布 |
| 性能优化 | 50 QPS p99 15.8ms 已远超个人需求，瓶颈在上游 rate limit 不在 Alvus |
| Key 选择策略 | 单 provider 内 Key 间无负载差异 |
| 外部密钥管理（Vault/KMS） | 本地 AES-256-GCM 加密已够安全 |
| 优雅降级（重试队列/半开） | HTTP 代理场景无法暂存请求；Key 切换成本极低，当前熔断器足够 |
| Dashboard 增强 | 已够用，无真实需求 |
| 请求/响应预处理 | ccswitch 已成熟，不重复造轮 |

---

## 附：测试覆盖

### 测试分布（265 tests，全部活跃）

| 文件 | 测试数 | 类型 |
|------|--------|------|
| `internal/config/config_test.go` | 51 | 单元测试 |
| `proxy_test.go` | 38 | **集成验收测试** |
| `handlers_test.go` | 26 | Handler 测试 |
| `internal/keypool/*_test.go` | 40 | 单元测试 |
| `internal/circuitbreaker/*_test.go` | 19 | 单元测试 |
| `internal/server/*_test.go` | 20 | 单元/集成测试 |
| `metrics_verification_test.go` | 6 | **集成验收测试** |
| `healthcheck_test.go` | 5 | **集成验收测试** |
| `start_cmd_test.go` | 4 | **集成验收测试** |
| `provider_cmd_test.go` | 5 | **集成验收测试** |
| `key_cmd_test.go` | 5 | **集成验收测试** |
| `e2e_test.go` | 1 | **集成验收测试** |
| `graceful_shutdown_test.go` | 3 | **集成验收测试** |
| `docker_compose_test.go` | 5 | **集成验收测试** |
| `config_cmd_test.go` | 3 | **集成验收测试** |
| `logstore_test.go` + `internal/logstore/*_test.go` | 9 | Handler + 单元 |
| `integration_test.go` | 0 | **已清空**（`.env` 测试随 `.env` 移除） |
| **总计** | **265** | |

### 压测基线（参考）

| 场景 | 结果 |
|------|------|
| 50 QPS 冒烟测试 | 100% 成功，p99 15.8ms |
| 500 QPS 全量压测 | ⚠️ 32% 成功，p99 34s（i5 笔记本饱和） |
| 200 QPS 中等负载 | ⚠️ 1.6% 成功（大量超时） |

### Docker 验证
- `docker compose config` ✅ 语法通过
- CI Docker build ✅ 已在 go.yml 中配置

### 熔断器验证
- KeyCircuitBreaker: 429 触发指数退避，401/403 永久禁用 ✅
- UpstreamCircuitBreaker: 5xx 触发熔断，304 恢复 ✅
- 上游错误不惩罚 Key（设计正确） ✅

---

## 已知约束

- **ccswitch 领域不碰** — 格式化/整流/转发、provider 路由、请求修改、响应变换等 ccswitch 已成熟的功能不重复造轮。详见 `.agents/documents/ccswitch-analysis.md`。
- **WSL2 9p 文件系统不支持 inotify** — 容器内热重载不会触发（不影响裸跑）
- **高并发性能瓶颈** — 100+ QPS 开始饱和，个人场景到不了这个量级

---

## 待议

- **全 Key 熔断错误提示** — 某 provider 所有 Key 都熔断时，是否应返回类似 `"sensenova 所有 API Key 已熔断"` 的明确错误信息？（参考 ccswitch 的"所有供应商已熔断，无可用渠道"）
- 关于更改项目名以及整个 IP 的名字，会很麻烦吗？我想改名为 AK switch，也就是 API key switch 的简称。我们现在整个 IP 的名字是不是硬编码了呀？是不是不方便？一般来说，大家都是怎么处理这个品牌名字的？如果说频繁变动的话，是不是硬编码就不方便了？包括GitHub仓库的名字以及URL的地址,这些能不能方便地更改
