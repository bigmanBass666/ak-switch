# akswitch 设计原则审查报告

审查日期：2026-07-07
审查范围：akswitch 项目全部 Go 源文件（cmd/、internal/ 包）
审查层次：模块级 + 架构级

---

## 1. 违反原则汇总

| 原则 | 状态 | 严重度 | 违反数 |
|------|------|--------|--------|
| SRP - 单一职责 | 违反 | 高 | 4 |
| DRY - 不重复 | 违反 | 高 | 3 |
| OCP - 开闭原则 | 部分违反 | 中 | 3 |
| SoC - 关注点分离 | 违反 | 中 | 2 |
| LoD - 迪米特法则 | 违反 | 低 | 1 |
| YAGNI - 不需要的不要 | 部分违反 | 低 | 1 |
| DIP - 依赖倒置 | 基本满足 | - | - |
| KISS - 保持简单 | 基本满足 | - | - |

---

## 2. 逐条诊断

---

### 2.1 SRP - 单一职责原则

**定义**：一个模块、类或函数应该只有一个被修改的理由。

#### 2.1.1 `startServer` 函数承担过多职责

- **位置**: `internal/cmd/start.go:37-185`
- **问题**: 该函数在 149 行内处理了以下完全不同的职责：
  1. 崩溃恢复 (defer CrashRecover)
  2. PID 文件检查与冲突检测
  3. 配置源检测（XDG 路径）
  4. Provider 从 TOML 加载
  5. Provider 选择逻辑（`--provider` / `--all` / `default_provider` / 第一个字母序）
  6. Key 加载（文件、加密存储、回退）
  7. 配置验证
  8. Key Pool 创建
  9. Provider 注册到 Router
  10. 文件日志初始化
  11. 写入 PID 文件
  12. 启动后台任务
  13. Signal 监听（SIGINT/SIGTERM）
  14. 优雅关闭

  超过 10 个修改理由，这是本项目最严重的 SRP 违反。

- **建议**: 拆分为以下独立函数：
  - `setupConfig() (*config.TomlConfig, error)` — 配置加载与检测
  - `resolveProviders(tc *config.TomlConfig, filter string, startAll bool) map[string]*config.Config` — provider 选择
  - `initProviderState(name string, cfg *config.Config, router) error` — 单个 provider 初始化
  - `startHTTPServer(router) (net.Listener, error)` — 服务器启动
  - `managePIDFile() (func(), error)` — PID 文件写入 + 清理函数
  - `waitSignalAndShutdown(router)` — 信号等待 + 关闭

#### 2.1.2 `executeProxy` 函数过长

- **位置**: `internal/server/handlers.go:112-294`
- **问题**: 182 行的核心代理函数处理了以下职责：
  1. Request body 读取与大小校验（10MB 限制）
  2. URL 路由（/genai/ 路径 vs 其他）
  3. Auth header 脱敏日志
  4. 重试循环
  5. Key 选择（next from pool）
  6. Circuit breaker 状态检查（per-key + upstream）
  7. HTTP 请求构造
  8. 7 路响应状态分发
  9. 指标记录
  10. 日志记录

  虽然有部分辅助函数被提取（handleRateLimited, handleAuthRejected 等），但主函数仍过于复杂。

- **建议**: 
  - 将 URL 构建提取为 `buildTargetURL(cfg *config.Config, r *http.Request) string`
  - 将重试循环体提取为 `func attemptProxy(ps *ProviderState, ...) (bool, error)`
  - 将 body 处理提取为 `func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool)`

#### 2.1.3 `ProviderRouter` 结构体职责过多

- **位置**: `internal/server/manager.go:33-47`
- **问题**: ProviderRouter 同时负责：
  1. HTTP 服务器生命周期管理（Start, Shutdown, Stop）
  2. Provider 路由（extractProvider, resolveProvider, lookupProvider）
  3. Handler 注册（registerRoutes）
  4. 后台任务管理（StartBackgroundTasks）
  5. 全局日志存储（logs 字段）
  6. 全局指标（metrics, metricsRegistry）
  7. 综合 mux 构造（Handler + sync.Once）

  修改理由举例：更换 mux 实现、添加新类型后台任务、变更日志策略都需要修改 ProviderRouter。

