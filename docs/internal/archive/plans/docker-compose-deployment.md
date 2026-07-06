# Docker Compose 完整部署计划

## 摘要

将现有单服务 `docker-compose.yml` 升级为完整监控栈：Alvus + Prometheus + Grafana，含持久化数据卷、预置监控面板、内部网络隔离。

## 当前状态

- `docker-compose.yml`：仅 alvus 单服务，无持久化数据卷，`keys.json` 变更随容器销毁丢失
- 端口矛盾：代码默认 8080 vs Docker 硬编码 3000
- 已具备：7 个 Prometheus 自定义指标 + Go 运行时指标，`/metrics` 端点可用
- `dashboard.html` 内嵌在二进制中，但缺少生产级可视化

## 改动清单

### 1. `docker-compose.yml` — 重写为三服务架构

**Alvus 服务（增强）：**
- 新增 `alvus-data` 命名卷挂载到 `/app/data`，用于持久化 `keys.json`
- 显式设置 `KEYS_FILE=data/keys.json` 使持久化路径与卷一致
- 保留原有 `.env` 挂载 + 端口映射

**Prometheus 服务（新增）：**
- 使用 `prom/prometheus:latest`
- 挂载 `./prometheus/prometheus.yml` 配置文件
- 挂载 `prometheus-data` 命名卷持久化 TSDB
- 暴露端口 9090

**Grafana 服务（新增）：**
- 使用 `grafana/grafana:latest`
- 挂载 `./grafana/provisioning/` 自动配置目录
- 挂载 `grafana-data` 命名卷持久化面板与用户设置
- 暴露端口 3001（避免与 alvus 内部 3000 混淆）
- 环境变量设置匿名登录（方便演示）

**共享网络：**
- 创建 `alvus-net` 内部网络
- Prometheus 通过 `alvus:3000` 访问 metrics 端点

### 2. `prometheus/prometheus.yml` — 新增

```yaml
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: 'alvus'
    static_configs:
      - targets: ['alvus:3000']
```

### 3. `grafana/provisioning/datasources/prometheus.yml` — 新增

Grafana 启动时自动添加 Prometheus 数据源，指向 `http://prometheus:9090`。

### 4. `grafana/provisioning/dashboards/dashboard.yml` — 新增

Dashboard 自动发现配置，指向 `dashboards/` 目录下的 JSON 文件。

### 5. `grafana/provisioning/dashboards/alvus-overview.json` — 新增

预置 Grafana Dashboard，包含 6 个面板：

| 面板 | 指标 | 类型 |
|------|------|------|
| 请求速率（按状态） | `rate(alvus_requests_total[1m])` by status | Stat |
| 请求速率（按 Key） | `rate(alvus_requests_total[1m])` by key_index | Bar gauge |
| 请求延迟 p50/p95/p99 | `histogram_quantile(0.5/0.95/0.99, rate(alvus_request_duration_seconds_bucket[1m]))` | Time series |
| Key 池状态 | `alvus_keypool_keys` | Pie chart |
| 上游错误速率 | `rate(alvus_upstream_errors_total[1m])` by type | Time series |
| 健康检查 | `alvus_healthcheck_probes_total` + `alvus_upstream_cb_state` | Stat + State timeline |

### 6. `.gitignore` — 新增排除规则

```gitignore
data/
prometheus-data/
grafana-data/
```

防止本地卷数据被 Git 跟踪。

### 7. `README.md` — 更新 Docker 部署文档

- 新增 "完整监控部署" 小节，说明 `docker compose up -d` 即可启动全栈
- 注明 Grafana 访问地址 `http://localhost:3001`，预置面板开箱即用
- 更新端口说明表

### 8. `TODO.md` — 标记为已完成

## 不变事项

- **不修改 Go 代码** —— 不碰 main.go / server.go / handlers.go / metrics.go
- **不修改 Dockerfile** —— 构建流程不变
- **不修改 Config 结构** —— 配置字段不变
- **不做 Dashboard 独立服务** —— 已有内嵌 dashboard.html，Grafana 是补充

## 验证步骤

1. `docker compose config` —— 配置解析正确
2. `docker compose up -d` —— 三服务全部启动无报错
3. `curl http://localhost:3000/health` —— Alvus 正常
4. `curl http://localhost:9090/api/v1/targets` —— Prometheus 能看到 alvus target
5. `curl http://localhost:9090/api/v1/query?query=alvus_requests_total` —— 有数据返回
6. `curl http://localhost:3001/api/health` —— Grafana 正常
7. 浏览器打开 `http://localhost:3001` 看到预置 Dashboard

## 依赖

无。完全独立，不修改任何 Go 源代码。可与其他开发工作并行。