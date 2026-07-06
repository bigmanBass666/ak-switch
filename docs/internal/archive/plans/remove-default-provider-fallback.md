# 移除 `default_provider` 向后兼容 fallback

## Summary

删除 `akswitch start` 在 `default_provider` 未设置时隐式启动所有 provider 的 fallback 逻辑，改为明确报错退出。这是纯个人使用项目的清理工作，消除条件反射式编程带来的无用代码。

## 现状分析

### `internal/cmd/start.go:74-79` — 问题代码

```go
shouldStart := true
if providerFilter != "" {
    shouldStart = (name == providerFilter)
} else if !startAll && config.DefaultProviderName != "" {
    shouldStart = (name == config.DefaultProviderName)
}
```

当 `providerFilter == ""`、`startAll == false`、`DefaultProviderName == ""` 三者同时成立时，`shouldStart` 保持 `true`（行 74 的初始化值），导致所有 provider 启动。这是无意义的"防 surprise"逻辑——项目唯一用户不会被 surprise。

### 受影响文件

| 文件 | 影响 |
|------|------|
| `internal/cmd/start.go` | 过滤逻辑 + --all flag help 文本 |
| `internal/cmd/root.go` | 无影响（只是传递参数） |
| `start_cmd_test.go` | 两个测试需要补 default_provider 否则出错 |
| `docs/cli-reference.md` | 行为描述需更新 |
| `docs/configuration.md` | default_provider 字段说明需更新 |

### 测试影响分析

| 测试 | flag/配置 | 不受影响？ |
|------|-----------|-----------|
| `TestStartCmd_TOMLMode` | 无 flag，无 default_provider | ❌ 需要加 `default_provider = "testp"` |
| `TestStartCmd_NoKeys` | 无 flag，无 default_provider | ❌ 需要加 `default_provider = "nokey"` |
| `TestStartCmd_ProviderFilter` | `--provider test-a` | ✅ |
| `TestStartCmd_AllFlag` | `--all` | ✅ |
| `TestStartCmd_DefaultProvider` | config 中已设 default_provider | ✅ |

## 改动方案

### 1. `internal/cmd/start.go` — 过滤逻辑改为明确三选一

**行 74-79 替换为：**

```go
// 三选一：--provider > --all > default_provider，无隐式 fallback
var shouldStart func(name string) bool
switch {
case providerFilter != "":
    shouldStart = func(name string) bool { return name == providerFilter }
case startAll:
    shouldStart = func(name string) bool { return true }
case config.DefaultProviderName != "":
    shouldStart = func(name string) bool { return name == config.DefaultProviderName }
default:
    slog.Error("default_provider not set. Use --provider <name>, --all, or set default_provider in config.toml")
    os.Exit(1)
}
```

将每个 provider 的判断改为 `if !shouldStart(name) { continue }`。

**为什么是 switch 而不是 if-else：** 三个分支互斥且地位对等，switch 比嵌套 if-else 更容易一眼看出是"三选一"。

**行 237 — --all flag help 文本：**

```
"Start all providers (default: only default_provider, or error if unset)"
```

### 2. `start_cmd_test.go` — 两个测试补 default_provider

**`TestStartCmd_TOMLMode`（约行 31-39）：** 在 `provider add` 和 `key add` 之后、子进程启动之前，往 config.toml 追加 `default_provider = "testp"`。

**`TestStartCmd_NoKeys`（约行 90-99）：** 同样在 `provider add` 之后、子进程启动之前，追加 `default_provider = "nokey"`。

追加方式（两个测试通用，参考 `TestStartCmd_DefaultProvider` 行 302-309 的已有模式）：
```go
configPath := filepath.Join(tmpDir, "config.toml")
data, err := os.ReadFile(configPath)
// ... error handling ...
newContent := "default_provider = \"testp\"\n" + string(data)
os.WriteFile(configPath, []byte(newContent), 0644)
```

### 3. `docs/cli-reference.md` — 同步更新行为描述

**行 19：**
```
akswitch start                    # 启动 default_provider 指定的 provider（未设置则报错退出）
```

**行 25：**
```
- 默认只启动 `default_provider` 指定的 provider（未设置则报错，用 `--all` 或 `--provider` 启动）
```

### 4. `docs/configuration.md` — 同步更新字段说明

**行 25 default_provider 说明：**
```
| `default_provider` | 否 | `""` | 默认启动的 provider 名称。设置后 `akswitch start` 只启动此 provider；未设置时需用 `--all` 或 `--provider` 启动 |
```

## 验证步骤

1. `go build ./...` 编译通过
2. `go test ./internal/...` 全部通过（包括 4 个新测试）
3. `go test -run TestStartCmd` 验收测试通过
4. 实际运行验证：`akswitch start`（无 default_provider）→ 输出错误消息并退出
5. `akswitch start --all` → 正常启动全部
6. `akswitch start --provider nvidia` → 正常启动指定 provider