- **建议**: 将以下关注点分离：
  - `ServerLifecycle` 接口：Start / Shutdown / Stop
  - `RouteRegistry`：handler 注册与路由（现有 registerRoutes 的进一步抽象）
  - `BackgroundScheduler`：后台任务编排

#### 2.1.4 `Config` 结构体字段过多

- **位置**: `internal/config/config.go:23-51`
- **问题**: Config 结构体包含 20+ 个字段，涵盖截然不同的配置域：
  - 网络：Port
  - 上游：TargetBase, GenaiBase
  - Key 管理：Keys, KeyNames, KeysFile, EncryptionKey
  - 退避策略：BackoffCapSec, BackoffMultiplier
  - 熔断器：CBResetSec, UpstreamCBThreshold
  - 健康检查：HealthCheckIntervalSec, HealthCheckPath, HealthCheckTimeoutSec
  - 日志：LogLevel, LogFile, LogMaxSize, LogMaxAge
  - 重试与超时：MaxRetries, CooldownSec, HTTPTimeoutSec

  虽然结构体作为配置 DTO 本身合理，但 Validate() 方法能感知所有域，Diff() 包含所有字段的比较逻辑，都在同一个文件中。

- **建议**: 不拆分结构体本身（保持配置扁平化），但将 Validate 按域拆分为子验证函数：`validateNetwork()`, `validateBackoff()`, `validateHealthCheck()`, `validateLogging()`。

---

### 2.2 DRY - 不重复原则

**定义**：同一逻辑不应该在多个地方出现。

#### 2.2.1 Key 加载逻辑重复

- **位置**: 
  - `internal/server/handlers.go:807-828` (`loadKeysFromConfig`)
  - `internal/cmd/start.go:188-216` (`loadKeysForProvider`)
- **问题**: 两个函数实现几乎相同的逻辑，仅用在不同的包中：
  1. 尝试从自定义 KeysFile 加载
  2. 回退到标准加密存储路径（`<XDG>/keys/<name>.enc`）
  
  两者都接收 provider name 和 config，返回 keys, names。

- **建议**: 将重复逻辑提取到 `internal/keypool` 包中的共享函数：
  ```go
  func LoadProviderKeys(name string, cfg *config.Config) (keys, names []string, err error)
  ```

#### 2.2.2 Port 检测逻辑重复

- **位置**: 
  - `internal/cmd/status.go:29-33`
  - `internal/cmd/logs.go:28-32`
  - `internal/cmd/reload.go:14-19`
- **问题**: 三个 CLI 命令使用完全相同的 port 检测模式：
  ```go
  port := adminPort
  if xdgPath, err := config.XDGConfigPath(); err == nil {
      if p := config.FindServerPort(xdgPath); p > 0 {
          port = p
      }
  }
  ```
  这段代码出现了 3 次。如果 port 检测逻辑变化（如环境变量覆盖），需要修改 3 处。

- **建议**: 提取为共享助手函数：
  ```go
  func detectServerPort() int {
      // 优先级: 环境变量 > TOML > 默认
      if xdgPath, err := config.XDGConfigPath(); err == nil {
          if p := config.FindServerPort(xdgPath); p > 0 {
              return p
          }
      }
      return adminPort // 8080
  }
  ```

#### 2.2.3 环境变量清理列表重复

- **位置**: 
  - `internal/cmd/testhelper.go:23-31` (ResetConfigEnv)
  - `internal/config/config_test.go:15-28` (resetEnv)
- **问题**: 测试环境变量清理列表在两个地方维护。如果新增或删除配置 env var，需要同步修改两处，容易遗漏。

- **建议**: 在 `internal/config` 包中定义导出的变量列表：
  ```go
  // config.go 中
  var ConfigEnvVars = []string{"PORT", "TARGET_BASE_URL", "GENAI_BASE_URL", ...}
  ```
  测试文件引用此列表，避免重复。

---

### 2.3 OCP - 开闭原则

