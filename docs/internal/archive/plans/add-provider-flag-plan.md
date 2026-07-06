# Plan: 添加 `--provider` 启动过滤参数

## 摘要

给 `alvus start`（以及裸 `alvus` 命令）增加 `--provider <name>` 参数，只启动指定的 provider，不修改 config.toml。

## 当前状态分析

- `alvus start` 读取 config.toml 后**全量启动**所有 provider，无法只启动其中一个
- 三种 provider（NVIDIA port 4000、Agnes port 4001、SenseNova port 4002）已配好，用户只想跑 SenseNova
- `--local`、`--network-only`、`--tag` 已在 `rootCmd.PersistentFlags()` 定义，子命令继承
- `startServer(dashHTML, local, networkOnly, tag)` 函数签名不含 provider 过滤参数
- 关键过滤点：`start.go:62` — `for name, cfg := range providers {}`

## 改动方案

### 修改 1：`internal/cmd/root.go`

**新增** `--provider` persistent flag（与 `--local` 同一模式）：

```go
rootCmd.PersistentFlags().String("provider", "", "Only start the specified provider")
```

### 修改 2：`internal/cmd/start.go`

**a)** 给 `startServer` 增加 `providerFilter string` 参数：

```go
func startServer(dashboardHTML string, isLocal, isNetwork bool, processTag, providerFilter string)
```

**b)** 在 TOML provider 循环处（第 62 行）加过滤：

```go
for name, cfg := range providers {
    if providerFilter != "" && name != providerFilter {
        slog.Debug("skipping provider (filtered)", "name", name)
        continue
    }
    // ... 现有代码 ...
}
```

**c)** `startCmd.Run` 读取 flag 并传入：

```go
providerFilter, _ := cmd.Flags().GetString("provider")
startServer(dashHTML, local, networkOnly, tag, providerFilter)
```

### 修改 3：`internal/cmd/root.go`

`rootCmd.Run` 同样读取并传入 `--provider` 值（支持裸 `alvus --provider sensenova`）

### 修改 4：测试

`start_cmd_test.go` 新增一个测试：`TestStartCmd_ProviderFilter`：
- 用 `provider add` 创建两个 provider（test-a, test-b），各带 key
- 子进程 `alvus start --local --provider test-a`
- 验证 test-a 端口可达，test-b 端口不可达

`internal/server/manager_test.go` 无需改动（InstanceManager 层面不感知过滤，过滤在 startServer 层）

## 向后兼容

- `--provider` 留空 = 全量启动，行为不变
- `.env` 单 provider 模式不涉及过滤（没有多个 provider 可以选）

## 验证步骤

1. `go build ./cmd/alvus/` 编译通过
2. `go vet ./...` 零警告
3. `go test -race ./...` 全部通过
4. `alvus start --local --provider doesn-exist` → 启动后 InstanceManager 无实例 → 退出
5. `alvus start --local --provider sensenova` → 只启动 sensenova
