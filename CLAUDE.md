## 构建

- `go install ./cmd/akswitch/` → 全局 `akswitch`（`%USERPROFILE%\go\bin\` 已在 PATH）
- 所有依赖装在项目级，**禁止污染全局**

## 工作流

- main 分支受保护，禁止直接推送
- 执行改动前创建功能分支
- 遵循 GitHub Flow + 原子 commit
- 提交 PR 后在前台等 CI 绿

### 前置检查清单（强制）

声明"完成"前必须逐条核查。**跳过任意一条 = 任务未完成。**

1. **[测试]** — 新增 CLI 命令/标志 → 对应 CLI 入口测试已写？
2. **[测试]** — `go test ./...` 全量通过？
3. **[手动验收]** — `go install` 后用真实二进制验证了行为？
4. **[提交]** — 在正确的分支？提交信息清晰？
5. **[CI]** — 前台等到 CI 绿？

## 测试策略

- **主攻方向**：集成验收测试（mock upstream + 真实代理请求），如 `proxy_test.go`
- **测试入口**：所有 CLI 可达路径用 `runCommand()` 或子进程模式
- **标准**：before/after 对比，不测绝对值快照
- **不写**：mock 掉一切只测 JSON 的 Handler 测试（如 `handlers_test.go`）
- **边界**：Key ≤12 字符时 `MaskKey` 输出 `****`

## 项目定位

akswitch 只专注于单 provider 内的 API key 轮转，不重复造 ccswitch 的轮子。

## 测试规范

测试按速度分层，用 //go:build 标签区分：

- `unit`: ≤1s，纯逻辑无 IO
- `integration`: ≤10s，CLI 命令 + mock HTTP
- `e2e`: ≤2m，子进程 + 端口绑定

### 新增测试文件规则

1. 先判断所属层级，加对应 `//go:build` 标签
2. CLI 命令测试必须包含输出断言（`assertOutputContains` 或类似）
3. 禁止无输出断言的 `runCommand` 模式（只测不崩不算测完）