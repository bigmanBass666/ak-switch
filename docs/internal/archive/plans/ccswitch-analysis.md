# ccswitch 源码分析报告

> 基于 `farion1231/cc-switch` (main, commit `f15338b`, 2026-06-30) 源码分析
> 仓库: https://github.com/farion1231/cc-switch — Tauri 桌面应用 (Rust + React)

---

## 一、架构总览

ccswitch 是一个 Tauri 桌面应用（Rust 后端 + React 前端），本质上是一个**本地反向代理网关**。

### 技术栈
- **后端**: Rust + axum HTTP 服务器（localhost:15721）
- **前端**: React + Vite + Tailwind
- **数据**: SQLite (rusqlite) + JSON 配置
- **构建**: Tauri (Rust + WebView)

### 核心模块（Rust 后端）
```
src-tauri/src/proxy/
├── server.rs              # axum HTTP 服务器启动/停止
├── handlers.rs            # API 端点处理（/v1/messages, /v1/chat/completions 等）
├── provider_router.rs     # 供应商路由选择 + 故障转移排队
├── circuit_breaker.rs     # 熔断器（三态：Closed/Open/HalfOpen）
├── failover_switch.rs     # 故障转移切换（UI 更新 + 去重控制）
├── forwarder.rs           # 请求转发 + 重试循环
├── response_handler.rs    # 响应处理（流式/非流式）
├── session.rs             # 会话管理
├── providers/             # Provider 适配器（Anthropic/OpenAI/Gemini 格式转换）
├── usage/                 # 用量统计与 Token 解析
├── types.rs               # ProxyStatus, AppProxyConfig 等数据类型
└── error.rs               # ProxyError, ErrorCategory 错误分类
```

---

## 二、路由机制（Routing）

### 2.1 多应用路由

ccswitch 同时支持**3 个应用**的独立路由：
- Claude Code (`/v1/messages`)
- Codex CLI (`/v1/chat/completions`, `/v1/responses`)
- Gemini CLI (`/v1/models`, `/v1/chat/completions`)

每个应用有独立的：
- 故障转移队列
- 熔断器配置
- 用量统计

### 2.2 路由原理

路由实际上是修改 CLI 工具的配置文件，将 API Base URL 指向 ccswitch 本地代理：

```
Claude:   ANTHROPIC_BASE_URL → http://127.0.0.1:15721
Codex:    base_url → http://127.0.0.1:15721/v1
Gemini:   GOOGLE_GEMINI_BASE_URL → http://127.0.0.1:15721
```

### 2.3 Provider 选择流程 (`provider_router.rs`)

```rust
select_providers(app_type) -> Vec<Provider>
```

两种模式：
1. **故障转移关闭**：仅返回 `current_provider`（配置中的当前供应商），**跳过熔断器检查**
2. **故障转移开启**：仅按**故障转移队列顺序**返回（P1 → P2 → P3...），跳过已熔断的供应商

**关键设计**：`select_providers` 只做"可用性判断"（`is_available()`），**不消耗** HalfOpen 探测名额。真正的放行检查在 `allow_request()`。

---

## 三、故障转移机制（Failover）

### 3.1 故障转移流程 (`forwarder.rs`, `forward_with_retry_inner`)

```
请求到达
  → select_providers() 获取可用供应商列表
  → 依次遍历每个 provider:
    → allow_request() 获取熔断器放行许可
    → 转发请求到上游
    → 成功？→ record_success() + 切换当前 provider
    → 失败？
      → categorize_proxy_error() 分类错误
      → Retryable → record_failure() + 尝试下一个 provider
      → NonRetryable → 直接返回错误，不污染熔断器健康度
```

### 3.2 错误分类 (`error.rs`)

```
ErrorCategory:
  Retryable      — 网络超时、5xx、连接失败 → 换 provider 重试
  NonRetryable   — 400/405/406/413/414/415/422/501 → 客户端问题，不重试
  ClientAbort    — 客户端主动中断 → 不计入健康度
```

**精细的 4xx 分类**：不同于简单的"4xx = 客户端错误"，ccswitch 区分了哪些 4xx 换 provider 可能解决：
- **可重试 4xx**: 401/403/404/408/409/429/451（换 provider 可能不同 key/配额）
- **不可重试 4xx**: 400/405/406/413/414/415/422/501（纯客户端问题）

### 3.3 去重控制 (`failover_switch.rs`)

```rust
FailoverSwitchManager {
    pending_switches: HashSet<String>  // "app_type:provider_id"
}
```

