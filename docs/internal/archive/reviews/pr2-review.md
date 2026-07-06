# PR #2 审查报告：结构化日志迁移 (`feature/structured-logging → main`)

**审查日期：** 2026-06-24  
**审查范围：** `log.Printf` → `slog` 结构化日志迁移  
**严重度定义：** HIGH = 功能性/正确性问题；MEDIUM = 设计/健壮性问题；LOW = 清理/微小改进

---

## 严重度分级

| 级别 | 数量 |
|------|------|
| HIGH | 2 |
| MEDIUM | 2 |
| LOW | 1 |

---

## HIGH — 必须修复

### H1. `TestProxySlogOutput` 的 `defer` 未正确恢复全局 `slog` 默认 Logger

**文件：** `proxy_test.go:707-709`  
**问题：** 测试通过 `slog.SetDefault` 将全局 Logger 替换为写入 `buf` 的 handler，但 `defer` 的恢复逻辑有误：

```go
handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
slog.SetDefault(slog.New(handler))
defer slog.SetDefault(slog.New(slog.Default().Handler())) // ← 未正确恢复
```

`slog.Default()` 返回当前被设置的默认 Logger（即写入 `buf` 的那个），而 `slog.Default().Handler()` 返回该 Logger 持有的 handler（即 `TextHandler{&buf}`）。所以 `defer` 再次设置了一个 **同样写入 `buf` 的新 Logger**，而不是恢复测试前写入 stderr 的原始默认 Logger。

**影响：** 该测试执行后，任何后续代码调用 `slog.Default()` / `slog.Info()` 得到的 Logger 仍然将输出写入测试的 `buf`，原始 stderr 输出被静默吞掉。虽然当前无 `t.Parallel()` 测试并行执行，但若未来添加并行测试或在测试后调试时，日志丢失会造成混淆。

**建议：** 预先捕获原始 handler 并在 `defer` 中恢复：

```go
origHandler := slog.Default().Handler()
t.Cleanup(func() { slog.SetDefault(slog.New(origHandler)) })
```

---

### H2. 启动日志中 `"version"` 字段格式错误

**文件：** `main.go:647-651`  
**问题：** `tagSuffix` 保留了旧的 Printf 风格格式（方括号+前导空格），并作为 `"version"` 字段输出：

```go
tagSuffix := ""
if *processTag != "" {
    tagSuffix = fmt.Sprintf(" [tag=%s]", *processTag)     // ← Printf 残留
}
slog.Info("starting", "version", tagSuffix, ...)           // ← 字段名"version"语义错误
```

日志输出示例：
```
INFO starting version=" [tag=v1.2]" port=8080 keys=3 ...
```

**问题一：** `tagSuffix` 若为空时 `"version"` 字段值为 `""`，产生无意义字段。  
**问题二：** `tagSuffix` 非空时带前导空格和方括号，是 `fmt.Sprintf` 的残留格式，不适合结构化日志。  
**问题三：** 字段名 `"version"` 实际存放的是 `processTag`，语义错误且混淆。

**建议：** 消除 `tagSuffix`，改用直接字段：

```go
if *processTag != "" {
    slog.Info("starting", "tag", *processTag, "port", cfg.Port, "keys", len(pool.Keys()), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
} else {
    slog.Info("starting", "port", cfg.Port, "keys", len(pool.Keys()), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
}
```

或也可将 `tag` 作为可选的额外字段附加（如 JSON 日志场景）。

---

## MEDIUM — 建议修复

### M1. 子进程 stderr 输出使用 `Info` 级别，应改为 `Warn`

**文件：** `manage.go:139`  
**问题：** 子进程的 stderr 扫描器输出使用了 `slog.Info`：

```go
slog.Info("child stderr", "instance", m.Name, "line", scanner.Text())
```

原代码使用 `⚠️` 警告 emoji 前缀（`log.Printf("⚠️ [%s] %s", m.Name, scanner.Text())`），暗示范畴应更偏向警告而非信息。子进程的 stderr 输出通常代表潜在问题（错误、warning、panic），使用 `Info` 级别会导致运维人员在过滤 `Warn+` 日志时错过这些信号。

**建议：** 改为 `slog.Warn` 以保持与原始语义的一致性：

```go
slog.Warn("child stderr", "instance", m.Name, "line", scanner.Text())
```

---

### M2. `TestProxySlogOutput` 断言 `key_index=0` 耦合于内部实现

**文件：** `proxy_test.go:738`  
**问题：** 测试断言第一个使用的 key index 为 0：

```go
for _, key := range []string{"method=GET", "url", "status=200", "key_index=0"} {
```

该断言依赖 `pool.Next()` 的 round-robin 实现在首次调用时返回 index 0。虽然当前实现确实如此（`counter` 从 0 开始），但这是一个内部实现细节，而非 API 契约。若后续修改 key 选择逻辑（如随机选择、基于优先级的选取、Cold-start 策略），该测试将无故失败。

**建议：** 降低对 `key_index` 值的精确断言，只检查 key_index 存在且格式正确：

```go
// 仅断言 key_index 存在，不校验具体值
if !strings.Contains(output, "key_index=") {
    t.Error("expected key_index field in output")
}
```

或使用正则匹配 `key_index=\d+`。

---

## LOW — 可选择性改进

### L1. `manage.go:129,141` 中 `scanner.Err()` 重复调用

**文件：** `manage.go:129-130, 141-142`  
**问题：** `if err := scanner.Err(); err != nil` 判断后，错误值再通过 `scanner.Err()` 传入 slog：

```go
if err := scanner.Err(); err != nil {
    slog.Error("stdout scanner error", "instance", m.Name, "error", scanner.Err())
}
```

`scanner.Err()` 被调用了两次但返回相同值。这不是 bug，只是微小冗余。

**建议：** 复用 err 变量：

```go
if err := scanner.Err(); err != nil {
    slog.Error("stdout scanner error", "instance", m.Name, "error", err)
}
```

---

## 验证通过的检查项

| 检查项 | 结果 |
|--------|------|
| 所有 `log.Printf` 已迁移 | 通过 — 源码中无残留 `log.Printf` |
| `log.Fatalf` 处理正确 | 通过 — 每个 `log.Fatalf` 前均有 `slog.Error` |
| 关键字段完整性（proxyHandler） | 通过 — method/url/status/key_index/attempt 均存在 |
| 所有 `slog` 字段使用 key-value 对 | 通过 — 无孤立的非字面量参数 |
| 无数据竞争（t.Parallel） | 通过 — 所有测试顺序执行 |

---

## 总结

PR 整体质量良好，`log.Printf` 到 `slog` 的迁移干净完整。发现 **2 个 HIGH** 问题（测试的 `slog.SetDefault` 恢复错误 + 启动日志 `tagSuffix` 残留格式）、**2 个 MEDIUM** 问题（子进程 stderr 级别 + 测试断言脆弱性）、**1 个 LOW** 问题（冗余 scanner.Err 调用）。建议至少修复 H1 和 H2 再合并。