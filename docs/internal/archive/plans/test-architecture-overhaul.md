# 测试架构改造规划

## 目标

1. 将测试按运行速度与职责分层（unit / integration / e2e）
2. 补上缺失的 CLI 输出断言面（捕获之前发现的 14 个问题及同类问题）
3. 用 Makefile + build tag 把分层编码到项目中，后续 AI 自动对齐
4. CI 各层独立运行，快层不阻塞、慢层不浪费

---

## 现状（来自实际文件审查）

### 测试分布

| 层级 | 位置 | 文件 | 行数 |
|------|------|------|------|
| 单元测试 | `internal/**/*_test.go` | 14 个 | ~4000 |
| CLI 集成（`package main`） | 根目录 | 13 个 | ~17000 |
| 子进程 E2E | 根目录（`*_test.go` 内） | 分散 | — |
| 手动回归脚本 | `scripts/regression_test.ps1` | 1 个 | ~700 |

### 关键依赖

- `proxy_test.go`、`handlers_test.go`、`healthcheck_test.go` 依赖 `package main` 级辅助函数 `setupServer()` → 必须留在根目录
- `config_cmd_test.go`、`provider_cmd_test.go`、`key_cmd_test.go` 仅依赖 `internal/cmd.Execute()` + `internal/config` → 可迁入 `internal/cmd/`
- `start_cmd_test.go` 需要 `package main`（子进程模式中 `ALVUS_TEST_START_CHILD` 分支调用 `cmd.Execute()`）→ 必须留在根目录
- `runCommand()` 定义在 `provider_cmd_test.go`，被 `key_cmd_test.go`、`config_cmd_test.go`、`start_cmd_test.go` 共用 → 迁移需提取公共 helper

### 现有 CI（`.github/workflows/go.yml`）

```
go vet ./...
go test -race ./...                 ← 所有测试混跑
scripts/regression_test.ps1         ← PowerShell 回归，只测旧模式
docker build .
```

---

## 改造方案

### Step 1：添加 `//go:build` 分层标签（改造最少，价值最大）

对所有测试文件加 build tag，**不做目录迁移**。目录迁移是锦上添花，build tag 是核心。

**规则：**

| Tag | 条件 | 速度目标 | 允许做什么 |
|-----|------|---------|-----------|
| `unit` | 纯逻辑，零 IO | < 1s | TOML 解析、key 轮转算法、熔断器状态机、字符串处理 |
| `integration` | 需 CLI 命令调用（`cmd.Execute()`）或 mock HTTP | < 10s | `runCommand()` 模式、httptest.Server |
| `e2e` | 需真实子进程、端口绑定、完整生命周期 | < 2m | `exec.Command` 启动二进制、`go build` |

**具体分配：**

```
//go:build unit
  internal/config/config_test.go
  internal/keypool/crypto_test.go
  internal/keypool/keypool_test.go
  internal/keypool/store_test.go
  internal/circuitbreaker/key_test.go
  internal/circuitbreaker/upstream_test.go
  internal/logstore/logstore_test.go
  internal/server/colorhandler_test.go
  internal/server/crash_test.go
  internal/server/error_classification_test.go
  internal/server/filehandler_test.go
  internal/server/logging_test.go
  internal/server/manager_test.go
  internal/cmd/status_cmd_test.go     ← 纯状态推理逻辑

//go:build integration
  config_cmd_test.go                    ← 根目录，package main
  provider_cmd_test.go
  key_cmd_test.go
  start_cmd_test.go
  proxy_test.go
  handlers_test.go
  healthcheck_test.go
  metrics_verification_test.go
  logstore_test.go
  docker_compose_test.go

//go:build e2e
  e2e_test.go
  graceful_shutdown_test.go
```

### Step 2：新建 CLI Snapshot 测试（核心价值）

创建 `cli_snapshot_test.go`（`//go:build integration`），对所有 CLI 命令的输出做结构性断言。

**测试路径：**

| 测试函数 | 验证点 |
|---------|--------|
| `TestCLI_Root_NoArgs` | 无参数不启动服务器，显示 help |
| `TestCLI_Root_Help` | help 输出不含 `--provider`、`--all` |
| `TestCLI_Version` | `--version` 正常工作 |
| `TestCLI_ProviderList_Format` | 表格对齐、含 NAME/TARGET/PORT 列 |
| `TestCLI_ProviderList_DefaultMark` | default provider 有标记 |
| `TestCLI_ConfigView` | 显示所有字段 |
| `TestCLI_Status_NotRunning` | 错误信息含"服务器未运行" |
| `TestCLI_Stop_NoPID` | 错误信息含"未找到 PID"或类似 |
| `TestCLI_Logs_NotRunning` | 错误信息含"服务器未运行" |
| `TestCLI_ProviderRemove_Default` | 移除 default provider 后 `default_provider` 被清除 |
| `TestCLI_InvalidCommand` | 非法子命令的错误提示友好 |

**断言策略：** 不用 golden file（文件多了没人更新），改为**关键文本断言**：

```go
func assertOutputContains(t *testing.T, output string, fragments []string) { ... }
func assertOutputNotContains(t *testing.T, output string, fragments []string) { ... }
```