**定义**：对扩展开放，对修改封闭。

#### 2.3.1 新增配置字段需要修改多处

- **位置**:
  - `internal/config/config.go:23-51` (Config struct)
  - `internal/config/config.go:276-296` (TomlProviderConfig struct)
  - `internal/config/config.go:472-528` (tomlToConfig)
  - `internal/config/config.go:531-558` (configToToml)
  - `internal/config/config.go:92-130` (Validate)
  - `internal/config/config.go:156-176` (configDiffFields)
  - `internal/config/config.go:70-88` (DefaultConfig)
- **问题**: 新增一个配置字段需要修改至少 5 处代码，这是典型的霰弹式修改（Shotgun Surgery）。

- **建议**: 保持当前的手动映射模式是务实的（Go 的 struct 映射没有泛型反射之外的通用方案），但应增加 roundtrip 测试确保所有新字段在 tomlToConfig → configToToml 中一致。也可以考虑使用 `mapstructure` 或类似库减少样板代码。

#### 2.3.2 响应状态分发的 switch 不可扩展

- **位置**: `internal/server/handlers.go:244-275`
- **问题**: `executeProxy` 中的 switch 语句硬编码了 7 种响应状态分类。如果需要增加新的状态码处理，需要修改此 switch。

- **建议**: 引入策略模式：
  ```go
  type StatusHandler func(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, ...) (handled bool)
  
  var statusHandlers = map[int]StatusHandler{
      http.StatusTooManyRequests: handleRateLimited,
      http.StatusUnauthorized:    handleAuthRejected,
      http.StatusForbidden:       handleAuthRejected,
      http.StatusBadGateway:      handleServerError,
      http.StatusServiceUnavailable: handleServerError,
  }
  ```
  当前代码已将 handler 函数提取，但 switch 本身仍不封闭。

#### 2.3.3 `categorizeError` 硬编码状态码

- **位置**: `internal/server/proxy.go:33-46`
- **问题**: `CatNonRetryable` 的 HTTP 状态码列表（400, 405, 406, 413, 414, 415, 422, 501）是硬编码的。添加新的非重试状态码需要修改此函数。

- **建议**: 将非可重试状态码定义为包级变量：
  ```go
  var nonRetryableCodes = map[int]bool{
      400: true, 405: true, 406: true, 413: true,
      414: true, 415: true, 422: true, 501: true,
  }
  ```
  但当前列表已经稳定，保留 switch 也符合 KISS 原则。建议仅在确定状态码会变化时重构。

---

### 2.4 SoC - 关注点分离

**定义**：不同关注点应分离到不同的模块或层中。

#### 2.4.1 `config.go` 混合多种关注点

- **位置**: `internal/config/config.go` (574 行)
- **问题**: 单个文件混合了：
  - 配置数据结构定义（Config, TomlConfig, TomlProviderConfig）
  - 配置验证逻辑（Validate, ConfigError）
  - 差异分析（Diff, configDiffFields, ConfigChange）
  - TOML 序列化（LoadToml, SaveToml, LoadTomlConfig, SaveTomlConfig）
  - 文件 I/O（XDGConfigPath, DetectConfigSource）
  - 多 provider 加载（LoadAllTomlProviders, FindServerPort）

- **建议**: 拆分为多个文件（仍在同一包中）：
  - `config.go` — Config struct + 基本方法（Sanitized, DefaultConfig）
  - `validation.go` — Validate + ConfigError
  - `diff.go` — Diff + ConfigChange + fieldDef
  - `toml.go` — 全部 TOML 相关函数（LoadToml, SaveToml, LoadTomlConfig, SaveTomlConfig, LoadAllTomlProviders, tomlToConfig, configToToml, TomlProviderConfig, TomlConfig）
  - `paths.go` — XDGConfigPath, DetectConfigSource, FindServerPort

#### 2.4.2 `handlers.go` 文件过大

- **位置**: `internal/server/handlers.go` (922 行)
- **问题**: 该文件包含了代理逻辑、所有 API 处理器、辅助函数。922 行使得导航和理解困难。

