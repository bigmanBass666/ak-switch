# 代码审查报告

**项目：** Alvus Multi-Key Proxy  
**审查文件：** `main.go`, `manage.go`, `dashboard.html`, `regression_test.ps1`  
**审查日期：** 2026-06-24

---

## 严重问题（建议必须修复）

### 1. 数据竞争：`healthHandler` 无锁读取 `pool.keys`

- **文件：** `main.go:577`
- **风险：** 高
- **描述：** `healthHandler` 在 `s.mu.RLock()` 保护下获取 `pool` 指针后，在行 577 直接访问 `len(pool.keys)`，未持 `pool.mu`。而 `keysHandler` POST/DELETE 通过 `AddKey`/`RemoveKey` 在 `pool.mu` 保护下修改 `pool.keys`。这两个操作可并发执行，构成对 `pool.keys` slice header 的 data race。

  同样的问题也出现在 `configHandler` 行 379：`keys := s.pool.keys` 在 `s.mu.RUnlock()` 释放后使用，期间 `pool.keys` 可能被 `keysHandler` 修改。

- **建议修复：** 在 `healthHandler` 中访问 `pool.keys` 前加 `pool.mu.Lock()`，或将对 `pool` 的读写统一用 `s.mu` 写锁保护。

---

### 2. 数据竞争：`healthHandler` 调用 `GetKeyDetails` 时 `keysHandler` 并发修改 Pool

- **文件：** `main.go:565-577`
- **风险：** 高
- **描述：** `healthHandler` 获取 `pool` 后调用 `pool.GetKeyDetails()`，该方法内对 `pool.mu` 加锁读取所有字段。但若 `keysHandler`（POST/DELETE）在 `s.mu.RLock` 释放后对同一 `pool` 对象调用 `AddKey`/`RemoveKey`，两者在 `pool.mu` 上互相阻塞——有锁竞争，**理论上不会**导致数据竞争，但存在逻辑死锁风险：如果 `healthHandler` 先获得 `pool.mu`，`keysHandler` 在 `pool.mu` 上等待，且 `keysHandler` 执行期间持有某些资源...

  实际分析：`healthHandler` 持有 `pool.mu` → `GetKeyDetails` 遍历；`keysHandler` POST 在 `pool.AddKey()` 内争 `pool.mu`——这是正常的锁竞争，不是死锁。但 `healthHandler` 行 577 的 `len(pool.keys)` 在 `pool.mu` 范围外，这才是真正的 data race（与上一项重复）。

- **建议修复：** 将 `healthHandler` 行 577 移入 `GetKeyDetails` 内部，或在调用 `GetKeyDetails` 前持有 `pool.mu`。

---

### 3. 死代码 + 内存浪费：5xx 重试路径中 body 回读无意义

- **文件：** `main.go:680-686`
- **风险：** 中（逻辑误导性高）
- **描述：** 5xx 分支中：
  ```go
  body, _ := io.ReadAll(resp.Body)
  resp.Body.Close()
  log.Printf("⚠️ Upstream %d: %s (Retrying...)", resp.StatusCode, body)
  resp.Body = io.NopCloser(bytes.NewReader(body)) // ← 死代码
  continue
  ```
  `continue` 回到 for 循环开头，`pool.Next()` 选新 key 后执行新的 `client.Do(req)`，返回全新的 `resp`。行 684 的 `resp.Body = ...` 从未被读取。该行浪费内存分配且引人误解——读者会以为 body 在重试中被复用。

- **建议修复：** 删除行 684，或改为保存 body 供日志使用。如果意图是同一 key 重试不换请求体，则需要重构整个重试逻辑。

---

### 4. 安全：5xx 上游错误响应体被完整日志记录

- **文件：** `main.go:683`
- **风险：** 中
- **描述：** `log.Printf("⚠️ Upstream %d: %s (Retrying...)", resp.StatusCode, body)` 将上游错误响应体完整打印到日志。如果上游返回的内容包含敏感信息（token、认证信息、内部堆栈等），会被记录到 stdout 和日志文件（manage 模式下写入 `alvus-manage.log`）。

- **建议修复：** 对日志中的 body 做截断和敏感信息过滤，或仅在 verbose 模式下输出。

---

