# 设计原则审查报告

## 概述

Alvus 是一个聚焦于 API Key 轮转的反向代理服务，整体设计精良、职责划分清晰，属于小而美的垂直领域工具。核心架构可分为配置加载、KeyPool 管理、代理转发、熔断保护、监控指标五个模块。主要设计缺陷集中在 `ServerState` 承担过多职责（God Object）和配置字段增长导致的"霰弹式修改"问题。

---

## 发现的问题（按严重程度排序）

---

### 🔴 严重问题

#### 1. ServerState — God Object 反模式

**类型**: 违反 SRP（单一职责原则）  
**位置**: `internal/server/server.go:25-41`  
**严重度**: 🔴 高

`ServerState` 是一个典型的上帝对象，同时承担了至少 5 个独立的职责：

| 职责 | 字段 | 影响范围 |
|------|------|---------|
| 配置管理 | `cfg` | 所有 handler 需要读配置 |
| API Key 状态 | `pool` | 路由转发、管理 API |
| HTTP 路由 | `mux` | 所有请求入口 |
| HTTP 传输层 | `client` | 上游代理转发 |
| 日志存储 | `logs` | 管理 API、Dashboard |
| 监控指标 | `metrics` + `metricsRegistry` | 全局 |
| 熔断器 | `keyCBs` + `upCB` | 代理转发逻辑 |
| 健康检查状态 | `lastHealthCheckTime/OK` | 管理 API |
| UI | `dashboardHTML` | Dashboard 路由 |
| 持久化 | `keysFile` | Key 增删改的落盘 |

**违反表现**:
- 新增一个 handler 需要往 `ServerState` 上加方法，但状态也在累积
- 修改 KeyPool 的 API 时，虽然逻辑在 keypool 包，但注册路由和 Auth 检查在 ServerState
- `PersistKeys()` 在 ServerState 上定义，但它操作的完全是 keypool 的数据

**修复建议**: 将 `ServerState` 拆分为多个内聚的小结构体，通过组合或接口注入：

```
ServerState (薄壳: 路由注册 + 启动)
├── ConfigManager (配置加载/热重载)
├── KeyPoolManager (KeyPool + Persistence)
├── ProxyEngine (client + 代理逻辑 + CB)
├── MetricsCollector (metrics + registry)
└── DashboardUI (dashboardHTML)
```

`NewServerState` 变为组合这些子组件，各 handler 只引用它们需要的组件。

---

#### 2. LoadConfig / ReloadConfig 代码重复

**类型**: 违反 DRY（不要重复自己）  
**位置**: `internal/server/server.go:217-258` 和 `261-297`  
**严重度**: 🔴 高

`LoadConfig` 与 `ReloadConfig` 的 keys 文件合并逻辑几乎完全相同：

```go
// LoadConfig (line 233-251)
keys := cfg.Keys
names := cfg.KeyNames
if cfg.KeysFile != "" {
    fileKeys, fileNames, err := keypool.LoadKeysFromFile(cfg.KeysFile)
    if err != nil { ... }
    else if fileKeys != nil {
        cfg.Keys = fileKeys; cfg.KeyNames = fileNames
        keys = fileKeys; names = fileNames
    } else {
        // create file from env keys (LoadConfig 独有)
        keypool.SaveKeysToFile(cfg.KeysFile, keys, names)
    }
}

// ReloadConfig (line 280-294) — 几乎相同，但少了 auto-create
keys := cfg.Keys
names := cfg.KeyNames
if cfg.KeysFile != "" {
    fileKeys, fileNames, err := keypool.LoadKeysFromFile(cfg.KeysFile)
    ...
}
cfg.Keys = keys; cfg.KeyNames = names
```

**修复建议**: 提取共享辅助函数:

```go
func mergeKeysFromFile(cfg *config.Config) (keys, names []string, fromFile bool, err error) {
    // 统一的 keys 文件合并逻辑
}
```

---

#### 3. 配置字段增长导致"霰弹式修改"

**类型**: 违反 OCP（开闭原则）+ 违反 DRY  
**位置**: `internal/config/config.go` — 跨越 `Load()`、`Validate()`、`Diff()`  
**严重度**: 🔴 高

新增一个配置字段需要在 **7 个以上位置** 修改：

