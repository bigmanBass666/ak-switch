# `akswitch start` 无 default_provider 时默认启动第一个 provider

## Summary

移除 `akswitch start` 在 `default_provider` 未设置、也未指定 `--all`/`--provider` 时的报错退出行为，改为按 provider 名称字母序取第一个作为默认启动项。

## 现状分析

### 当前代码 (`internal/cmd/start.go:72-84`)

```go
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

default 分支直接报错退出，强制用户要么设 `default_provider`、要么用 `--all`/`--provider` 标志。

### 问题

此项目只有单一用户，存在 provider 配置而无 `default_provider` 时，直接报错对用户不友好。行业惯例（`git` 第一个 remote、`kubectl` 第一个 context、Docker 第一个 profile）是以第一个为默认。

### 关键信息

- `LoadAllTomlProviders()` 返回 `map[string]*Config`（`config.go:420`），map 遍历无序
- Go 1.23 标准库提供了 `maps.Keys` 和 `slices.Sorted`，可稳定排序 map keys
- `providers` 在上层已有非空检查（行 62-65），default 分支能安全取 `names[0]`

## 改动文件

| 文件 | 改动内容 |
|------|---------|
| `internal/cmd/start.go` | default 分支从 `os.Exit(1)` 改为取第一个 provider；新增 `maps` `slices` import |
| `start_cmd_test.go` | 移除 `TestStartCmd_TOMLMode` 和 `TestStartCmd_NoKeys` 中的 `default_provider` 注入 |
| `e2e_test.go` | 无变更（已在用 `--all`） |
| `docs/cli-reference.md` | 更新行为描述：未设 default_provider 时默认启动第一个 |
| `docs/configuration.md` | 更新 `default_provider` 说明 |
| `.agents/documents/remove-default-provider-fallback.md` | 计划文件无变更（归档参考） |

## 详细改动

### 1. `internal/cmd/start.go` — default 分支改为取第一个 provider

**行 72-84 替换为：**

```go
// 四选一：--provider > --all > default_provider > 第一个 provider（字母序）
var shouldStart func(name string) bool
switch {
case providerFilter != "":
    shouldStart = func(name string) bool { return name == providerFilter }
case startAll:
    shouldStart = func(name string) bool { return true }
case config.DefaultProviderName != "":
    shouldStart = func(name string) bool { return name == config.DefaultProviderName }
default:
    names := slices.Sorted(maps.Keys(providers))
    first := names[0]
    slog.Info("default_provider 未配置，默认使用第一个 provider", "provider", first)
    shouldStart = func(name string) bool { return name == first }
}
```

**新增 import：** 在 `import` 块中添加 `"maps"` 和 `"slices"`（注意是标准库的 maps 和 slices，Go 1.23+ 可用）。

**行 82 — 错误日志移除：** 原 `slog.Error(...)` + `os.Exit(1)` 不再需要。

**行 128-129 — --all flag help 文本：** 同步更新：
```
"Start all providers (default: first provider alphabetically, or error if none configured)"
```

### 2. `start_cmd_test.go` — 移除两个测试的 default_provider 注入

**`TestStartCmd_TOMLMode`（行 44-53）：** 删除从 "Add default_provider to config.toml" 到 `WriteFile` 的整个注入块。这个测试只有一个 provider "testp"，自然就是第一个启动项。

**`TestStartCmd_NoKeys`（行 114-123）：** 同样删除从 "Add default_provider to config.toml" 到 `WriteFile` 的整个注入块。

### 3. `docs/cli-reference.md` — 同步更新

**行 19：**
```
akswitch start                    # 启动第一个 provider（按名称字母序）
```

**行 25：**
```
- 默认启动按名称字母序的第一个 provider（可设 `default_provider` 指定、或 `--all` 启动全部）
```

### 4. `docs/configuration.md` — 同步更新字段说明

**行 25 default_provider 说明：**
```
| `default_provider` | 否 | `""` | 默认启动的 provider 名称。设置后 `akswitch start` 只启动此 provider；未设置时自动使用第一个 provider（按名称字母序）|
```

## 验证步骤

1. `go build ./cmd/akswitch/` — 编译通过
2. `go vet ./...` — 静态分析通过
3. `go test ./...` — 所有测试通过
   - `TestStartCmd_TOMLMode` — 无需 default_provider 注入也能通过
   - `TestStartCmd_NoKeys` — 同上
   - `TestStartCmd_DefaultProvider` — 仍按 `default_provider` 生效
   - `TestStartCmd_ProviderFilter` — `--provider` 不受影响
   - `TestStartCmd_AllFlag` — `--all` 不受影响
4. `go test -run TestFullE2E_RealUserSimulation` — E2E 通过
5. 实际运行验证：
   - 配置一个 provider 无 `default_provider` → `akswitch start` 应启动该 provider
   - 配置多个 provider 无 `default_provider` → 启动字母序第一个
   - 配置多个 provider 有 `default_provider` → 启动指定那个