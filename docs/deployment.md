# 部署指南

## Docker

### 快速启动

```bash
# 构建并启动
docker compose up -d

# 检查健康状态
curl http://localhost:3000/health

# 查看日志
docker compose logs -f
```

需要在容器内挂载 `config.toml` 或 `.env` 配置文件。

### 自定义端口

```bash
PORT=8080 docker compose up -d
```

### 仅构建

```bash
docker build -t alvus .
docker run --rm alvus --help
```

### 健康检查

Dockerfile 内置 HEALTHCHECK（每 30 秒探测 `/health`），不健康时自动重启。

## 监控栈

Alvus 自带 Prometheus 指标，搭配预置的 Grafana 面板开箱即用。

### 启动完整栈

```bash
docker compose up -d
```

### 服务端口

| 服务 | 端口 | 说明 |
|------|------|------|
| Alvus | `3000` | API Key 代理 |
| Prometheus | `9090` | 指标采集（`alvus:3000/metrics`） |
| Grafana | `3001` | 预置监控面板 |

### 自定义全部端口

```bash
PORT=3000 PROMETHEUS_PORT=9090 GRAFANA_PORT=3001 docker compose up -d
```

### Grafana 面板内容

- 请求速率（按状态码/Key 分布）
- 请求延迟 p50 / p95 / p99
- Key 池状态（活跃/冷却/禁用）
- 上游熔断器状态
- 上游错误按类型分布
- 健康检查成功率与延迟

> Grafana 面板无需登录（匿名只读访问），首次启动自动加载预置数据源和仪表盘。