- 同一 app 的相同切换**正在处理中时**跳过（幂等保护）
- 切换成功后：
  1. 热切换 provider（更新内存中的当前 provider）
  2. 更新托盘菜单
  3. 发射 `provider-switched` 事件到前端

### 3.4 最大尝试次数

`AppProxyConfig.max_retries`（默认 3-6），含义是**最多尝试多少个不同的 provider**，不是同一个 provider 的重试次数。

---

## 四、熔断器（Circuit Breaker）—— `circuit_breaker.rs`

### 4.1 三态模型

| 状态 | 说明 | 转换条件 |
|------|------|----------|
| **Closed** | 正常，允许请求 | 连续失败 ≥ 阈值 → Open |
| **Open** | 熔断，拒绝请求 | 超时到期 → HalfOpen |
| **HalfOpen** | 探测模式，放行有限请求 | 探测成功 ≥ 阈值 → Closed；探测失败 → Open |

### 4.2 核心设计

```rust
CircuitBreakerConfig {
    failure_threshold: 4,           // 连续失败多少次触发熔断
    success_threshold: 2,           // HalfOpen 下成功多少次后关闭
    timeout_seconds: 60,            // Open → HalfOpen 等待时间
    error_rate_threshold: 0.6,      // 错误率阈值（60%）
    min_requests: 10,               // 计算错误率前的最小请求数
}
```

**触发条件（双重检测）**：
1. 连续失败数 ≥ `failure_threshold`
2. **或** 错误率 ≥ `error_rate_threshold`（需要 ≥ `min_requests` 次请求）

### 4.3 HalfOpen 探测名额限制

```rust
// 最多允许 1 个并发探测请求
max_half_open_requests = 1;
```

**关键机制**：
- `is_available()` — **非消耗性**检查（不占探测名额），用于路由选择阶段
- `allow_request()` — **消耗性**检查（HalfOpen 下占用探测名额），用于实际转发
- `record_success/failure` — 释放探测名额 + 更新健康统计
- `release_permit_neutral()` — **仅释放名额不影响统计**（用于整流器/非健康相关场景）

### 4.4 热更新

```rust
update_config()   // 更新单个熔断器配置（不重置状态）
update_all_configs()  // 更新所有熔断器配置
update_app_configs()  // 更新指定应用的所有熔断器
```

### 4.5 统计信息

```rust
CircuitBreakerStats {
    state: CircuitState,            // 当前状态
    consecutive_failures: u32,      // 连续失败次数
    consecutive_successes: u32,     // 连续成功次数
    total_requests: u32,            // 总请求数
    failed_requests: u32,           // 失败请求数
}
```

---

## 五、对比 Alvus 现有实现

### 5.1 路由层对比

| 维度 | ccswitch | Alvus |
|------|----------|-------|
| **路由粒度** | Provider（供应商） | API Key |
| **多应用支持** | ✅ Claude/Codex/Gemini | ❌ 单应用 |
| **故障转移队列** | ✅ 按优先级排序的 Provider 列表 | ❌ Key 池顺序（round-robin，无优先级） |
| **API 格式转换** | ✅ Anthropic ↔ OpenAI | ❌ 纯透传（符合定位） |

### 5.2 熔断器对比

| 维度 | ccswitch | Alvus |
|------|----------|-------|
| **状态机** | Closed/Open/HalfOpen | Closed/Open/Permanent |
| **触发条件** | 连续失败 + 错误率双重检测 | 仅连续失败 |
| **HalfOpen 探测** | ✅ 限流 1 个并发探测 | ❌ 无 HalfOpen（直接进入 Open） |
| **恢复机制** | 超时自动 → HalfOpen → 探测成功 → Closed | 超时自动 → Closed |
| **Permanent 状态** | ❌ 无 | ✅ 401/403 触发永久禁用 |
| **健康统计** | ✅ 总请求/失败数/错误率 | ❌ 仅计数 |
| **release_permit_neutral** | ✅ 支持（别名的健康不污染） | ❌ 无此概念 |
| **热更新** | ✅ 运行时更新配置 | ❌ 需要重启 |

### 5.3 错误分类对比

| 维度 | ccswitch | Alvus |
|------|----------|-------|
| **分类策略** | 7 种 ErrorCategory（细粒度） | 4 种 ErrorCode（粗粒度） |
| **4xx 细粒度** | ✅ 区分可重试/不可重试 4xx | ❌ 全部 4xx 透传 |
| **NonRetryable 不污染健康** | ✅ | 🔶 401/403 不走重试，但其他 4xx 不分类 |
| **客户端中断** | ✅ ClientAbort 不计数 | ❌ 无此概念 |

