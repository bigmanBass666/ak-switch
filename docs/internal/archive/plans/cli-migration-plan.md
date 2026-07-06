# alvus CLI 化改造方案

## 一、概述

将 `alvus` 从纯服务二进制改造为单二进制 CLI + 服务，遵循 Cobra + Viper 行业标准。
一个 `alvus` 命令同时管理配置和运行服务，不再需要 `--manage` JSON 和多个子进程。

## 二、当前状态分析

### 现状（manage.go）

```
manage.json (JSON) → Manager (manage.go)
                      ├── 解析 JSON 配置
                      ├── 为每个 provider 写 .env 文件
                      ├── exec 子进程（alvus.exe 自身）
                      ├── 健康检查循环（每 3 秒）
                      └── 子进程重启逻辑
```

**问题**：
- 管理命令写在 `manage.go` 中，使用子进程模式（fork 自己），间接且脆弱
- 配置靠手写 `manage.json` JSON 文件，无校验提示
- 增删 key 需要 curl 调用 API

### 目标架构

```
alvus (单二进制)
├── CLI 层 (Cobra)
│   ├── alvus provider add/list/remove
│   ├── alvus key add/list/remove
│   ├── alvus start/stop/status/logs
│   └── alvus config init/view
└── 服务层
    ├── InstanceManager（管理多个 ServerState）
    └── ServerState（现有代码，基本不变）
```

### 核心改动

| 模块 | 改动量 | 说明 |
|------|--------|------|
| `cmd/alvus/main.go` | 新增 | Cobra 入口，替代当前 main.go |
| `main.go` | 保留兼容 | 现有入口改为 Cobra subcommand |
| `manage.go` | 替换 | InstanceManager 替代子进程模式 |
| `internal/config/` | 增强 | 增加 TOML 解析、XDG 路径 |
| `go.mod` | 修改 | 增加 cobra + viper 依赖 |

### 不动的模块

- `internal/server/proxy.go` — 代理转发逻辑完全不变
- `internal/server/handlers.go` — 管理 API 不变
- `internal/keypool/` — Key 池、加密、持久化完全不变
- `internal/circuitbreaker/` — 熔断器不变

## 三、最终产出（六阶段）

### Phase 0: 项目结构重组

**目标**: 迁入 Cobra，不改变任何行为。

**改动**:

```
D:\Test\Alvus-fork\
├── cmd/
│   └── alvus/
│       └── main.go          ← 新 Cobra 入口
├── internal/
│   └── cmd/                  ← 新增包
│       ├── root.go           ← 根命令（alvus --help）
│       ├── start.go          ← alvus start 子命令
│       ├── provider.go       ← alvus provider 子命令
│       ├── key.go            ← alvus key 子命令
│       └── config.go         ← alvus config 子命令
├── main.go                   ← 删除或变为调用 cmd/alvus/main.go
├── manage.go                 ← 暂时保留，兼容 --manage
└── internal/server/          ← 不变
```

**关键决策**:
- `main.go` 删除，入口统一到 `cmd/alvus/main.go`
- 加 `tools.go` 确保 `go mod tidy` 不删除 cobra 间接依赖
- `go build ./cmd/alvus/` 编译，生成 `alvus.exe`

**验证**:
- `go build ./cmd/alvus/` 通过
- `alvus start` 行为与当前 `alvus.exe` 完全一致（加载 .env、启动 HTTP 服务）

---

### Phase 1: TOML 配置层

**目标**: 新增 TOML 配置格式，支持加密 key 存储，保持向后兼容。

**改动文件**:

| 文件 | 改动 |
|------|------|
| `internal/config/config.go` | 新增 `LoadToml()`, `SaveToml()`, 支持 XDG 路径 |
| `go.mod` | + `pelletier/go-toml/v2` |
| `internal/cmd/config.go` | `alvus config init/view` 子命令 |

**配置存储路径（XDG 规范）**:

```
$XDG_CONFIG_HOME/alvus/
├── config.toml        # 明文，可备份，含除 key 外的所有配置
└── keys/
    ├── sensenova.enc  # AES-256-GCM 加密
    └── nvidia.enc     # AES-256-GCM 加密
```

**Windows 路径**: `%APPDATA%/alvus/`（即 `C:\Users\<user>\AppData\Roaming\alvus\`）

**config.toml 格式**:

```toml
# alvus 配置文件

[provider.sensenova]
target = "https://api.sensenova.com/v1"
genai = "https://api.sensenova.com"
port = 3001
cooldown_sec = 60
max_retries = 3

