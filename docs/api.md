# API 文档

## 代理端点

| 方法 | 路径 | 说明 |
|------|------|------|
| `*` | `/` | 代理所有请求到上游。`/genai/` 路径路由到 GenAI URL，其余路由到 Target URL |

### 代理行为

- 请求 header 透传（排除 `X-Admin-Token`、`Cookie`、`Proxy-Authorization`）
- 响应 body 流式转发（支持 SSE）
- 自动选择 Key 并设置 `Authorization` header
- 429 自动换 Key 重试，401/403 禁用 Key 后重试
- 重试全部耗尽返回 503

## 管理端点

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/health` | 可选 | 健康检查，返回 Key 详情 |
| `GET` | `/api/config` | 否 | 获取当前配置（Key 脱敏） |
| `POST` | `/api/config` | 是 | 更新配置并热重载 |
| `GET` | `/api/keys` | 否 | 列出所有 Key 状态 |
| `POST` | `/api/keys` | 是 | 添加新 Key |
| `DELETE` | `/api/keys` | 是 | 删除 Key（body `{"index": n}`） |
| `POST` | `/api/keys/{index}/disable` | 是 | 禁用指定 Key |
| `PUT` | `/api/keys/{index}/cooldown` | 是 | 冷却指定 Key |
| `DELETE` | `/api/keys/{index}` | 是 | 按索引删除 Key |
| `GET` | `/api/stats` | 否 | 请求统计（成功率、Key 状态、运行时长） |
| `POST` | `/api/reload` | 是 | 从 `.env` 重新加载配置 |
| `GET` | `/logs` | 否 | 获取请求日志（最近 1000 条） |
| `POST` | `/clear` | 是 | 清空日志 |
| `GET` | `/metrics` | 否 | Prometheus 指标 |
| `GET` | `/dashboard` | 否 | Web Dashboard |

### 鉴权

当配置了 `admin_token` 后，标记为"是"的端点需在请求头中提供 `X-Admin-Token`。未设置时所有端点无鉴权。

```bash
curl -X POST http://localhost:3000/api/reload \
  -H "X-Admin-Token: your-token"
```

### `/api/stats` 响应示例

```json
{
  "active_keys": 3,
  "disabled_keys": 0,
  "cooling_keys": 0,
  "total_requests": 142,
  "successful_requests": 138,
  "failed_requests": 4,
  "uptime_seconds": 3600,
  "key_details": [
    {"index": 0, "key": "nva...xxx", "active": true, "cooling_until": null, "name": ""},
    {"index": 1, "key": "nva...yyy", "active": true, "cooling_until": "2026-07-01T13:00:00Z", "name": "prod-key"}
  ]
}
```

## 错误码

代理请求失败时返回统一 JSON 格式：

```json
{"error": {"code": "ERROR_CODE", "message": "错误描述"}}
```

| 错误码 | HTTP 状态 | 触发条件 |
|--------|-----------|----------|
| `BAD_REQUEST` | 400 | 请求体过大或不可读 |
| `UPSTREAM_ERROR` | 500 | 构建上游请求失败 |
| `ALL_KEYS_INVALID` | 503 | 所有 Key 额度耗尽或已失效 |
| `EXHAUSTED_RETRIES` | 503 | 重试全部耗尽 |