### 5. 安全：Admin Token 使用字符串比较，存在时序侧信道攻击

- **文件：** `main.go:397`（`configHandler` POST）及 `main.go:492`（`keysHandler` POST/DELETE）
- **风险：** 低（对代理类工具可接受，但应修复）
- **描述：** 使用 `!=` 比较 admin token，Go 的字符串比较在发现第一个不同字符后立即返回。对局域网工具风险有限，但不符安全最佳实践。

- **建议修复：** 使用 `crypto/subtle` 的 `ConstantTimeCompare`：
  ```go
  import "crypto/subtle"
  if s.cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(s.cfg.AdminToken)) != 1 {
  ```

---

### 6. Manage 模式：`StopAll()` 未等待子进程退出即清理工作目录

- **文件：** `manage.go:396-405`
- **风险：** 高（Windows 上可能失败）
- **描述：** `runManager` 收到停止信号后：
  ```go
  mgr.StopAll()          // 发送 Kill，不等待
  // 紧接着:
  os.RemoveAll(workBase) // 子进程可能仍在运行
  ```
  `ManagedInstance.Stop()` 调用 `m.Cmd.Process.Kill()` 后立即返回，不等待进程退出。随后 `RemoveAll` 删除工作目录时，子进程可能尚未终止（尤其是子进程在关闭 HTTP 连接或写日志），在 Windows 上会导致 `RemoveAll` 失败（文件被占用）。

- **建议修复：** `Stop()` 中 Kill 后应 `Wait()` 等待进程退出（加超时）：
  ```go
  func (m *ManagedInstance) Stop() {
      m.mu.Lock()
      defer m.mu.Unlock()
      if !m.Running || m.Cmd == nil || m.Cmd.Process == nil {
          return
      }
      m.Cmd.Process.Kill()
      done := make(chan struct{})
      go func() {
          m.Cmd.Wait()
          close(done)
      }()
      select {
      case <-done:
      case <-time.After(5 * time.Second):
          log.Printf("⚠️ [%s] 进程 %d 未在 5s 内退出", m.Name, m.Cmd.Process.Pid)
      }
      m.Running = false
  }
  ```

---

### 7. 竞态：`Stop()` 可能在进程已退出后调用 Kill

- **文件：** `manage.go:165-176`
- **风险：** 中
- **描述：** `ManagedInstance.Start()` 中的 wait goroutine（行 147-159）在进程退出后设置 `m.Running = false`，但不清理 `m.Cmd` 或 `m.Cmd.Process`。`Stop()` 检查 `m.Running` 时可能看到 `true`（wait goroutine 尚未获取 `m.mu`），然后调用 `m.Cmd.Process.Kill()` 杀死一个已退出的进程。`Process.Kill()` 在已退出进程上调用在 Windows 上无实质影响，但 wait goroutine 在解锁后调用 `cmd.Wait()` 返回时会再次尝试操作进程状态。

- **建议修复：** 在 wait goroutine 中将 `m.Cmd` 置 nil：
  ```go
  go func() {
      err := cmd.Wait()
      m.mu.Lock()
      m.Running = false
      m.Cmd = nil  // 明确清理
      m.mu.Unlock()
      ...
  }()
  ```
  并在 `Stop()` 中增加对 `m.Cmd == nil` 的判断保护。

---

### 8. 缺少请求体大小限制，存在 OOM 风险

- **文件：** `main.go:590`
- **风险：** 中
- **描述：** `io.ReadAll(r.Body)` 无限制读取整个请求体到内存。攻击者可发送 GB 级请求体导致内存耗尽。

- **建议修复：** 设置合理的 `http.MaxBytesReader` 限制：
  ```go
  r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
  bodyBytes, err = io.ReadAll(r.Body)
  ```

---

### 9. `proxyHandler` 流式响应中忽略 Write 错误

- **文件：** `main.go:696`
- **风险：** 中
- **描述：** 在流式转发循环中：
  ```go
  for {
      n, rerr := resp.Body.Read(buf)
      if n > 0 {
          w.Write(buf[:n])  // 错误被忽略
          f.Flush()
      }
      if rerr != nil {
          break
      }
  }
  ```
  如果客户端断开连接，`w.Write()` 失败但函数继续从上游读取数据，直到 `resp.Body.Read` 返回错误。浪费上游带宽和代理资源。