[provider.nvidia]
target = "https://integrate.api.nvidia.com/v1"
genai = "https://ai.api.nvidia.com"
port = 3002
```

**Key 加密存储**:
- 复用 `internal/keypool/crypto.go` 现有 `Encrypt`/`Decrypt` 函数
- `alvus provider add` 时提示输入 `KEYS_ENCRYPTION_KEY`（或从环境变量读取）
- Key 文件路径：`$XDG_CONFIG_HOME/alvus/keys/<name>.enc`
- 格式：`{"keys": [{"key": "<encrypted>", "name": "...", "disabled": false}]}`
- 加密过程走现有 `SaveFullStore`/`LoadFullStore` 机制

**向后兼容**:
- 同时支持 `.env`（旧）和 `config.toml`（新）两种配置源
- 检测优先级：`--config` 指定 → `config.toml` → `.env`
- 迁移提示：检测到 `.env` 但无 `config.toml` 时打印 "建议运行 `alvus config init` 迁移到 TOML"

**验证**:
- `alvus config init` 生成合法 TOML 文件
- `pelletier/go-toml/v2` 读写正确
- 加密 key 文件写入后解密可读

---

### Phase 2: InstanceManager

**目标**: 单进程管理多个 ServerState，替代 manage.go 的子进程模式。

**新增文件**:

`internal/server/manager.go`:

```go
type InstanceManager struct {
    instances map[string]*ManagedInstance
    mu        sync.RWMutex
    stop      chan struct{}
}

type ManagedInstance struct {
    Name      string
    Config    *config.Config
    Pool      *keypool.KeyPool
    State     *ServerState
    Listener  net.Listener
}
```

**核心流程**:

```
alvus start
  │
  ├── 1. 读取配置源（config.toml / .env / --config）
  │
  ├── 2. 对每个 provider:
  │     ├── 从 keys/<name>.enc 加载 Key（解密）
  │     ├── 创建 KeyPool
  │     ├── 创建 ServerState
  │     └── 绑定端口启动 HTTP
  │
  ├── 3. 启动后台 goroutine（每个实例一组）
  │     ├── WatchEnvFile（如从 .env 启动）
  │     ├── RefreshKeyPoolMetrics
  │     └── ActiveHealthCheck
  │
  ├── 4. 监听信号（SIGINT/SIGTERM/stop channel）
  │
  └── 5. Waitgroup 等待所有实例退出
```

**可复用现有代码**:
- `NewServerState()` → 直接复用，每个实例一个
- `PersistKeys()` → 每个实例独立持久化
- 后台 goroutine → 需为每个实例创建一套

**关闭流程**:

```
Ctrl+C
  → close(stop)
  → 所有 goroutine 退出
  → 逐个 http.Server.Shutdown(ctx)
  → 所有实例优雅关闭
  → 退出
```

**验证**:
- `alvus start` 启动两个 provider，两个端口都能访问
- 一个实例 panic 不波及另一个（`recover`)
- 优雅关闭两个实例日志完整

---

### Phase 3: 配置管理子命令

**目标**: 不用手写 JSON/TOML，用 CLI 管理 provider 和 key。

```bash
# provider 管理
alvus provider add nvidia \
  --target https://integrate.api.nvidia.com/v1 \
  --port 3002

alvus provider list         # 列出所有，含运行状态（绿色/红色）
alvus provider rm nvidia    # 删除 provider

# key 管理
alvus key add nvidia sk-xxxxxx     # 加密存储
alvus key list nvidia               # 脱敏显示
alvus key rm nvidia 1               # 删除第 1 个 key
alvus key disable nvidia 1          # 禁用
```

**实现位置**: `internal/cmd/provider.go`, `internal/cmd/key.go`

**关键行为**:
- 所有 CRUD 操作直接修改 `config.toml` 和加密 key 文件
- 不涉及运行时，不影响正在运行的实例
- 缺参数时报错，不进入交互模式

**验证**:
- `alvus provider add --help` 显示完整 flags
- 增删 provider 后 `config.toml` 内容正确
- 增删 key 后 `.enc` 文件内容正确

---

### Phase 4: 运行时管理命令

**目标**: 查看和管理运行中的实例。

```bash
alvus status              # 所有实例状态一览
alvus logs nvidia         # tail 指定实例的日志
alvus stop                # 停止所有实例
```

**实现方式**:
- 通过 `InstanceManager` 暴露的方法操作运行中的实例
- `status` 汇总所有实例的 ServerState 健康信息
- `logs` 读取日志存储（现有 `logstore`）

**验证**:
- `alvus status` 输出各实例端口、key 数、健康状态
- `alvus stop` 能优雅停止所有实例

---

### Phase 5: 清理与文档

**目标**: 收尾。

- 删除 `manage.go`（或标记 deprecated）
- 删除 `main.go`（已由 `cmd/alvus/main.go` 替代）
- 更新 README 文档
- `.env.example` 示例更新
- 确保 `go build ./...` 和 `go vet ./...` 零警告

---

## 四、Spec 拆分策略

6 个 Phase 不写成一个巨 spec，拆成 **3 个独立 spec**，每个完成后立即 PR 合并，互不阻塞。

```
Spec A (Phase 0 + 1): Cobra 骨架 + TOML 配置层
    └── 产出: alvus --help 出来，alvus config init 生成配置
    └── 验证: 编译通过，TOML 读写正确
    └── 合并后: 可以开始用新配置格式