- **建议**: 拆分为：
  - `proxy.go` — 将 `executeProxy` 从 handlers.go 移入（已有 proxy.go 但内容较少）
  - `handlers_health.go` — healthHandler, statsHandler
  - `handlers_keys.go` — keysHandler, disableKeyHandler, enableKeyHandler, cooldownKeyHandler, deleteKeyHandler
  - `handlers_config.go` — configHandler, reloadHandler
  - `handlers_admin.go` — logLevelHandler, clearHandler
  - `handlers_helpers.go` — buildLogEntry, recordProxyMetrics, streamResponse, loadKeysFromConfig

---

### 2.5 LoD - 迪米特法则

**定义**：一个对象应该只与其直接朋友通信，不通过链式调用访问深层对象。

#### 2.5.1 ProviderState 链式字段访问

- **位置**: 多个文件
  - `internal/server/handlers.go:114-117`: `ps.Proxy.client`, `ps.Proxy.keyCBs`, `ps.Proxy.upCB`
  - `internal/server/handlers.go:366`: `ps.Proxy.upCB.RecordFailure()`
  - `internal/server/server.go:160-172`: `s.pool.Keys()`, `s.pool.Name(i)`, `s.pool.IsDisabled(i)`

- **问题**: `ProviderState` 暴露了 `Proxy`、`Pool`、`Config` 内部结构供外部直接访问。调用者需要了解 ProviderState 的内部结构才能完成操作。例如 `ps.Proxy.upCB.RecordFailure()` 需要知道 `Proxy` 内部有 `upCB` 字段。

- **建议**: 在 `ProviderState` 上添加外观方法：
  ```go
  func (ps *ProviderState) RecordUpstreamFailure() { ps.Proxy.upCB.RecordFailure() }
  func (ps *ProviderState) KeyCB(idx int) *circuitbreaker.KeyCircuitBreaker { return ps.Proxy.keyCBs[idx] }
  func (ps *ProviderState) UpstreamCB() *circuitbreaker.UpstreamCircuitBreaker { return ps.Proxy.upCB }
  func (ps *ProviderState) HTTPClient() *http.Client { return ps.Proxy.client }
  ```
  当前不违反严重，但如果 ProviderState 的结构继续复杂化，建议早做重构。

---

### 2.6 YAGNI - 不需要的不要

**定义**：不要为"以后可能用到"的功能写代码。

#### 2.6.1 `LogStore.CountByStatus` 方法未使用

- **位置**: `internal/logstore/logstore.go:60-70`
- **问题**: `CountByStatus` 方法在源码中没有任何调用（仅在测试中）。它接收一个 `func(int) bool` 谓词，在当前代码库中没有需求。

- **建议**: 删除未使用的 `CountByStatus` 方法。如果将来需要，从实际调用处推导出确切签名再添加。

---

### 2.7 满足标准的原则

#### DIP - 依赖倒置原则 | 状态：满足

**满足表现**:
- `KeyPool` 通过明确的方法集暴露操作，`ProviderRouter` 通过 `Pool` 字段持有，依赖抽象而非具体类型。
- `CircuitBreaker`（`KeyCircuitBreaker`、`UpstreamCircuitBreaker`）是独立的数据类型，通过方法操作状态，不依赖外部框架。
- `LogStore` 通过 `utils.LogEntry` 结构体抽象日志条目，与具体的日志格式解耦。
- `Metrics` 通过 Prometheus 的 `CounterVec`、`GaugeVec` 等抽象，不暴露底层 registry 细节。

**注意点**:
- `NewProxyEngine` 直接创建 `http.Client` 和 `CircuitBreaker` 实例，没有从外部注入。简单场景下可接受，但测试时无法替换 HTTP 客户端或熔断器。
- 建议为 `NewProxyEngine` 增加可选的注入参数或 setter，方便测试。

#### KISS - 保持简单原则 | 状态：满足

