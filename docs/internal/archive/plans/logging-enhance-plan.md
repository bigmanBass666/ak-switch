# Plan: 日志系统增强（压测前可观测性加固）

## 摘要

在现有日志系统基础上，添加 3 个零成本字段 + 1 个缺失日志记录 + CLI 展示同步，使压测时有完整的可观测性。

---

## 当前状态分析

### 日志管道路径

```
executeProxy() → 构造 LogEntry → pr.logs.Append() → JSON 序列化 → /logs 端点 → CLI 展示
```

### 当前 LogEntry 字段（8 个）

| 字段 | 来源 | 状态 |
|------|------|------|
| `timestamp` | `time.Now().Format(time.RFC3339)` | ✅ 已记录 |
| `key` | `pool.KeyAt(idx)` → Append 时 `MaskKey` | ✅ 已记录 |
| `key_index` | `idx + 1`（1-based） | ✅ 已记录 |
| `key_name` | `pool.Name(idx)` | ✅ 已记录 |
| `method` | `r.Method` | ✅ 已记录 |
| `url` | 经过路由后的完整 upstream URL | ✅ 已记录 |
| `status` | `resp.StatusCode` | ✅ 已记录 |
| `request_body_size` | `len(bodyBytes)` | ✅ 已记录 |

### 可用但未记录的数据

| 数据 | 代码位置 | 当前用途 |
|------|---------|---------|
| `time.Since(start).Milliseconds()` | `handlers.go:118` 已有 `start := time.Now()` | 仅 `slog.Debug` 输出 |
| `attempt + 1` | `handlers.go:175` 已有 `for attempt := 0; ...` | 仅 `slog.Info` 输出 |
| `ps.Name`（provider） | 所有 log 点 `ps *ProviderState` 上下文 | 仅 `slog.Info` 输出 |
| `cfg.MaxRetries` | `handlers.go:175` | 仅用于循环条件 |
| 重试耗尽时的 503 响应 | `handlers.go:362-365` | **没有写 LogEntry！** |

### 关键缺口

1. **`duration_ms` 缺失** — 压测时无法知道每个请求的耗时分布
2. **`attempt` 缺失** — 无法区分"一次成功"和"重试后才成功"
3. **`provider` 缺失** — 多 provider 场景下日志需要知道属于哪个 provider
4. **重试耗尽无日志** — 当所有 Key 都尝试失败返回 503 时，LogStore 里没有任何记录，查不到
5. **CLI 展示不全** — `alvus logs` 只显示 4/8 个字段，key_index/key_name 等不展示

---

## 改动方案

### Change 1: LogEntry 新增 3 个字段

**文件**: `internal/utils/utils.go` — LogEntry 结构体

```go
type LogEntry struct {
    Timestamp       string `json:"timestamp"`
    Key             string `json:"key"`
    KeyIndex        int    `json:"key_index"`
    KeyName         string `json:"key_name"`
    Method          string `json:"method"`
    URL             string `json:"url"`
    Status          int    `json:"status"`
    RequestBodySize int    `json:"request_body_size"`
    // 新增字段
    DurationMs      int64  `json:"duration_ms,omitempty"`      // 请求耗时（毫秒）
    Attempt         int    `json:"attempt,omitempty"`           // 第几次尝试（1-based）
    Provider        string `json:"provider,omitempty"`          // provider 名称
}
```

**为什么是这 3 个**：
- `duration_ms` — 压测最核心指标，代码已有 `start` 计时器
- `attempt` — 区分一次成功 vs 重试成功，代码已有 `attempt` 变量
- `provider` — 多 provider 场景下日志归属，上下文已有 `ps.Name`

**不做的**：
- `response_body_size`：流式响应场景下 body 大小不确定，且修改涉及 streaming 路径，复杂度高
- `error_type`：当前错误分类已在 ProxyEngine 层处理，且错误路径已有 `slog.Error` 输出，LogEntry 中再加会冗余
- `cb_state`：熔断器状态是动态的，日志快照无法反映实时状态，看 `/health` 端点更合适

---

### Change 2: 重试耗尽时记录日志

**文件**: `internal/server/handlers.go` — `executeProxy` 函数

当前逻辑（约第 362-365 行）：循环结束后，直接返回 503 错误响应，**没有调用 `pr.logs.Append()`**。

```go
// 改前：重试耗尽，无日志
writeProxyError(w, "上游请求失败，所有重试均已耗尽", http.StatusServiceUnavailable, ErrorExhaustedRetries)
return
```

```go
// 改后：重试耗尽，记录日志
pr.logs.Append(utils.LogEntry{
    Timestamp:       time.Now().Format(time.RFC3339),
    Key:             key,  // 最后一次尝试的 key
    KeyIndex:        idx + 1,
    KeyName:         pool.Name(idx),
    Method:          r.Method,
    URL:             target,
    Status:          http.StatusServiceUnavailable,
    RequestBodySize: len(bodyBytes),
    DurationMs:      time.Since(start).Milliseconds(),
    Attempt:         attempt + 1, // 实际尝试次数（MaxRetries）
    Provider:        ps.Name,
})
```

