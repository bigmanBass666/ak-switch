# 优雅关闭（Graceful Shutdown）实施计划

## 概述

当前代码已具备基本的 `http.Server.Shutdown()` 调用，但存在以下问题：

1. 后台 goroutine（`WatchEnvFile`、`RefreshKeyPoolMetrics`）在主 goroutine 退出时可能未完成
2. 关闭超时仅 5 秒，对 SSE 流式响应或长时间请求不够
3. 关闭 goroutine 是 fire-and-forget，`main()` 在 `Serve()` 返回后立即退出
4. 没有等待所有 goroutine 完成后再退出

## 改动范围

仅改 **1 个文件**：`main.go`

- 不新增文件
- 不修改 `internal/server/` 包
- 不修改 `manage.go`

## 具体改动

### 1. 引入 `sync.WaitGroup` 管理后台 goroutine

```go
var wg sync.WaitGroup

wg.Add(1)
go func() {
    defer wg.Done()
    server.WatchEnvFile(state, stop)
}()

wg.Add(1)
go func() {
    defer wg.Done()
    server.RefreshKeyPoolMetrics(state, stop)
}()
```

### 2. 将关闭超时从 5s 延长到 30s

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
```

理由：
- 与 Docker `STOP_TIMEOUT` 默认值对齐
- 对 SSE 流式响应更友好
- Go 标准库推荐生产环境使用 30s 作为默认关闭超时

### 3. 在 `Serve()` 返回后等待所有 goroutine 完成

```go
if err := httpServer.Serve(listener); err != http.ErrServerClosed {
    slog.Error("server error", "error", err)
    log.Fatalf("Server error: %v", err)
}

// 等待后台任务完成
wg.Wait()
slog.Info("server stopped gracefully")
```

### 4. 关闭顺序确认

```
SIGINT/SIGTERM
    → signal goroutine 关闭 stop channel
    → 后台 goroutines (WatchEnvFile, RefreshKeyPoolMetrics) 收到 stop 信号开始退出
    → shutdown goroutine 收到 stop 信号，调用 httpServer.Shutdown(30s)
    → Serve() 返回（所有活跃请求在 30s 内完成或被取消）
    → wg.Wait() 等待后台 goroutine 完成
    → main() 返回
```

## 不做的改动

- ❌ 不添加 SIGQUIT/SIGHUP 信号处理（当前不需要）
- ❌ 不添加 `ReadTimeout`/`WriteTimeout`/`IdleTimeout`（与项目无直接关系）
- ❌ 不添加 HTTP client 连接池清理（进程退出自然会关闭）
- ❌ 不修改 `internal/server/` 包（生命周期管理在 main.go 层）
- ❌ 不修改 `manage.go`（子进程管理模式已有自己的关闭逻辑）

## 验证方法

1. `go build ./...` — 编译通过
2. `go vet ./...` — 零警告
3. `go test -race ./...` — 全部通过
4. 手动验证：启动服务 → 发送一个慢请求 → Ctrl+C → 观察日志输出顺序
5. 验证超时：模拟一个超过 30s 的请求 → Ctrl+C → 确认 30s 后强制退出