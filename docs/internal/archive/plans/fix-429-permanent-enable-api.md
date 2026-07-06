# 修复 429 永久禁用 + 加手动 Enable 接口

> 计划日期: 2026-07-04

---

## Summary

两处核心代码改动（~50 行）+ 一处测试更新，解决"429 达到退避上限后 Key 被永久禁用，永不恢复"的问题，并添加手动解禁 API 端点。

---

## 当前状态分析

### 问题

`internal/circuitbreaker/key.go` 的 `RecordFailure()` 在指数退避 `raw >= backoffCap` 时，将 Key 置为 `StatePermanent`。随后 `handleRateLimited()` 检测到 `StatePermanent` 后调用 `pool.Disable()`，Key 从此永久禁用，没有任何机制能恢复。

默认参数下（CooldownSec=60, BackoffCapSec=120, Multiplier=2），**连续 2 次 429** 即触发永久禁用。

### 受影响的关键流程

```
executeProxy → 429 → handleRateLimited()
  ├── keyCBs[idx].RecordFailure()    ← 退避达到 cap → StatePermanent
  ├── pool.Cooldown(idx, d)
  └── StatePermanent? → pool.Disable(idx)  ← Key 死了
```

### 已有但缺失的部分

| 有 | 没有 |
|---|---|
| `KeyPool.Disable()` | `KeyPool.Enable()` |
| `KeyCircuitBreaker.RecordPerma()` | `KeyCircuitBreaker.Reset()` |
| `disableKeyHandler` HTTP handler | `enableKeyHandler` HTTP handler |
| `POST /api/keys/{index}/disable` 路由 | `POST /api/keys/{index}/enable` 路由 |

---

## 改动清单

### Change 1: 修复 RecordFailure() 永久禁用（~8 行）

**文件:** `internal/circuitbreaker/key.go:57-61`

**当前行为:**
```go
if raw >= k.backoffCap {
    k.state = StatePermanent       // ← 死的，永不恢复
    k.trippedReason = "quota_exhausted"
    return
}
```

**改为:**
```go
if raw >= k.backoffCap {
    k.state = StateOpen
    k.cooldownUntil = time.Now().Add(k.backoffCap)  // 冷却 backoffCap 时长
    k.attempt = 0                                   // 重置计数器，下一轮从头开始
    return
}
```

**设计决策:**
- 达到 cap 后冷却 `backoffCap` 时长（默认 120s），而非永久禁用
- `attempt` 归零，使下一轮的退避从 `base` 重新开始指数增长
- 401/403 路径不受影响——`RecordPerma()` 仍然设置 `StatePermanent`

### Change 2: 更新测试 TestReachesCap（~8 行）

**文件:** `internal/circuitbreaker/key_test.go:64-80`

- 预期状态从 `StatePermanent` 改为 `StateOpen`
- 验证 `CooldownRemaining()` 约为 `backoffCap`（120s）
- 验证 `Allow()` 返回 false（冷却中）

### Change 3: 新增 KeyCircuitBreaker.Reset()（~10 行）

**文件:** `internal/circuitbreaker/key.go`

新增方法:
```go
// Reset fully resets the breaker to closed state.
func (k *KeyCircuitBreaker) Reset() {
    k.mu.Lock()
    defer k.mu.Unlock()
    k.state = StateClosed
    k.attempt = 0
    k.cooldownUntil = time.Time{}
    k.trippedReason = ""
}
```

**原因:** 手动 Enable 时需要清除 KeyCircuitBreaker 的冷却状态。现有 `RecordSuccess()` 在 `StatePermanent` 上是 no-op（设计如此，用于防止 401/403 后被成功请求覆盖）。需要一个通用的 `Reset()` 方法。

### Change 4: 新增 KeyPool.Enable()（~12 行）

**文件:** `internal/keypool/keypool.go`

新增方法:
```go
// Enable re-enables a previously disabled key, clearing both the disabled flag
// and any active cooldown. No-op if the key is already enabled.
func (p *KeyPool) Enable(idx int) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    if idx < 0 || idx >= len(p.keys) {
        return fmt.Errorf("key index %d out of range (0-%d)", idx, len(p.keys)-1)
    }
    p.disabled[idx] = false
    p.cooldowns[idx] = time.Time{}  // clear cooldown
    return nil
}
```

与 `Disable()` 对称，逻辑相反。

### Change 5: 新增 enableKeyHandler（~15 行）

**文件:** `internal/server/handlers.go`

在 `disableKeyHandler` 后新增:
```go
func (pr *ProviderRouter) enableKeyHandler(w http.ResponseWriter, r *http.Request) {
    // 1. resolveProvider → 获取 ProviderState
    // 2. parseKeyIndex → 获取 key 索引
    // 3. ps.Pool.Enable(idx) → 清除禁用+冷却
    // 4. ps.Proxy.keyCBs[idx].Reset() → 清除 CB 状态
    // 5. ps.State.PersistKeys() → 持久化
    // 6. respondJSON 200
}
```

模式完全复用 `disableKeyHandler`（~20 行代码），核心差异是 `Enable()` + `Reset()` 而非 `Disable()`。

### Change 6: 注册路由（~1 行）

**文件:** `internal/server/manager.go:118`

在 `registerRoutes()` 中新增:
```go
mux.HandleFunc("POST /api/keys/{index}/enable", pr.enableKeyHandler)
```

放在 `disableKeyHandler` 旁边保持对称。

---

## 不变更的内容

| 不做 | 原因 |
|------|------|
| Half-Open 状态机 | 冷却到期后下个自然请求就是探针，换 Key 零成本不需要限制并发 |
| 双冷却系统简化（KeyPool + KeyCB 并行） | 不影响功能，改动有回归风险 |
| handleRateLimited 中 pool.Disable 路径 | 1. CB 不会再因 429 进入 StatePermanent，此分支不再执行<br>2. 保留代码作为防御性编程（防未来改动引入） |
| 其他 test 文件 | 无需改动，测试理论继续通过 |

---

## 验证步骤

1. `go build ./cmd/akswitch/` — 编译通过
2. `go vet ./...` — 无静态分析警告
3. `go test ./internal/circuitbreaker/` — KeyCB 单元测试通过（含 TestReachesCap 适配）
4. `go test ./internal/keypool/` — KeyPool 单元测试通过
5. `go test ./internal/server/` — Handler 测试通过
6. `go test ./... -count=1` — 全量测试通过（预存 flaky 除外）