- **建议修复：** 检查 `w.Write()` 返回值，出错时立即跳出循环：
  ```go
  if n > 0 {
      if _, werr := w.Write(buf[:n]); werr != nil {
          break
      }
      f.Flush()
  }
  ```

---

## 中等问题

### 10. `configHandler` POST 重载失败返回 202 无说明

- **文件：** `main.go:465-469`
- **风险：** 低
- **描述：** `.env` 写入成功后调用 `reloadConfig()` 失败（如环境变量格式问题），函数返回 `http.StatusAccepted` 但不告知客户端重载失败。客户端以为配置已生效，实际仍在使用旧配置。

- **建议修复：** 在 202 响应体中包含错误信息：
  ```go
  w.Header().Set("Content-Type", "application/json")
  w.WriteHeader(http.StatusAccepted)
  json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "warning": "config saved but reload failed: " + err.Error()})
  ```

---

### 11. `keysHandler` GET `/api/keys` 使用 1-based index，Dashboard 再减 1 转 0-based

- **文件：** `main.go:506` 和 `dashboard.html:569`
- **风险：** 低
- **描述：** 后端用 `i + 1` 作为 `index`（1-based 展示），前端用 `(key.index - 1)` 转换为 0-based 传给 DELETE 接口。这种"展示层和数据层用不同坐标系"的做法脆弱——如果前端有人忘记减 1，会删除错误的 key。

- **建议修复：** 后端直接返回 0-based index，前端展示时自行加 1。

---

### 12. `WatchAndRestart` 健康检查未处理 DNS 解析异常等情况

- **文件：** `manage.go:329-344`
- **风险：** 低
- **描述：** 健康检查使用 `http://127.0.0.1:%d/health`。如果 `client.Get` 因 DNS 或其他网络错误失败（虽然理论上不会影响 127.0.0.1），`resp` 为 nil 且 `err` 非 nil，进入 else 分支并在行 334 检查 `resp != nil`。这是正确的防御，但缺少对网络错误具体类型的日志区分。

- **建议修复：** 在 health check 失败时增加具体错误日志：
  ```go
  if err != nil {
      log.Printf("⚠️ [%s] 健康检查网络错误: %v", inst.Name, err)
  }
  ```

---

### 13. `proxyHandler` 4xx 响应体未经截断即转发

- **文件：** `main.go:669-677`
- **风险：** 低
- **描述：** 4xx 客户端错误直接透传上游响应。若上游返回意外的大响应体（如 HTML 错误页面），`io.Copy(w, resp.Body)` 会完整转发到客户端。虽非安全问题，但可能浪费带宽。

- **建议修复：** 可设置 `io.CopyN` 限制或记录 4xx 响应体大小。

---

### 14. Dashboard `updateStatus` 仅当 Overview 页面可见时才请求

- **文件：** `dashboard.html:454-455`
- **风险：** 低（性能建议）
- **描述：** `updateStatus` 检查当前面板是否为 overview，但 `updateLogs`（行 475）和 `loadRuntimeKeys`（行 560，通过 5s interval 调用）无此检查。三个轮询间隔统一为 5s，即使面板不可见也持续请求。

- **建议修复：** 统一通过 `visibilitychange` 事件控制轮询的启停，减少后台网络开销。

---

### 15. 测试脚本：`Invoke-AlvusGet` / `Invoke-AlvusPost` 未正确释放所有资源

- **文件：** `regression_test.ps1:81-132`
- **风险：** 低
- **描述：** 函数中手动管理 `StreamReader`/`ResponseStream` 的 Close，但在异常路径（catch 块）的 `$_.Exception.Response` 处理中没有 Close response stream。每次测试产生的泄漏不大，但多轮测试后可能积少成多。

- **建议修复：** 使用 `try/finally` 确保 `$resp.Close()` 在异常路径也被调用。

---

## 轻微问题 / 建议

### 16. 不一致的响应体关闭模式

- **文件：** `main.go` 各处的 `resp.Body.Close()` 调用
- **描述：** 部分分支使用 `defer resp.Body.Close()`（无），全部使用显式关闭。建议统一使用 `defer`，减少遗漏风险。当前代码中每个分支都显式关闭了，但后续维护者添加新分支时容易遗漏。