通过检查：

| 检查项 | 示例 |
|--------|------|
| 必须包含的文本 | `"Usage:"`, `"NAME"`, `"TARGET"` |
| 一定不能出现的文本 | `"failed to start server"`, `"unknown flag"` ,标准库 `log` 格式 (`^\d{4}/\d{2}/\d{2}`) |
| 错误信息语义 | `"not running"`, `"not found"`, `"already exists"` |
| 无意外 panic | `recover()` 包裹 |

### Step 3：提取公共测试 helper

当前问题：`runCommand()` 定义在 `provider_cmd_test.go`，所有 CLI 测试共用。

在 `internal/cmd/testhelper_test.go` 中提取：

```go
//go:build integration

package cmd

// RunCommand executes akswitch with the given args.
func RunCommand(t testing.TB, args ...string) error {
    // 统一 os.Args 管理 + stdout/stderr 捕获 + 状态恢复
}
```

然后根目录的 `*_cmd_test.go` 改为：
```go
import "akswitch/internal/cmd"
// 删除本地的 runCommand，统一用 cmd.RunCommand()
```

**注意**：`resetConfigEnv()` 仍留在 `integration_test.go`（根目录），因为需要同时清理 `config.DefaultProviderName` 和环境变量。

### Step 4：创建 Makefile

```makefile
.PHONY: test-unit test-integration test-e2e test-all

test-unit:
    go test -tags=unit -count=1 -short ./internal/...

test-integration:
    go test -tags=integration -count=1 -race ./...

test-e2e:
    go test -tags=e2e -count=1 -timeout=5m -race ./...

test-all: test-unit test-integration test-e2e
```

### Step 5：更新 CI

```yaml
jobs:
  unit:
    runs-on: windows-latest
    steps:
      - run: go test -tags=unit -count=1 -short ./internal/...

  integration:
    needs: unit
    runs-on: windows-latest
    steps:
      - run: go vet ./...
      - run: go test -tags=integration -count=1 -race ./...

  e2e:
    needs: integration
    runs-on: windows-latest
    steps:
      - run: go test -tags=e2e -count=1 -timeout=5m -race ./...

  docker:
    needs: integration
    runs-on: ubuntu-latest
    steps:
      - run: docker build .
```

CI 流程变为：
```
unit（秒级） → integration（10秒级） → e2e（分钟级）
```

任一失败，后续不跑。不浪费时间。

### Step 6：更新 CLAUDE.md

在 CLAUDE.md 中追加（不删除现有内容）：

```markdown
## 测试规范

测试按速度分层，用 `//go:build` 标签区分：

| 标签 | 速度 | 覆盖范围 |
|------|------|---------|
| `unit` | ≤1s | 纯逻辑、无 IO |
| `integration` | ≤10s | CLI 命令、mock HTTP |
| `e2e` | ≤2m | 子进程、端口绑定 |

### 新增测试文件的规则

1. 先判断所属层级，加对应 `//go:build` 标签
2. CLI 命令测试必须包含输出断言（`assertOutputContains`）
3. 禁止无输出断言的 `runCommand` 模式（只测不崩不算测完）
```

---

## 不做的几件事

1. **不做目录大迁移**。文件位置是次要的，build tag + Makefile + CI 分层已经把核心约束编码了。待后续逐步优化。
2. **不引入第三方测试框架**（testify、ginkgo）。标准库够用，降低所有 AI 的接入门槛。
3. **不把 `scripts/regression_test.ps1` 转 Go。** PowerShell 回归脚本测的是旧模式（`.env` / `--manage`），这些已经不是主要配置路径。暂时不动，后续随旧模式一起弃用。
4. **不在本轮修复 14 个 CLI 问题。** 本规划只建测试框架，不修被测代码。修复另案讨论。

---

## 验证标准

```
Step 1 完成：go test -tags=unit ./internal/...  → 通过（仅跑单元）
Step 1 完成：go test -tags=integration ./...     → 通过
Step 1 完成：go test -tags=e2e ./...              → 通过（跳过需子进程的测试）
Step 1 完成：go test -tags=integration -run TestCLI_...  → 全部通过
Step 2 完成：新增 CLI snapshot 测试覆盖 ≥8 个命令路径
Step 3 完成：runCommand 迁移到 internal/cmd/testhelper_test.go
Step 4 完成：make test-unit 1 秒内完成
Step 5 完成：CI 三层流水线绿
```

---

## 执行顺序

```
1. 加 build tag（所有 *_test.go 文件头 +1 行）          [30 个文件，机械操作]
2. 创建 Makefile                                          [1 个文件]
3. 提取 runCommand → internal/cmd/testhelper_test.go     [重构]
4. 创建 cli_snapshot_test.go                             [核心价值]
5. 更新 CI 配置                                           [.github/workflows/go.yml]
6. 更新 CLAUDE.md 测试规范                                [CLAUDE.md]
7. 全局 go test ./... 全绿确认                            [验证]
```

最多 7 步，其中步骤 1 最简单但文件最多，步骤 4 最核心。