**满足表现**:
- 加密回退机制（无加密 key 时用 base64 pass-through）简单实用，不引入复杂的加密编排。
- `multiHandler` 模式干净，不引入第三方 log 框架，同时实现双输出。
- 双熔断器（per-key 指数退避 + upstream 三态）的复杂度与领域需求匹配。
- 基于路径的 provider 路由（`/{provider}/...`）简单直观。
- 配置使用 TOML 格式，减少依赖，viper 仅用于辅助功能。

---

## 3. 架构亮点

尽管存在上述问题，项目中有一些值得肯定的设计：

1. **`configDiffFields` 注册表模式** (config.go:156-176): 新增 diff 字段无需修改 `Diff()` 函数，符合 OCP。这是项目中 OCP 做得最好的地方。

2. **Handler 拆分**: `executeProxy` 中已将响应处理提取为独立函数（`handleRateLimited`、`handleAuthRejected`、`handleServerError`、`handleNonRetryable`、`handleSuccess`），是良好的重构方向，每个函数职责单一。

3. **Config.Sanitized() 浅拷贝 + 脱敏** (config.go:135-145): 安全地返回配置脱敏副本，`Keys` 被 MaskKey 处理，`EncryptionKey` 被清除，避免敏感信息泄露。

4. **熔断器分离**: `KeyCircuitBreaker`（指数退避 + 永久禁用）和 `UpstreamCircuitBreaker`（三态：CLOSED/OPEN/HALF_OPEN）职责明确，互不干扰。

5. **sync.Once 缓存 mux** (manager.go:132-143): mux 只构建一次，线程安全，符合 Go 的最佳实践。

6. **加密回退**: 无加密 key 时自动降级为 base64，避免启动失败，兼顾安全与易用。

7. **测试分层**: 使用 `//go:build` 标签区分 unit / integration / e2e 测试，符合标准的 Go 测试实践。

---

## 4. 优先修复建议（按优先级排序）

### [高] 修复 Key 加载逻辑重复（DRY）
- **位置**: `handlers.go:loadKeysFromConfig` 和 `start.go:loadKeysForProvider`
- **预期收益**: 消除 30 行重复代码，新增 key 加载路径时只需修改一处
- **改动量**: 小（提取共享函数，两个调用点替换）

### [高] 拆分 `startServer` 函数（SRP）
- **位置**: `internal/cmd/start.go:37-185`
- **预期收益**: 函数从 149 行降至每段 20-30 行，每段可独立单元测试
- **改动量**: 中（提取 5-6 个函数，重构选择逻辑）

### [中] 拆分 `handlers.go` 文件（SoC）
- **位置**: `internal/server/handlers.go`
- **预期收益**: 拆分后每个文件 < 300 行，职责明确，便于新成员理解
- **改动量**: 中（纯文件拆分，不涉及逻辑变更）

### [中] 消除重复的 port 检测模式（DRY）
- **位置**: `status.go` / `logs.go` / `reload.go`
- **预期收益**: 消除 3 处重复，新增 CLI 命令时无需复制
- **改动量**: 小（提取为 `detectServerPort()` 共享函数）

### [低] 减少 ProviderState 链式访问（LoD）
- **位置**: `handlers.go` / `manager.go`
- **预期收益**: 降低 ProviderState 使用者的认知负担，减少内部结构变更的影响范围
- **改动量**: 小（添加外观方法，替换调用点）

### [低] 删除 `CountByStatus`（YAGNI）
- **位置**: `internal/logstore/logstore.go:60-70`
- **预期收益**: 消除 11 行未使用代码
- **改动量**: 极小

---

## 5. 原则冲突分析

部分原则之间存在冲突，本报告已做取舍：

| 冲突 | 取舍 | 理由 |
|------|------|------|
| OCP vs KISS（配置字段映射） | 保留手动映射 | Go 缺乏泛型反射，过度抽象增加复杂度，TL 不变的话多次改一处可接受 |
| SRP vs 性能（executeProxy 拆分） | 倾向于 SRP | 函数调用开销可忽略，可读性收益显著 |
| OCP vs KISS（categorizeError） | 保留 switch | 当前列表稳定，拆出 map 不带来实际收益 |
| ISP vs DRY（接口拆分） | 不拆分 | 当前无接口复用场景，提前拆分增加复杂度 |