### 17. `loadDotEnv` 无法区分"文件不存在"和"文件无权限"

- **文件：** `main.go:878-879`
- **描述：** `os.ReadFile` 失败时静默返回（`return`）。若 `.env` 文件存在但权限不足，错误不会被报告，用户不知道配置未加载。

### 18. `reloadConfig()` 先 Unsetenv 再读取 .env

- **文件：** `main.go:305-311`
- **描述：** 若 `.env` 中不包含某个变量（如 `MAX_RETRIES`），`reloadConfig()` 会将其 unset，然后 `buildConfig()` 使用 fallback 默认值。这符合预期行为，但如果在 reload 过程中有并发代码 `os.Getenv("PORT")`，可能得到空值。虽然当前代码中没有这种情况，但作为 API 契约值得注意。

### 19. `maskKey` 对短 key 的遮盖可能不够

- **文件：** `main.go:24-29`
- **描述：** 对于长度 <= 12 的 key，统一返回 `"****"`。如果所有 key 都短，Dashboard 上会显示多个完全相同的 `"****"`，无法区分。

### 20. Dashboard 日志搜索使用全 JSON 字符串匹配

- **文件：** `dashboard.html:486`
- **描述：** `JSON.stringify(log).toLowerCase().includes(search)` 搜索整个 JSON 序列化后的字符串，包括 `key_index`、`request_body_size` 等数字字段。用户输入数字可能匹配到不相关的字段，造成搜索困惑。

### 21. 测试脚本版权年份硬编码

- **文件：** `dashboard.html:431`
- **描述：** `&copy; 2026 Alvus Multi-Key Proxy` 年份写死，2027 年需手动更新。

### 22. `proxyHandler` 重试循环中 body 逐次分配

- **文件：** `main.go:627`
- **描述：** 每次重试都调用 `bytes.NewReader(bodyBytes)` 创建新的 Reader。由于 `bodyBytes` 不变，理论上可以复用同一个 `bytes.Reader` 并 `Seek` 到开头。当前方式简单且正确，仅在极高重试次数下有 GC 压力。

### 23. 测试脚本端口分配未实际 bind 验证

- **文件：** `regression_test.ps1:173-183`
- **描述：** `Get-FreePort` 仅在内存中记录已分配端口号，不实际 bind 验证端口空闲。若系统有其他进程占用该端口，测试将在 `Wait-ForEndpoint` 阶段超时失败，错误信息不够明确。

### 24. 测试脚本 `Test-ProcessManagement` 子进程发现依赖 WMI

- **文件：** `regression_test.ps1:470-483`
- **描述：** WMI 查询失败时回退到 `global:TestPids` 中非 manager 的进程。但如果测试运行时系统有其他 `alvus.exe` 实例（残留），可能匹配到无关进程。

---

## 按文件统计

| 文件 | 严重 | 中等 | 轻微 | 合计 |
|------|------|------|------|------|
| `main.go` | 7 | 2 | 4 | 13 |
| `manage.go` | 2 | 1 | 0 | 3 |
| `dashboard.html` | 0 | 1 | 2 | 3 |
| `regression_test.ps1` | 0 | 1 | 2 | 3 |
| **合计** | **9** | **5** | **8** | **22** |

---

## 摘要

本次审查发现 **9 个严重问题**、**5 个中等问题** 和 **8 个轻微问题**。

**最关键的三个修复优先级：**

1. **数据竞争（#1, #2）** — `healthHandler` 和 `configHandler` 在无锁下读取 `pool.keys`，并发 `keysHandler` 修改同一 slice 导致崩溃或未定义行为。必须修复。

2. **Manage 模式子进程生命周期管理（#6, #7）** — `Stop()` 不等待进程退出即清理目录，Windows 上必定触发"文件占用"错误。管理模式的可靠性基座必须加固。

3. **安全防御（#4, #5, #8）** — 日志泄漏、时序攻击、无请求体限制，三项在公开部署场景下构成实际威胁。

其余问题主要集中在代码一致性（响应体关闭模式）、测试健壮性和 Dashboard 细节。
