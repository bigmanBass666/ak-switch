**现阶段: 纯个人使用, 仅自用**

- 一切问题, 请反思如何从根源解决, 而不是抑制错误

- 所有依赖装在项目级, **禁止污染全局**, 如有必要, 请与用户讨论
- 构建: `go install ./cmd/akswitch/` → 全局 `akswitch`（%USERPROFILE%\go\bin\ 已在 PATH）

## 工作流规范

- main 分支受保护，禁止直接推送。(main 分支需保持完全健康状态与随时可运行的状态)
- 执行一切改动前需思考是否需要新建分支.
- 务必遵循 GitHub Flow, 原子commit原则, 提交PR之后,必须在前台等CI的结果.

## 测试设计规范

### 测试策略：Testing Trophy（与主流标准对齐）

当前业界主流（Go 生态、Kubernets、Prometheus）共识是 Testing Trophy 模型。

```
优先级排序：
1. 静态分析（go vet, go build, 类型检查）— 零成本，收益最大
2. 集成验收测试（mock upstream + 真实代理请求）— 主力，最高 ROI
3. 单元测试（纯内部逻辑，如 parseKeys、CoolingCount）— 只在复杂逻辑时写
4. HTTP Handler 测试（mock 依赖测路由）— 仅 API 契约变更时才写，不每改一行都补
```

**原则：**
- 新功能**默认**做集成验收测试（before/after 对比，证明它在真实场景下工作）
  - 写 `proxy_test.go` 那样带 mock upstream 的集成测试
  - **不写** `handlers_test.go` 那样 mock 掉一切只测 JSON 的 Handler 测试
- 单元测试只留给复杂内部逻辑，不为简单 getter/setter 写
- Handler 测试（mock 掉依赖只测路由的）低价值，已有就留着当 API 契约文档，新功能不再写
- 永远有 **before/after 对比**，不测绝对值的快照

### 验收测试三问（每次写测试前必须自我检查）

任何改动（包括新功能、Bug 修复、重构）落实测试时，必须回答以下三个问题：

1. **用户可见行为** — 这次改动的外部可观察行为是什么？（不是"代码跑通了"，而是"用户看到了什么"）
   - 例：配置缺字段 → 启动失败 + stderr 有错误消息，而不是"不会 panic"
   
2. **生效证明** — 有没有一个测试能证明这个行为真的生效了？这个测试会不会在改动被删除时失败？（不是"模块测通了"，而是"功能验收了"）
   - 例：改 `reloadConfig()` → 应该有一个测试写坏 `.env` 然后验证旧配置保留、diff 日志正确生成
   
3. **破坏测试** — 如果我故意捣乱（坏配置、空数据、上游挂了、网络断了），程序怎么死？这个死法你接受吗？你写测试验证了吗？
   - 例：所有 Key 冷却 → 返回 429 还是 panic？写个测试证明了

### 违反案例库（防止重复踩坑）

以下是我们反复跳进去的坑，任何改动都必须规避：

- ❌ 只测模块不测集成 — `config.Validate()` 通过了不等于 `loadConfig()` 启动校验走通了
- ❌ 只测代码不测行为 — 改了一行没改测试→ 不说明改动生效了
- ❌ 只测正常路径不测破坏 — 热重载改了 `.env` 但没测故意写错 `.env` 会怎么样
- ❌ 环境泄漏 — 子进程测试没有隔离环境变量，测试互相干扰
- ❌ 跳过边缘情况 — Key 长度 ≤6 字符时 `MaskKey` 输出 `****`，不是常规格式

### 关键路径覆盖纪律（2026-07-01 新增）

> **所有 CLI 行为必须通过 CLI 入口测试。** 这条纪律是对"验收测试三问"的强制执行条款。

#### 判断标准

- **需要 CLI 入口测试**：任何可以从 CLI 命令到达的代码路径
  - `internal/cmd/` 下的全部文件
  - `startServer()`、`loadKeysForProvider()`、各种 `RunE` 函数
- **不需要 CLI 入口测试**（纯内部逻辑）：
  - 数据结构（`TomlProviderConfig`、`LogEntry`）
  - 算法（`MaskKey`、`parseKeys`、circuit breaker 状态机）
  - 纯工具函数

#### 实现方式

| 命令类型 | 测试方式 | 示例 |
|---------|---------|------|
| 非阻塞 CLI 命令（config、provider、key） | `runCommand(t, "akswitch", ...)` | `provider_cmd_test.go` |
| 阻塞/长运行 CLI 命令（start） | 子进程模式：启动 → 验证 → kill | `start_cmd_test.go` |

#### PR 合并前自问

1. 这个 PR 改动了哪些编码？
2. 用户是通过哪个 CLI 命令接触到这些代码的？
3. **那个 CLI 命令有没有对应的入口测试？**
   - 没有 → 先写测试，再写代码
   - 有 → 运行一次确认测试通过

#### 这条纪律防止的 bug

| 历史 bug | 根因 | 纪律能拦住吗？ |
|---------|------|---------------|
| Validate 在加载 Key 前调用 | `startServer()` 内部顺序错误，`startServer()` 零测试 | ✅ 入口测试会在 PR 阶段暴露 |
| `loadKeysForProvider` 没查标准存储 | 函数未走 `KeysFile` fallback 路径 | ✅ 入口测试会验证 Key 真正被加载 |

## 项目定位

与[ccswitch](https://github.com/farion1231/cc-switch/raw/refs/heads/main/docs/user-manual/zh/README.md)结合使用, ccswitch负责provider的轮转, 而akswitch只专注于做单 provider 内的 api key 的轮转, 其他功能有其他已经极为成熟的工具如ccswitch搭配使用, akswitch只专精于apikey轮转垂直领域

ccswitch本身就已经具有了强大的本地路由代理功能了, 比如:
```
启动代理、配置项、运行状态
应用路由、配置修改、状态指示
故障转移队列、熔断器、健康状态
用量统计、趋势图表、定价配置
模型检查、健康检测、延迟测试
```
只是缺乏了api key的路由代理, 这是我们要填补的, 而不是重复造ccswitch的轮子