# Regression Test 健壮性审查

**文件**: `D:\Test\Alvus-fork\regression_test.ps1`
**审查日期**: 2026-06-24
**测试规模**: 28 个用例，4 个测试套件

## 总结

整体架构合理（独立端口、`try/finally` 清理、`$global:TestPids` 追踪），但存在两个 **High** 级别的 HTTP 响应泄漏问题，以及 WMI 降级路径下的测试语义错误。以下是按严重度排序的发现。

---

## 发现

### 1. [High] `Wait-ForEndpoint` 成功匹配时泄漏 HTTP 响应

**文件位置**: 第 70-72 行

```powershell
if ($resp.StatusCode -eq $ExpectedStatus) { return $true }  # ← 直接 return，$resp.Close() 未执行
$resp.Close()
```

当状态码匹配时，`return $true` 绕过 `$resp.Close()`，每次健康检查成功都泄漏一个 `HttpWebResponse` 对象。由于几乎所有测试都依赖 `Wait-ForEndpoint` 判断就绪，28 个测试中至少有 10+ 次调用，连接泄漏可能累积到 `ServicePointManager.DefaultConnectionLimit` 导致后续请求阻塞。

**修复**: return 前先关闭响应：

```powershell
if ($resp.StatusCode -eq $ExpectedStatus) { $resp.Close(); return $true }
```

---

### 2. [High] `Invoke-AlvusGet` / `Invoke-AlvusPost` 错误路径泄漏 `$resp`

**文件位置**: `Invoke-AlvusGet` 第 93-98 行，`Invoke-AlvusPost` 第 123-128 行

```powershell
catch {
    if ($_.Exception.Response) {
        $resp = $_.Exception.Response
        $reader = New-Object System.IO.StreamReader($resp.GetResponseStream())
        $body = $reader.ReadToEnd()
        $reader.Close()
        # ← $resp.Close() 缺失!
        return @{ StatusCode = [int]$resp.StatusCode; ... }
    }
    return @{ StatusCode = 0; ... }
}
```

捕获异常时读取了错误体并关闭了 `StreamReader`，但未关闭 `$resp`（`HttpWebResponse`）。这样的泄漏在连续出错（如端口暂未就绪时反复调用 GET/POST）场景下会加剧。

**修复**: `$reader.Close()` 后添加 `$resp.Close()`。也可以在两条路径共用 `finally` 块。

---

### 3. [Medium] WMI 降级路径导致子进程测试语义错误

**文件位置**: `Test-ProcessManagement` 第 470-483 行

当 `Get-CimInstance Win32_Process` 失败时（权限不足、WMI 服务异常），降级回退：

```powershell
$childPids = @($global:TestPids.Keys | Where-Object { $_ -ne $mgrProc.Id })
```

但 `$global:TestPids` 只记录通过 `Start-AlvusProcess` 启动的进程，Manager 自行 `fork` 的子进程并不在其中。降级后 `$childPids` 恒为空数组，导致：

- 第 483 行 `"子进程数量正确"` 断言失败（原因误报为"数量不够"，实际是 WMI 枚举失败）
- 第 486 行的子进程杀灭+自动重启验证被**静默跳过**，核心场景没有被测试

**修复**: WMI 降级后改为标记 `$wmiFailed = $true`，跳过依赖子进程 PID 的测试并明确输出"WMI 不可用，跳过子进程管理测试"而非返回空列表。

---

### 4. [Medium] 强制终止（Ctrl+C / 进程崩溃）下清理不可靠

**文件位置**: 第 640-646 行

```powershell
Register-EngineEvent -SourceIdentifier PowerShell.Exiting -SupportEvent -Action {
    taskkill /F /T /PID $pid
}
```

`Register-EngineEvent -SupportEvent` 在 PowerShell 正常退出时触发，但以下场景不会执行：

- 用户按下 Ctrl+C 中断脚本
- 宿主进程被 `taskkill` 强制终止
- PowerShell ISE / VS Code 集成终端意外关闭

这些情况会遗留孤儿 `alvus.exe` 进程占用端口。如果连续运行测试且端口被占用，后续测试会因 `Get-FreePort` 分配不同端口而勉强通过，但端口资源持续泄漏。

**修复**: 添加全局 `try/finally` 包裹整个主流程，或使用 `trap` 处理 `Ctrl+C`。

---

### 5. [Medium] 子进程重启测试使用固定 5 秒盲等

**文件位置**: 第 497 行

```powershell
Start-Sleep -Seconds 5
$restarted = Wait-ForEndpoint -Url "http://127.0.0.1:$port/health" -TimeoutSeconds 8
```

即使子进程在 1 秒内完成重启，测试依然固定等待 5 秒。这是 28 个测试中最长的单一步骤延迟，且不随实际重启速度自适应。

**修复**: 移除 `Start-Sleep -Seconds 5`，将 `Wait-ForEndpoint -TimeoutSeconds` 从 8 延长到 13（覆盖原来的 sleep+timeout 总和），改用指数退避轮询。

---

### 6. [Low] 死代码：`$global:CleanupJobs`

**文件位置**: 第 31 行

```powershell
$global:CleanupJobs = @()
```

变量声明后从未被读取或写入。可安全删除。

---

### 7. [Low] 死代码：第 22-25 行空 if 块

```powershell
if (-not ($MyInvocation.Line -match '-PortBase')) {
    # 默认安全，无需额外检查
}
```

既不赋值也不做任何操作。`-PortBase` 参数在脚本中也不存在。似乎是以前版本遗留的桩代码。

**修复**: 整块删除。

---

### 8. [Low] 端口范围 15000-16000 硬编码

**文件位置**: 第 176 行

CI 环境、Docker 容器或本地可能已有服务占用此范围内的端口，导致 `Get-FreePort` 误判"free"后实际 `bind` 失败。

**修复**: 将端口范围和基础值抽为参数，默认值保持不变但允许覆盖：

```powershell
param(
    [int]$PortRangeStart = 15000,
    [int]$PortRangeEnd = 16000
)
```

---

### 9. [Low] `Get-FreePort` 分配的端口永不归还

**文件位置**: 第 173-183 行

`$global:AllTestPorts` 内的端口条目在测试结束后从未被移除。虽然在单次脚本执行中够用（约分配 12 个端口），但如果脚本被连续调用（同一 PowerShell 进程内多次 dot-source），范围会逐渐耗尽。

**修复**: 添加 `Free-Port` 函数，在测试 `finally` 中显式归还端口。可改用一个单调递增计数器+回收队列代替哈希表。

---

### 10. [Low] `Test-ProcessManagement` 中双重重叠的 `Stop-AlvusProcess`

**文件位置**: 第 504 行（try 块内）和第 515 行（finally 块内）

第 504 行已显式停止 Manager，第 515 行 finally 中又调用一次。由于 `Stop-AlvusProcess` 防御性检查了 `$Proc.HasExited`，第二次调用不会报错，但暴露了代码结构问题：try 块内不应承担清理职责，清理应统一在 finally 中完成。

**修复**: 删除第 504 行的 `Stop-AlvusProcess $mgrProc`，仅依赖 finally 块做清理。try 块内只需设置标志或收集状态。