**为什么重要**：当前重试耗尽路径是"静默失败"——用户看到 503，但日志里查不到对应记录，无法排查。

---

### Change 3: 更新 3 个现有 LogEntry 构造点

**文件**: `internal/server/handlers.go` — `executeProxy` 函数

3 个现有的 `pr.logs.Append()` 调用点（约 299、312、355 行）都加上 3 个新字段：

```go
pr.logs.Append(utils.LogEntry{
    Timestamp:       time.Now().Format(time.RFC3339),
    Key:             key,
    KeyIndex:        idx + 1,
    KeyName:         pool.Name(idx),
    Method:          r.Method,
    URL:             target,
    Status:          resp.StatusCode,
    RequestBodySize: len(bodyBytes),
    DurationMs:      time.Since(start).Milliseconds(),  // 新增
    Attempt:         attempt + 1,                        // 新增
    Provider:        ps.Name,                            // 新增
})
```

**注意**：`DurationMs` 在每个 log 点都可用（`start` 在函数顶部定义），`attempt` 在循环内可用，`ps.Name` 在 `executeProxy` 签名中可用。

---

### Change 4: CLI 展示新增字段

**文件**: `internal/cmd/logs.go` — `logsCmd.RunE`

当前展示格式：
```
  [timestamp] METHOD URL -> STATUS
```

改造为在 verbose 模式下展示更多字段，默认模式保持简洁：

```go
// 默认模式：简洁
fmt.Printf("  [%s] %s %s -> %d\n", ts, method, path, status)

// 如果想展示更多信息，可以改为：
// 格式: [timestamp] PROVIDER METHOD URL -> STATUS (attempt/N, duration_ms)
// key_index 和 key_name 在非 verbose 模式下不展示，避免信息过载
```

**具体改动**：在 `entry` 循环中，除了现有 4 个字段，尝试读取 `duration_ms`、`attempt`、`provider` 并展示：

```
  [18:16:15] sensenova POST /v1/messages -> 200 (attempt 1/3, 2340ms, key: 默认)
```

**为什么不做单独列**：`alvus logs` 是 tail 式的日志查看，不适合表格布局。在每行尾部加括号补充信息更紧凑。

---

### 完整改动范围

| 文件 | 改动 | 行数 |
|------|------|------|
| `internal/utils/utils.go` | LogEntry 新增 3 个字段 | ~5 行 |
| `internal/server/handlers.go` | 3 个现有 log 点 + 1 个新增 log 点，各加 3 个字段 | ~20 行 |
| `internal/cmd/logs.go` | CLI 展示新增字段 | ~5 行 |

**总计约 30 行改动，零新依赖，零新文件。**

---

## 假设与决策

- **不做 LogStore 持久化** — 内存 ring buffer 对压测排查足够
- **不做 `response_body_size`** — 流式场景下复杂度高，收益有限
- **不做字段过滤** — 新增字段用 `omitempty`，向后兼容
- **不做 Dashboard 展示增强** — 当前 Dashboard 够用
- **不修改 Prometheus 指标** — 已有 `RequestDuration` 直方图，无需重复

---

## Spec 安排

分析：4 个 Change 沿同一数据流路径（struct → handler 写入 → CLI 读取），严格按序依赖，合并为 **1 个 spec**。

### Spec: `enhance-log-entries`

**范围**：Change 1~4，覆盖 LogEntry 结构体扩展 + handler 写入 + CLI 展示全路径。

**任务分解**：

| 任务 | 内容 | 依赖 |
|------|------|------|
| T1 | LogEntry 结构体新增 `duration_ms` / `attempt` / `provider` 字段 | 无 |
| T2 | handler 写入：3 个现有 log 点加字段 + 重试耗尽新增 log 点 | T1 |
| T3 | CLI 展示 `alvus logs` 显示新字段 | T1 |
| T4 | 测试覆盖：单元测试 + 集成验收测试 | T2 + T3 |
| T5 | TODO.md 更新 + 提交 | T4 |

### TODO

- [ ] 创建 spec 文件（spec.md / tasks.md / checklist.md）→ 本文件完成后立即执行
- [ ] T1: LogEntry 结构体字段扩展
- [ ] T2: Handler 写入更新
- [ ] T3: CLI 展示增强
- [ ] T4: 测试覆盖
- [ ] T5: TODO.md + 提交

1. `go build ./cmd/alvus/` 编译通过
2. `go vet ./...` 零警告
3. `go test -race ./...` 全量回归通过
4. 启动后发请求，`curl /logs` 确认 `duration_ms`、`attempt`、`provider` 字段出现
5. 模拟重试耗尽（关掉所有 Key），确认 503 响应有对应的日志记录
6. `alvus logs` CLI 输出显示新字段