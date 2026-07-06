# 彩色日志输出 — 实现方案

## 概述

为 AK Switch 运行时日志添加 ANSI 彩色输出，零业务代码改动，零配置项，自动适配终端/管道。

## 当前状态分析

| 项目 | 现状 |
|------|------|
| 日志库 | `log/slog`（Go 标准库） |
| 输出目标 | `os.Stderr` |
| 当前 Handler | `slog.NewTextHandler(os.Stderr, ...)` — 纯文本，无色 |
| 初始化位置 | `internal/server/server.go:181` — `ApplyLogLevel()` 函数内 |
| 测试覆盖 | `logging_test.go` 测试 `ApplyLogLevel` 的 level 设置；`proxy_test.go` 自行替换全局 handler 验证结构化格式 |
| 外部依赖 | 零日志依赖 |
| `golang.org/x/term` | 不存在。`golang.org/x/sys`（间接依赖，由 Prometheus 引入）已存在 |

### 关键发现

1. **所有日志调用都走全局 `slog.Xxx()`** — `slog.Info()`, `slog.Warn()`, `slog.Error()`, `slog.Debug()`。无一例外。
2. **测试已隔离 handler** — `proxy_test.go` 用 `slog.SetDefault(slog.New(slog.NewTextHandler(&buf, ...)))` 覆盖全局 handler，不影响测试。
3. **`logging_test.go` 只测试 `ApplyLogLevel` 的 level 启用性**，不测试 handler 类型。
4. **跨平台需求** — 用户运行在 Windows 11 (Windows Terminal)，ANSI 原生支持。

## 设计方案

### 总变更：3 个文件

```
internal/server/colorhandler.go   ← 新增，~90 行
internal/server/server.go         ← 改 1 行
go.mod / go.sum                   ← 加 1 依赖
```

### 文件 1：`internal/server/colorhandler.go`（新增）

**职责**：实现 `slog.Handler` 接口的 ANSI 彩色包装器。

```
newHandler(w io.Writer, lvl slog.Level) slog.Handler
    ├── w 是 TTY（终端）→ 返回 ColorHandler（ANSI 彩色）
    └── w 不是 TTY（文件/管道）→ 返回 slog.NewTextHandler（原始行为）
```

**彩色输出示例**：
```
02:50:23.408  INFO  server started                  addr=:8080  providers=3    ← 🟢 绿色
02:50:23.645  WARN  key rate limited                status=429  cb_state=OPEN  ← 🟡 黄色
02:50:23.752  ERRO  failed to persist keys          error=permission denied    ← 🔴 红色
02:50:23.478  DEBU  key on cooldown                 key_index=3               ← ⚪ 灰色
```

**ColorHandler 实现结构**：

| 方法 | 行为 |
|------|------|
| `Enabled()` | 委托给 inner handler（filter by level） |
| `Handle()` | 格式化输出：`[灰色时间] [彩色LEVEL] [加粗消息] [灰色key=亮白value]` |
| `WithAttrs()` | 委托给 inner handler，维持链式调用 |
| `WithGroup()` | 委托给 inner handler |

**配色映射**：

| Level | 文字色 | 消息 | 时间 |
|-------|--------|------|------|
| DEBUG | 灰色 (90) | 灰色+加粗 | 灰色 |
| INFO | 绿色 (32) | 绿色+加粗 | 灰色 |
| WARN | 黄色 (33) | 黄色+加粗 | 灰色 |
| ERROR | 红色 (31) | 红色+加粗 | 灰色 |
| key=value | 灰色 key / 亮白 value | — | — |

**TTY 检测**：使用 `golang.org/x/term` 的 `term.IsTerminal(int(fd))`。

**NO_COLOR 支持**：检测环境变量 `NO_COLOR`（https://no-color.org/ 约定），若设置则降级为纯文本。这比 TTY 检测优先级更高——用户显式要求无色时即使终端也无色。

**调用者信息**：DEBUG 级别时启用 `slog.HandlerOptions.AddSource`，在消息前显示 `file:line`。INFO+ 级别不显示。

**依赖理由**：`golang.org/x/term` 是 Go 团队维护的标准扩展库，跨平台 TTY 检测的唯一可靠方案。`golang.org/x/sys` 已为间接依赖，新增 `x/term` 仅增加约 2KB 编译后体积。

### 文件 2：`internal/server/server.go`（改 1 行）

**位置**：`ApplyLogLevel()` 函数，第 181 行

**变更**：
```go
// 改前：
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))

// 改后：
slog.SetDefault(slog.New(newHandler(os.Stderr, lvl)))
```

`newHandler` 的 TTY 检测在内部自动完成，不暴露给调用者。

### 文件 3：`go.mod`（加 1 依赖）

运行 `go get golang.org/x/term` 后自动更新 `go.mod` 和 `go.sum`。

### 不做什么

- ❌ 不加 `log.color` 配置项
- ❌ 不改任何 `slog.Info/Warn/Error/Debug` 调用
- ❌ 不改测试文件
- ❌ 不重构现有日志系统
- ❌ 不加 caller 到 INFO+ 级别（只在 DEBUG 显示）

## 关键决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| TTY 检测依赖 | `golang.org/x/term` | 跨平台可靠，`x/sys` 已存在 |
| 配置项 | 零配置，自动检测 | 符合项目"不添投机性"哲学 |
| 测试影响 | 无 | 测试自行替换 handler |
| 部署影响 | 无 | 管道/文件重定向自动降级 |
| Worktree 策略 | Agent `isolation: "worktree"` | 不碰当前 feature 分支 |
| NO_COLOR 支持 | 有（环境变量检测） | 行业标准，零成本 |

## 验证步骤

1. **终端测试**：运行 `go run . start`，观察彩色输出
2. **管道测试**：运行 `go run . start 2>&1 | findstr INFO`，确认无色
3. **文件测试**：运行 `go run . start 2> log.txt && type log.txt`，确认无 ANSI 乱码
4. **NO_COLOR 测试**：运行 `set NO_COLOR=1 && go run . start`，确认无色
5. **DEBUG 测试**：运行 `go run . start --log-level debug`，确认 caller 显示
6. **回归测试**：运行 `go test ./internal/server/... -count=1`，全部通过
7. **全量测试**：运行 `go test ./... -count=1`，全部通过

## 关于 Worktree 的说明

实现使用 Agent 的 `isolation: "worktree"` 模式：
- Agent 在临时 worktree 中独立工作，基于 `main` 分支
- 当前 `feature/rename-to-akswitch` 分支完全不受影响
- 完成后 worktree 自动清理，**不自动合并任何内容**
- 如果实现结果可用，需要你决定如何合并（另行开 PR 或 cherry-pick）

---

*计划版本：v1 · 2026-07-04*