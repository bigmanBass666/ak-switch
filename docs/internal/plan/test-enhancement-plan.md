# 测试增强计划

> 编写日期: 2026-06-24
> 更新日期: 2026-06-24
> 状态: 已定稿 / 待执行

## 一、现状与问题

### 当前回归测试覆盖了什么

| 测试套件 | 用例数 | 覆盖范围 | 是否真实业务 |
|---------|--------|---------|------------|
| 单实例模式 | 5 | HTTP 端点响应、配置读写、Key掩码、日志清空 | ❌ |
| 管理模式 | 3 | 多实例启动、非法配置处理 | ❌ |
| 进程生命周期 | 3 | 子进程启动/停止/自动重启 | ❌ |
| 进程隔离 | 3 | -tag标识、端口冲突、tag继承 | ❌ |

### 当前回归测试没覆盖什么

回归测试的 `.env` 中 `TARGET_BASE_URL=https://test.api.example.com/v1` 是假地址，永远连不上。因此 proxyHandler 的核心逻辑：

- **代理转发** — 请求是否到达上游？响应是否正常返回？
- **Key 轮换** — 多个 key 是否被循环使用？
- **冷却与重试** — 429 后是否触发 cooldown？重试后是否成功？
- **认证注入** — Authorization: Bearer header 是否正确设置？
- **SSE 流式传输** — 流式响应是否正常分块传输？
- **错误处理** — 401/403 禁用 key、503 重试、5xx 重试？

以上逻辑从未在测试中实际执行。

### 一次性的 E2E 测试

2026-06-24 在 `test/e2e` 分支上手动执行了面向真实 NVIDIA API 的 E2E 测试：
- `GET /v1/models` → 200 ✅
- `POST /v1/chat/completions` → `"Hello."` ✅
- Key 掩码、/api/keys 增删、端口冲突 全部通过

但这是手动操作，不可重复，无法纳入 CI。

---

## 二、目标

让回归测试能够真正执行 Alvus 的代理转发逻辑，验证：

1. 基本 HTTP 代理转发正确
2. Key 轮换机制工作
3. 冷却/重试/禁用机制工作
4. SSE 流式传输正常
5. 运行时 Key 管理不影响转发
6. 端口冲突等边界情况

**不依赖真实 API Key、不消耗额度、完全可控、可重复运行。**

---

## 三、选定方案

### 技术栈：Go `httptest` + 黑盒测试

使用 Go 标准库 `net/http/httptest`，在独立的 Go 测试文件（`proxy_test.go`、`handlers_test.go` 等）中编写测试。

### 为什么选这个

| 因素 | Go httptest | PowerShell HttpListener |
|------|------------|------------------------|
| 类型安全 | ✅ 强类型 | ❌ 弱类型 |
| SSE 支持 | ✅ http.Flusher 原生支持 | ❌ 手动实现困难 |
| 速度 | ✅ 毫秒级 | ❌ 进程级启动慢 |
| 全局状态 | ✅ LogStore 已重构，无全局变量 | ❌ 无关 |
| 维护成本 | ✅ Go 原生测试工具链 | ❌ 额外学习成本 |

### 分层策略

```
Go 测试文件（新增）           PowerShell 回归测试（已有）
─────────────────────        ─────────────────────────
代理转发逻辑                 进程生命周期
Key 轮换                    管理模式启动/停止
冷却/重试/禁用              端口冲突
SSE 流式传输                 进程隔离
/ API 管理 + 转发             Tag 继承
                             清理验证

`go test -v ./...`           `.\regression_test.ps1`
快速、精确、可并行            慢（进程级）、覆盖运维场景
```

两者互补，不互相替代。

### 全局状态问题（已解决）

在此之前存在 `usageLogs` 全局变量跨测试泄漏的问题。LogStore 重构已解决：
- `usageLogs` / `usageMu` / `appendUsageLog` 已删除
- `LogStore` 嵌入 `ServerState.logs`，每个状态实例独立
- `go test` 可以 `-p 1` 串行跑，也可以并行跑（LogStore 自带 Mutex）

### 测试架构

