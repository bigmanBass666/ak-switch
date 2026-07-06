# CLI 命令审查结果

审查日期：2026-07-05
审查方式：隔离配置目录 + 真实二进制逐个运行

---

## P0 — 行为错误 / 用户可见的 Bug

### P0-1：`akswitch`（无子命令）直接启动服务器

```
$ akswitch
time=... level=ERROR msg="failed to load providers from TOML" ...
```

**问题**：不带子命令时直接调用了 `startServer()`。规范做法是显示 help 或提示用户指定子命令。

**文件**：`internal/cmd/root.go:20` — `rootCmd.Run` 直接调 `startServer()`。

---

### P0-2：`akswitch --version` 报错

```
$ akswitch --version
Error: unknown flag: --version
```

**问题**：`--version` 是 CLI 通用惯例，当前不支持。只有 `akswitch version` 子命令可用。

**文件**：`internal/cmd/root.go:40-43` — `init()` 未设置 `rootCmd.Version`。

---

### P0-3：移除 default provider 后 `default_provider` 残留

```toml
default_provider = 'e2e'   # ← 残留，引用不存在的 provider
```

**复现步骤**：`provider default e2e` → `provider remove e2e` → `default_provider` 未清除。

**问题**：配置指向不存在的 provider，`start` 时会尝试启动不存在的 provider。

**文件**：`internal/cmd/provider.go` — `providerRemoveCmd.RunE` 未处理 `DefaultProvider` 清理。

---

### P0-4：`akswitch status` 服务器未运行时错误信息无意义

```
$ akswitch status
Error: failed to parse health response: invalid character '<' looking for beginning of value
```

**问题**：端口上有其他服务返回了 HTML（或连接被重置等），用户完全看不出是"服务器未运行"。

**根因**：`status.go:38-40` 仅检查 `err != nil`，但 HTTP 请求成功、响应是 HTML 时进入 JSON parse 分支。

**文件**：`internal/cmd/status.go:38-46`

---

### P0-5：`akswitch logs` 服务器未运行时同样的无意义错误

```
$ akswitch logs
Error: failed to parse logs: invalid character '<' looking for beginning of value
```

**问题**：同上，应给出"服务器未运行"提示。

---

## P1 — 体验问题 / 设计问题

### P1-1：根命令 help 中 `--all` / `--provider` 让人困惑

```
$ akswitch --help
Flags:
      --all               Start all providers ...
      --provider string   Only start the specified provider
```

**问题**：`--all` 和 `--provider` 是 `start` 的专属 flag，出现在根命令 help 中误导用户。原因是 rootCmd 的 `Run` 直接调用了 `startServer`，不得不把这两个 flag 放根命令。

**文件**：`internal/cmd/root.go:41-42`

---

### P1-2：`akswitch version` 输出 `unknown`

```
$ akswitch version
akswitch version unknown
```

**问题**：无版本号至少应改为 `akswitch (dev)` 或者隐含版本号。当前输出让用户觉得软件没有版本管理。

**文件**：`internal/cmd/root.go:31`

---

### P1-3：启动期日志格式不一致

启动早期用标准库 `log` 包，后期用 `slog` 结构化格式：

```
# 早期（标准 log 包）
2026/07/05 19:21:28 INFO default_provider 未配置，默认使用第一个 provider provider=e2e

# 后期（slog）
time=2026-07-05T19:21:28.064+08:00 level=INFO msg="provider configured" name=e2e
```

**问题**：同一次启动中两种格式混用，不专业。甚至同一个命令的不同输出路径也混用（例：`start --provider nonexistent` 第一行是标准 log 格式，第二行是 slog 格式）。

---

### P1-4：报错时同时显示 Error + Usage，冗余

```
Error: provider "nonexistent" not found in C:\...\config.toml
Usage:
  akswitch provider remove <name> [flags]
...
```

**问题**：所有错误路径 Cobra 都自动追加 Usage 输出。对于用户输入错误可接受，但对于运行时错误（如 provider 不存在、文件找不到）显示 Usage 是多余噪音。但这个是 Cobra 默认行为，需要主动配置 `SilenceUsage: true` 并手动在 RunE 中处理。

---

### P1-5：PID 文件路径是 CWD，非隔离

```
$ ls ./akswitch.pid
akswitch.pid
```

**问题**：PID 写入当前工作目录。多终端同时使用时会冲突；从不同目录运行 `stop` 无法找到 PID 文件。应写入标准运行时目录（如 `$XDG_RUNTIME_DIR` 或配置目录）。

**文件**：`internal/cmd/start.go:13` — `var PidFileName = "akswitch.pid"`（硬编码相对路径）

---

## P2 — 小问题 / 可改进点

### P2-1：`provider list` 不标注哪个是 default provider

```
Providers (from ...):
  NAME         TARGET     PORT
  example-a    ...        8080    ← default 无标记
  example-b    ...        8080
```

用户查看列表时无法知道哪个 provider 会被 `start` 使用。

---

### P2-2：`key list` 无 provider 参数时直接报错

```
$ akswitch key list
Error: accepts 1 arg(s), received 0
```

可改进为列出所有 provider 的 key 概览，但非必须。

---

### P2-3：TOML 中 `default_provider` 用单引号

```toml
default_provider = 'e2e'
```

TOML 规范允许单引号（字面字符串），但多数编辑器/工具使用双引号。这是 TOML 库的默认选择，无功能问题但略显突兀。

---

### P2-4：`akswitch help` 与 `akswitch --help` 行为不一致

```
$ akswitch help      # 正常显示 help
$ akswitch --help    # 正常显示 help，但还触发 start 逻辑？
```

需要确认 `--help` 是否仍隐含 `startServer` 副作用（Cobra 的 `--help` 在调用 `Run` 前就拦截了，所以实际不会。但用户可能疑惑）。

---

## 总结

| 等级 | 数量 |
|------|------|
| P0（缺陷） | 5 |
| P1（体验/设计） | 5 |
| P2（小问题） | 4 |
| 合计 | 14 |

这些是我建议现在讨论解决的问题。