1. `Config` 结构体定义（第 42-65 行）
2. `DefaultConfig()` 设置默认值（第 68-83 行）
3. `Load()` 中解析环境变量（第 122-259 行）
4. `Validate()` 中验证字段（第 322-357 行）
5. `Diff()` 中对比差异（第 384-523 行）
6. `ReloadConfig()` 中清除环境变量列表（第 261-271 行）
7. 可能还需要在 handler 中暴露 API（handlers.go）

**违反表现**:
- `Diff()` 方法约 140 行，由 20 个几乎完全相同的 `if c.Field != other.Field` 块组成
- 每个字段的 env var name 字符串（如 "BACKOFF_CAP_SEC"）在 Load + Validate + Diff + ReloadConfig 中重复出现
- 缺失字段未在 ReloadConfig 中清零（如 `HEALTH_CHECK_INTERVAL_SEC` 系列字段在 `ReloadConfig` 中已添加，但这个过程是手动的）

**修复建议**:
- 选项 A（推荐）: 使用字段注册表模式，定义 `FieldSpec` 结构，通过 `for range` 统一处理加载/验证/对比
- 选项 B（最简）: 将 `Diff()` 改为基于反射的通用 diff，减少新字段的修改点
- 选项 C（折中）: 定义一个 `fieldDef` 切片，包含字段名、env 变量名、默认值、验证函数，用循环处理加载和验证

选项 A 示例骨架：

```go
type FieldSpec struct {
    EnvVar      string
    Default     interface{}
    Validate    func(interface{}) error
    DiffValue   func(*Config) interface{}
}
```

---

### 🟡 建议改进

#### 4. KeyPool 读操作用 Mutex 而非 RWMutex

**类型**: 锁粒度过粗  
**位置**: `internal/keypool/keypool.go` — `Keys()`（46 行）、`ActiveCount()`（182 行）、`CoolingCount()`（208 行）、`Name()`（55 行）等  
**严重度**: 🟡 中

`KeyPool` 使用单一的 `sync.Mutex` 保护所有操作，包括纯读取的操作。在高并发场景下，`Keys()` 这样的只读操作也会阻塞写操作（如 `Cooldown`/`Disable`），反之亦然。

**违反表现**: `Keys()` 获取独占锁后复制整个切片，这个操作不需要排他；`ActiveCount()` 扫描数组的读锁足够。

```
goroutine A: Keys() 获取互斥锁 — 读操作阻塞了 goroutine B 的 Cooldown() 写操作
```

**修复建议**: 将 `sync.Mutex` 改为 `sync.RWMutex`，所有只读方法用 `RLock()` / `RUnlock()`。

注意：`KeyStatusLabel(i, now)` 当前**无锁**调用（依赖调用者加锁），如果改为 RWMutex，这个方法需要在调用时明确约定锁模式。

---

#### 5. proxyHandler 函数过长

**类型**: 违反 SRP（单一职责）  
**位置**: `internal/server/proxy.go:42-281`  
**严重度**: 🟡 中

`proxyHandler` 约 240 行，在一个函数中完成了：

- 请求体读取与尺寸限制
- URL 路由（genai vs target）
- 重试循环
- 上游熔断器检查
- Key 选取与状态检查
- Key 级熔断器检查
- 上游请求构建与发送
- 429 / 502 / 503 / 401 / 403 / 4xx / 5xx 等 6 种以上的响应分支处理
- SSE 流式响应
- 日志记录
- 指标记录

**违反表现**: 代码阅读难度高，每个分支逻辑相互交织。例如"重试循环"中既包含 key 选取逻辑又包含 upstream CB 逻辑还包含 error code 判断。

**修复建议**: 提取内部方法：

```go
func (s *ServerState) proxyHandler(w, r) { ... }
// 提取出
func (s *ServerState) buildUpstreamRequest(r, target) (*http.Request, error)
func (s *ServerState) selectKey(pool, keyCBs) (idx int, key string, ok bool)
func (s *ServerState) handleUpstreamResponse(w, r, resp, idx, key, bodyBytes) (handled bool)
func (s *ServerState) streamResponse(w, resp)
```

---

#### 6. crypto.go 使用包级可变状态

**类型**: 难以测试 + 潜在的竞态风险  
**位置**: `internal/keypool/crypto.go:14-17`  
**严重度**: 🟡 中