```
┌─────────────────────┐     HTTP      ┌─────────────────────┐
│  Go 测试 (testing.T) │ ──────────→   │  Alvus ServerState   │
│                     │               │  (httptest.NewServer) │
│  验证响应内容        │               │          │           │
│  验证 Key 轮换      │               │          │           │
│  验证 Auth header   │               │    HTTP  │           │
└─────────────────────┘               │          ▼           │
                                       │  ┌──────────────────┐│
                                       │  │ Mock 上游 Server  ││
                                       │  │(httptest.NewServer)││
                                       │  │ 可编程响应行为     ││
                                       │  │ 记录收到的请求     ││
                                       │  └──────────────────┘│
                                       └─────────────────────┘
```

### 具体做法

```go
// proxy_test.go
func TestProxyHandler_BasicForward(t *testing.T) {
    // 1. 起 mock 上游：返回固定 JSON
    upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 记录请求头（验证 Auth header）
        // 返回预定义的响应
    }))
    defer upstream.Close()

    // 2. 构造 Alvus 配置 + KeyPool
    cfg := Config{TargetBase: upstream.URL, ...}
    pool := NewKeyPool([]string{"test-key-1", "test-key-2"})

    // 3. 起 Alvus HTTP 服务
    state := newServerState(cfg, pool)
    alvus := httptest.NewServer(state.mux)
    defer alvus.Close()

    // 4. 发请求过 Alvus 代理
    resp, _ := http.Get(alvus.URL + "/v1/chat/completions")

    // 5. 验证响应
    assert.Equal(t, 200, resp.StatusCode)
}
```

对于需要并行运行的测试：每个测试创建独立的 ServerState + httptest.Server，互不干扰。

---

## 四、测试场景

| # | 测试名 | Mock 上游行为 | 验证点 |
|---|-------|--------------|--------|
| 1 | 基本转发 | 返回固定 JSON `{"ok":true}` | HTTP 200，响应体匹配 |
| 2 | 认证注入 | 记录 Authorization header，返回 200 | Header 存在、格式为 `Bearer key`、key 内容正确 |
| 3 | Key 轮换 | 返回 200 | 两次请求的 Authorization 使用不同 key |
| 4 | 429 冷却 | 第一次 429，第二次 200 | 第一次触发 cooldown，最终请求成功 |
| 5 | 401 禁用 | 某 key 持续 401，其他 key 正常 | 该 key 被 disable，请求最终成功 |
| 6 | 5xx 重试 | 第一次 503，第二次 200 | 记录重试日志，最终成功 |
| 7 | 全部冷却 | 所有 key 返回 429 | 最终返回 503 `exhausted all retries` |
| 8 | SSE 流式 | 分块发送 3 行 data | 客户端收到完整 3 行 |
| 9 | 空响应体 | 返回 204 | 透传 204 |
| 10 | 请求体透传 | 发送 `{"hello":"world"}`，上游返回相同 body | 响应体与请求体一致 |
| 11 | /api/keys + 转发 | 添加 key 后请求正常 | 添加/删除 key 后转发不中断 |
| 12 | MAX_RETRIES 配置 | 连续失败后停止 | 在 MaxRetries 次失败后返回 503 |

---

## 五、文件规划

```
alvus-fork/
├── proxy_test.go          # 代理转发核心测试（新建）
├── handlers_test.go       # HTTP handler 测试（新建）
├── keypool_test.go        # KeyPool 单元测试（新建）
├── logstore_test.go       # LogStore 测试（已有）
├── regression_test.ps1    # 进程级回归测试（已有，不变）
└── ...
```

---

## 六、执行计划

1. 从 `test/e2e` 分支继续（该分支已有过去的手动 E2E 测试记录）
2. 编写 `proxy_test.go` — 核心代理转发场景（12 个测试）
3. 编写 `handlers_test.go` — HTTP handler 单元测试
4. 编写 `keypool_test.go` — KeyPool 单元测试（纯逻辑，无网络）
5. `go test -v -p 1 ./...` 验证全过
6. `regression_test.ps1` 验证 29/29 全过
7. 不合并到 main，测试分支保留

---

## 七、尚未解决的问题

- 当前 mock 上游不能验证 Key 轮换的「顺序」（轮换逻辑在 ServerState 内部，外部只能看 Auth header 是否是不同 key）
- SSE 测试需要处理分块传输的边界条件
- 现有 `regression_test.ps1` 的 mock 上游 `test.api.example.com` 是假地址，但暂时不改——PowerShell 测试专注进程管理，Go 测试专注代理逻辑，职责分离
