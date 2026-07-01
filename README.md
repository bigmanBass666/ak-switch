# Alvus — API Key 轮转代理

[![Go Version](https://img.shields.io/badge/Go-1.23-blue)](https://go.dev)
[![Tests](https://img.shields.io/badge/tests-278%20passing-brightgreen)](https://github.com/OmitNomis/Alvus/actions)

> 专注单 provider 内 API Key 的智能轮转与熔断，与 [ccswitch](https://github.com/farion1231/cc-switch) 互补。
> ccswitch 负责 provider 级路由与故障转移，Alvus 负责 provider 内 key 级轮转与限流处理。

---

## 快速开始

```bash
# 编译
go build -o alvus.exe ./cmd/alvus/

# 初始化配置
./alvus.exe config init

# 添加 provider 和 key
./alvus.exe provider add nvidia --target https://integrate.api.nvidia.com/v1 --port 3001
./alvus.exe key add nvidia nvapi-xxxxxxxxxxxx

# 启动
./alvus.exe start

# 查看状态
./alvus.exe status
```

## 核心功能

- **Key 轮转** — 多 Key 轮询、429 指数退避、401/403 永久禁用、自动恢复
- **两层熔断** — Key 级（限流退避） + 上游级（502/503 熔断 + 半开探测）
- **单进程多实例** — 一个进程管理多个 provider，各自独立端口
- **CLI 管理** — `provider` / `key` 增删查改，`status / logs / stop` 运行时管理
- **加密存储** — API Key 以 AES-256-GCM 加密存储
- **配置热重载** — `.env` 修改自动生效，配置 diff 日志脱敏输出
- **Dashboard** — 内置 Web 实时面板
- **Prometheus 指标** — 开箱即用的监控栈（Alvus + Prometheus + Grafana）

## 文档

| 文档 | 说明 |
|------|------|
| [CLI 参考](docs/cli-reference.md) | 所有子命令、标志位、使用示例 |
| [配置说明](docs/configuration.md) | TOML 与 .env 配置、所有可用字段 |
| [API 文档](docs/api.md) | 代理端点、管理 API、错误码 |
| [熔断器架构](docs/architecture.md) | 两层熔断器状态转移、响应矩阵 |
| [部署指南](docs/deployment.md) | Docker、监控栈、自定义端口 |

## 项目定位

Alvus **不做** provider 级路由、请求整流、响应变换（ccswitch 已成熟）。
Alvus **只做** 单 provider 内多 Key 的智能轮转、限流处理、自动熔断。

## License

[MIT](LICENSE)