```go
var (
    encryptionKey []byte
    encryptionMu  sync.RWMutex
)
```

加密密钥通过包级变量 `SetEncryptionKey()` 设置，测试时必须记得 `defer SetEncryptionKey(nil)` 清理。如果某个测试忘记清理，会影响同一测试二进制中的其他测试。

且 `Encrypt()` 和 `Decrypt()` 通过 `RLock` 读取包级变量，在所有依赖 keypool 的测试间共享。

**违反表现**: 测试文件（`store_test.go:248-249`）和集成测试（`proxy_test.go:1433-1434`）都必须显式 `SetEncryptionKey / defer SetEncryptionKey(nil)`，容易遗漏。

**修复建议**: 将加密密钥作为 `Store` 结构体的字段，而非包级全局变量：

```go
type Store struct {
    keys          []KeyEntry
    encryptionKey []byte // nil = disabled
}

func NewStore(encryptionKey []byte) *Store { ... }
```

这样每个 `Store` 实例独立持有自己的密钥，测试不需要全局状态管理。

---

#### 7. PersistKeys 沉默吞错

**类型**: 错误处理不当  
**位置**: `internal/server/server.go:145-164`  
**严重度**: 🟡 中

```go
func (s *ServerState) PersistKeys() {
    ...
    if err := keypool.SaveFullStore(cfg.KeysFile, ...); err != nil {
        slog.Error("failed to persist keys", ...) // 只打日志
    }
}
```

`PersistKeys` 被 `keysHandler` 的 POST/DELETE 和 `disableKeyHandler`/`deleteKeyHandler` 等管理 API 调用。如果持久化失败，API 返回 `200 OK`，但磁盘上实际没有写入。调用方无感知。

**修复建议**: 改为返回 `error`，让调用 handler 在持久化失败时返回 `500 Internal Server Error`。对静默的周期性持久化调用保留不返回 error 的版本。

---

### 🟢 值得注意

#### 8. Dual Backoff 机制 — CooldownSec vs BackoffCapSec

**类型**: 设计复杂度  
**位置**: `internal/server/proxy.go:178-181` 和 `internal/circuitbreaker/key.go:54-57`  
**严重度**: 🟢 低

Key 被 429 后会经历两层退避：

1. **KeyPool.Cooldown(idx, cooldown)** — 由 `CooldownSec` 控制，固定时长
2. **KeyCircuitBreaker.RecordFailure()** — 由 `BackoffCapSec` + `BackoffMultiplier` 控制，指数退避

这两个退避机制同时生效，但目标不同：
- KeyPool 的 cooldown 阻止 Next() 选中该 key
- KeyCircuitBreaker 的 Allow() 在 proxy loop 中检查

当 KeyCircuitBreaker 判定永久禁用时，KeyPool 的 Disable() 也会调用。两个机制的交互关系文档没有明确说明。

**建议**: 在注释或 AGENTS.md 中明确两套退避的层级关系：KeyCB 是指数退避（同一 key 反复 429 → 逐次加长），KeyPool 固定 Cooldown 是配合 Retry-After header 的瞬态冷却。

---

#### 9. utils.LogEntry 跨包共享

**类型**: 耦合  
**位置**: `internal/utils/utils.go:8-17`  
**严重度**: 🟢 低

`LogEntry` 定义在 `utils` 包，被 `logstore` 和 `server` 同时引用。虽然目前是简单的数据 DTO，但如果未来需要加序列化标签或验证逻辑，`utils` 会成为修改热点。

**建议**: 当 `LogEntry` 需要添加行为（验证、序列化）时，将它移到 `logstore` 包中并导出。

---

#### 10. 双层熔断器设计值得肯定

**类型**: 设计亮点  
**位置**: `internal/circuitbreaker/` 两个文件  
**严重度**: 🟢 正面

`KeyCircuitBreaker`（每 Key 级别）与 `UpstreamCircuitBreaker`（上游级别）的分离设计清晰：

- KeyCB → 429 触发 Key 级退避，401/403 触发 Key 永久禁用
- UpstreamCB → 502/503 触发上游级熔断，不影响 Key 状态

这是正确的职责分离，与项目"专注单 provider 内 API Key 轮转"的定位一致。

**特别加分**: 集成测试 `TestCB_UpstreamErrorNoKeyPenalty`（proxy_test.go:1070）显式验证了"上游错误不惩罚 Key"这一关键行为，这是高价值的验收测试。

