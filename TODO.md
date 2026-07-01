# Alvus — 状态跟踪

> 项目已进入**维护模式**：只修真正遇到的 bug，不添投机性功能。

---

✅ **核心目标已达成 — 功能完整，278 测试通过。**

---

## 已完成

### CLI 迁移（Spec A + B + C）
- Cobra CLI 框架，单一 `alvus` 二进制管理所有操作
- TOML 配置格式（`config.toml`），XDG 标准路径，向后兼容 `.env`
- `alvus start` 单进程多实例（InstanceManager），替代 manage.go 子进程模式
- `alvus provider add | list | remove` — provider 配置管理
- `alvus key add | list | remove | disable` — Key 加密存储管理
- `alvus config init | view` — 配置初始化和查看
- `alvus status | logs | stop` — 运行时状态查询和管理
- `manage.go` 已删除

### 代码健康 Sprint
- `reloadHandler` 失败时返回 HTTP 500（原为 200）
- 统一 `maskKey`（CLI 与 API 一致）
- `resetAllEnv` 补齐所有遗漏环境变量
- 删除未使用的 `im.stop` channel
- 清理未使用的 viper 依赖
- `TomlProviderConfig` 补齐 15 个字段

### README 重写 + 文档拆分
- README 压回导航页（~50 行），详细文档拆分到 `docs/`
- `docs/cli-reference.md` — CLI 命令参考
- `docs/configuration.md` — TOML + .env 配置说明
- `docs/api.md` — API 端点文档
- `docs/architecture.md` — 熔断器架构
- `docs/deployment.md` — Docker 部署与监控栈
- 研究/分析文档移入 `docs/internal/`

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

### 测试分布（278 tests）

| 文件 | 测试数 | 类型 |
|------|--------|------|
| `internal/config/config_test.go` | 51 | 单元测试 |
| `proxy_test.go` | 35 | **集成验收测试** |
| `handlers_test.go` | 26 | Handler 测试 |
| `internal/keypool/*_test.go` | 40 | 单元测试 |
| `internal/circuitbreaker/*_test.go` | 19 | 单元测试 |
| `internal/server/*_test.go` | 20 | 单元/集成测试 |
| `metrics_verification_test.go` | 6 | **集成验收测试** |
| `provider_cmd_test.go` | 5 | **集成验收测试** |
| `key_cmd_test.go` | 5 | **集成验收测试** |
| `healthcheck_test.go` | 5 | **集成验收测试** |
| `docker_compose_test.go` | 5 | **集成验收测试** |
| `logstore_test.go` + `internal/logstore/*_test.go` | 9 | Handler + 单元 |
| `integration_test.go` | 4 | **集成测试** |
| `graceful_shutdown_test.go` | 3 | **集成验收测试** |
| `config_cmd_test.go` | 3 | **集成验收测试** |
| **总计** | **245+（含子测试 278）** | |

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

- 某个provider的所有apikey都熔断之后预期应返回错误信息: "sensenova 所有api key已熔断"之类的话, 你觉得呢? 就像ccswitch在开启故障转移之后, 在所有供应商都熔断时返回错误信息: "所有供应商已熔断, 无可用渠道", 你可以派几个子代理去看一下他源码, 看他是怎么处理这个问题的
- cli有-h参数吗?
- 探讨所有向后兼容存在的合理性