Spec B (Phase 2): InstanceManager
    └── 产出: alvus start 启动多 provider，单进程运行
    └── 验证: 两个实例同时跑，独立端口，优雅关闭
    └── 合并后: 可以替代 manage.json 子进程模式

Spec C (Phase 3 + 4): 管理命令 + 运行时命令
    └── 产出: provider/key CRUD、status/stop/logs
    └── 验证: 增删改查 + 运行态操作
    └── 合并后: 完全替代手动操作

Phase 5 (清理): 不单独写 spec，在 Spec C 的 checklist 中收尾
```

**为什么不拆更细（每 Phase 一个 spec）**：

| 方案 | 问题 |
|------|------|
| 6 个 spec | Phase 0 内容太少，单独写 spec 小题大做 |
| 每个 PR 一个 Phase | PR 数量过多，CI 排队时间长 |

**为什么不分批并行**：

Phase 依赖链是线性的（0→1→2→3→4），后一个依赖前一个的代码结构，无法并行。
但每个 spec 内部的 tasks 可以并行执行（如 Phase 1 的 TOML 读写和 XDG 路径可以并行开发）。

### Spec 内容概览

| | Spec A | Spec B | Spec C |
|---|--------|--------|--------|
| **Phases** | 0 + 1 | 2 | 3 + 4 + 5 |
| **主轴改动** | 迁入 Cobra + TOML 配置 | InstanceManager | 管理命令 + 运行时 |
| **现有代码改动** | main.go 搬迁、config.go 新增 | manage.go 替换 | cmd/ 子命令 |
| **主力测试** | 编译验证 + 单元测试 | 集成验收测试 | 集成验收测试 |
| **预期测试数** | 190 左右（现有不变 + TOML 单元） | 195 左右（+ 多实例集成） | 200 左右（+ CRUD 集成） |
| **PR 大小** | 中（依赖引入 + 文件搬迁） | 中（新文件多但逻辑清晰） | 小～中（纯子命令） |

## 五、执行顺序与依赖

```
Spec A: Phase 0 + 1 ── 先做
    │
Spec B: Phase 2 ── 依赖 Spec A 的配置读取
    │
Spec C: Phase 3 + 4 ── 依赖 Spec A（配置 CRUD）+ Spec B（InstanceManager 运行态）
    │
Phase 5 ── 在 Spec C 的 checklist 中收尾
```

## 六、测试策略

| 阶段 | 测试类型 | 覆盖内容 |
|------|----------|----------|
| Phase 0 | 编译验证 | `go build ./cmd/alvus/` |
| Phase 1 | 单元测试 | TOML 读写、加密 key 存储、XDG 路径解析 |
| Phase 2 | 集成验收测试 | 两个 provider 启动、独立端口、优雅关闭 |
| Phase 3 | 集成验收测试 | 增删 provider/config.toml 正确性 |
| Phase 4 | 集成验收测试 | status 输出正确、stop 优雅关闭 |
| Phase 5 | 全量回归 | `go test -race ./...` 零失败 |

## 六、假设与决策

**已确认的决策**:

| 项目 | 结论 |
|------|------|
| 后端框架 | Cobra（行业标准） |
| 配置格式 | TOML（优于 YAML 的确定性） |
| 进程模型 | 单进程多实例（不 fork 子进程） |
| detach/后台 | 不做（用户用 Docker 或终端标签页） |
| --manage 兼容 | 保留（过渡期并行，优先引导新格式） |
| 交互方式 | 纯 --flag，缺参数报错（不进入交互模式） |
| Key 存储 | 加密文件，与配置分离 |
| Encryption key | 环境变量 `KEYS_ENCRYPTION_KEY`（现有机制） |
| 配置路径 | XDG 规范（Windows: %APPDATA%/alvus/） |

## 七、风险与缓解

| 风险 | 概率 | 缓解 |
|------|------|------|
| Cobra 依赖引入问题 | 低 | `tools.go` 固定依赖版本 |
| InstanceManager 多实例死锁 | 中 | 每个实例独立 stop channel |
| TOML vs .env 双配置源混淆 | 中 | 优先级明确文档化 |
| manage.go 子进程模式删了 | 低 | 过渡期保留，用户切新 CLI 后再删 |