---

#### 11. 集成测试设计优秀

**类型**: 测试设计亮点  
**位置**: `proxy_test.go` 全文件  
**严重度**: 🟢 正面

测试设计符合 Testing Trophy 模型，具体表现：

- 所有集成测试使用真实的 `httptest.Server` 作为 mock upstream
- 测试模拟了 `429 → retry → 200`、`401 → disable → fallback`、`503 → upstream CB` 等真实场景
- `TestCB_UpstreamCircuitBreakerOpens` 验证了 CB 打开后 _不再调用 upstream_ 的关键行为
- `TestKeyPersistence_AddKeyRestart` 验证了"重启后 Key 仍在"的全生命周期
- `TestKeyEncryption_EndToEnd` 验证了加密文件的完整流程（写密文 → 读明文 → API 返回掩码）
- 并发测试（`TestProxyConcurrentRequests`、`TestProxyConcurrentKeyRotation`）验证了锁的正确性

这些测试**不是**在测代码覆盖率，而是在测**用户可见的行为**。

---

#### 12. 配置热重载的"失败保留旧配置"策略正确

**类型**: 设计决策  
**位置**: `internal/server/lifecycle.go:46-49`  
**严重度**: 🟢 正面

```go
newCfg, newPool, err := ReloadConfig()
if err != nil {
    slog.Error("env reload failed; keeping previous config", "error", err)
    continue
}
```

`.env` 文件热重载失败时保留旧配置，而不是让服务崩溃或进入不一致状态。这是一个好的错误处理设计，避免了"坏配置导致服务不可用"的场景。

---

### 原则符合度总结

| 原则 | 状态 | 说明 |
|------|------|------|
| SRP | ⚠️ 局部违反 | ServerState 承担 5+ 职责；proxyHandler 240 行 |
| OCP | ❌ 违反 | 新增配置字段需改 7+ 处 |
| DIP | ⚠️ 局部违反 | ServerState 直接依赖具体类型而非接口 |
| DRY | ❌ 违反 | LoadConfig/ReloadConfig 重复；Diff 方法重复 if-block |
| KISS | ✅ 满足 | 整体设计简洁，无过度抽象 |
| YAGNI | ✅ 满足 | 未发现投机代码 |
| LSP | ✅ 不适用 | 无继承层级 |
| ISP | ✅ 不适用 | 无胖接口 |

---

## 优先修复建议

| 优先级 | 问题 | 预期收益 | 工作量 |
|--------|------|---------|--------|
| P0 | ServerState 拆分为组合结构 | 各职责独立演化，可测试性大幅提升 | 中（重构 ServerState + handler 引用方式） |
| P1 | 消除 LoadConfig/ReloadConfig 重复 | 减少 bug 引入概率 | 小（提取 mergeKeysFromFile） |
| P1 | 配置字段注册表模式 | 新增字段从 7 处减少到 2-3 处 | 中（重构 config 包） |
| P2 | KeyPool 读操作用 RWMutex | 高并发下减少锁竞争 | 小（Mutex → RWMutex） |
| P2 | crypto.go 包级变量改为实例字段 | 消除测试间状态泄漏 | 中（重构 SetEncryptionKey 为 Store 字段） |
| P3 | PersistKeys 返回 error | API 调用方能感知持久化失败 | 小 |

---

## 总体评价

**综合评分: 7/10** — 好的地方非常好，硬伤集中在两个可明确修复的问题上。

Alvus 是一个**职责聚焦、设计适度、测试到位**的项目。核心优势在于：

1. **功能边界清晰** — 明确"只做 API Key 轮转"，不膨胀范围
2. **测试质量高** — 集成测试验证真实用户行为，有 before/after 对比
3. **错误处理有层次** — 区分 ConfigError 的 category，热重载失败保留旧配置
4. **并发模型合理** — RWMutex + atomic counter + stop channel，goroutine 生命周期完整

主要重构方向可以概括为 **"为 ServerState 瘦身"**，并**为配置增长建立约束**。截至当前代码量（约 2500 行），这些设计债尚未造成实质伤害，但随着功能增长会逐渐加重。

建议在下一次跨越 3+ 个包的改动前，先完成 ServerState 拆分和配置注册表化，避免债务累积。