### 5.4 优势对比总结

**Alvus 的优势**：
- Key 维度旋转（ccswitch 没有 Key 概念）
- Key 加密持久化
- 轻量级（~2500 行 Go vs ccswitch 的 1045+ 文件 Tauri 应用）
- 独立部署，不依赖桌面环境

**ccswitch 的优势**：
- 多 Provider 路由 + API 格式转换
- 更完善的熔断器（HalfOpen + 错误率 + 热更新）
- 用量统计和 Token 解析
- 可视化 Dashboard
- 精细的错误分类

---

## 六、社区动向

社区中 `yourChainGod/cc-switch` fork 的描述为：
> "Key 池、通道级故障转移、会话亲和"

这验证了：
1. **社区确实需要 API Key 级别的管理能力**
2. 我们的定位（Alvus = Key 层轮转）与生态需求一致
3. ccswitch 自身不会下沉做 Key 管理（Tauri 桌面应用架构不适合）

---

## 七、对 Alvus 的建议

基于本次分析，建议 Alvus 在以下方向中选择：

### 方向 A：强化熔断器（轻量改进，1-2 天）

对标 ccswitch 的熔断器设计，提升 Alvus 的健壮性：

1. **引入 HalfOpen 状态** — 将当前 `Closed/Open/Permanent` 三态改为 `Closed/Open/HalfOpen/Permanent` 四态
   - 当前问题：Open → Closed 直接跳转，缺乏探测机制
   - 改进：Open 超时后 → HalfOpen → 探测成功 → Closed / 探测失败 → Open

2. **错误率检测** — 在连续失败触发之外，增加基于错误率的熔断
   - 场景：Key 间歇性失败（非连续但高频），当前机制无法检测

3. **错误分类细化** — 引入 ccswitch 式的 ErrorCategory
   - NonRetryable（400/422 等客户端问题）不消耗重试次数
   - 区分"上游问题"和"Key 问题"

4. **`release_permit_neutral` 模式** — 用于健康检查等非业务场景，不污染 Key 健康度

**工作量**: 中（3-5 天）
**收益**: 熔断器行为更精确，减少误判

### 方向 B：Dashboard 增强（用户可见体验，3-5 天）

参考 ccswitch 的 ProxyStatus 模型：

1. **实时健康状态面板** — 类似 ccswitch 的供应商卡片（绿/黄/红状态）
2. **故障转移日志** — 记录每次 Key 切换的原因和时间
3. **请求详情页** — 类似 ccswitch 的用量统计

**工作量**: 中
**收益**: 用户可见体验提升

### 方向 C：对接 ccswitch 的数据通道（架构改进，待定）

1. **嵌入 ccswitch 的 Provider 管理** — Alvus 作为 ccswitch 的"Key 层插件"
2. **共享配置/状态** — ccswitch 管理 Provider，Alvus 管理 Key
3. **会话亲和（Session Affinity）** — 同一会话使用同一 Key

**工作量**: 大
**收益**: 与生态对齐，潜力最大

### 方向 D：性能优化（有数据支撑，1-2 天）

之前压测显示 100+ QPS 开始饱和：
1. **Key 选择策略** — 当前 round-robin，可加权/least-loaded
2. **HTTP 连接池调优** — 当前 MaxIdleConns=100, MaxIdleConnsPerHost=10
3. **零拷贝转发** — `io.CopyN` / `splice` 减少内存拷贝

**工作量**: 小-中
**收益**: 可量化（有压测基线 50 QPS p99 15.8ms）

---

## 八、推荐优先级

基于当前项目状态（功能基本完备，182 测试，代码质量良好），以及 ccswitch 的对比：

**第一优先：方向 A（强化熔断器）**
- 最直接对标 ccswitch 的设计优势
- 有明确的可验证目标（HalfOpen 行为、错误率检测）
- 不改变对外接口，风险最低
- 为后续的"优雅降级"功能打基础

**第二优先：方向 A 的子项——错误分类细化**
- 在 proxyHandler 中引入 ErrorCategory
- 当前 4 个 ErrorCode → 细化为 6-8 个
- 非可重试错误不消耗重试次数
- ClientAbort 不污染 Key 健康度

**第三优先：方向 D（性能优化）**
- 有压测数据支撑瓶颈明确
- 改动小，收益可量化

---

*分析日期: 2026-06-30*
*基于: farion1231/cc-switch commit f15338b*