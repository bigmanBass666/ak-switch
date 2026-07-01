# Claude Code / Codex 工具调用后静默中断分析报告

> 分析日期: 2026-06-23
> 来源: CC Switch GitHub Issues, Anthropic Messages API 协议分析, 同类路由项目(LiteLLM/new-api/one-api)调研
> 发现: 10 个直接关联 CC Switch Issue + 3 个同类项目 Issue + 协议源头分析
> 最终结论: 根因在 sensenova/DeepSeek 的 thinking mode + 工具调用兼容性问题, Alvus 和 CC Switch 都只是中间层

---

## 一、症状确认

| 特征 | 详情 |
|------|------|
| **触发条件** | 调用工具后（如 `/docx`、Bash、Read 等） |
| **速度** | **5 秒内**立即停止，不是超时 |
| **表现** | 显示 `"running stop hook"` 后像正常结束一样退出 |
| **错误信息** | 无任何报错或警告 |
| **受影响工具** | **Claude Code（Anthropic 协议）** 和 **Codex（OpenAI 协议）** 都出现 |
| **发生时机** | 新对话一开始正常，多轮对话后更频繁 |

---

## 二、"running stop hook" 的真实含义

根据 Claude Code 源码分析（`query/stopHooks.ts`），`"running stop hook"` **不是"即将停止"**，而是**已经在执行终止清理**。

Claude Code 的查询循环终止判断：

```
终止条件检查（每轮循环）：
  stop_reason == "end_turn"?     → 判定对话结束 → handleStopHooks → 退出
  stop_reason == "max_tokens"?   → Token 超限
  stop_reason == "tool_use"?     → 继续循环（期待更多工具调用）
```

所以看到 `"running stop hook"` 意味着：

> **Claude Code 已经收到了 API 返回的 `stop_reason: "end_turn"`，认为对话自然而然地结束了。**

---

## 三、根因分析

### 根因 A（最可能）：Failover 切换后 API Base URL 未同步 — Issue #3486

**状态**: OPEN | **影响**: Claude Code + Codex

CC Switch failover 机制存在关键缺陷：故障转移时，**模型名和 API Key 正确切换了，但 `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` 仍指向旧 Provider**。

```
Provider A（DeepSeek，URL: https://token.sensenova.cn/v1）出问题
  ↓ CC Switch failover
Provider B（比如通义千问，URL: https://dashscope.aliyuncs.com/v1）
  但请求 URL 仍然是 https://token.sensenova.cn/v1
  ↓
DeepSeek 端点收到 Provider B 的模型名（如 qwen-max）
  → 不认识的模型名 → HTTP 400
  ↓
连续 400 → 熔断器触发 → 所有请求被静默拒绝
  ↓
Claude Code 收到 end_turn → "running stop hook" → 退出
```

**这是一切问题的根源**，因为它完美解释：
- 两个工具都出问题（failover 是所有 app 公用的代理层功能）
- 5 秒内停止（HTTP 400 + 熔断是毫秒级）
- 无错误提示（熔断器静默拒绝）
- 新对话正常（failover 触发需要时间）
- 工具调用更容易触发（工具用 request 更复杂，失败概率更高）

### 根因 B（顺位第二）：thinking-only 响应 + end_turn 映射错误 — Issue #3654

**对应 Issue**: CC Switch #3654（OPEN）

当 tool_result 提交后，DeepSeek 兼容 API 返回**仅包含 thinking 块**的响应（没有 text 块，没有 tool_use 块），然后 CC Switch 在 SSE 转换中发出：

```
SSE: [thinking] → message_delta { stop_reason: "end_turn" }
                                            ↑ 应为 "tool_use"！
```

### 根因 C：热切换导致转发 URL 循环指向自身 — Issue #4014

**状态**: OPEN | **影响**: Claude Code + Codex

在热切换（实时配置更新）期间，CC Switch 的转发 URL 短暂指向自身 `http://127.0.0.1:15721`，1 秒内产生 20+ 次 **502 Bad Gateway** → 熔断器跳闸 → 所有请求被静默拒绝。

用户在压测时频繁切换 provider（Codex → Claude），热切换很可能触发此问题。

### 根因 D：缺少内容健康检查 — Issue #3488

**状态**: OPEN | **影响**: Claude Code + Codex

熔断器只检查 **HTTP 状态码**（4xx/5xx），不检查响应内容。如果一个上游返回：
- HTML 登录页面（HTTP 200）
- 验证码拦截页面（HTTP 200）  
- 格式错误但 HTTP 200 的响应

熔断器永远不会触发（认为请求成功），但 Claude Code/Codex 收到无法解析的内容 → 静默停止。

### 根因 E：SSE chunk 合并导致 message_delta 丢失

**对应 Issue**: CC Switch #2354（已在 #2366 修复）、new-api #4697（OPEN）

上游 API 将 `finish_reason` 和 `usage` 分在两个 SSE chunk 返回。CC Switch 的 `streaming.rs` 在处理 chunk 1 时因为 `usage` 为 null，把整个 `message_delta` 当成不完整数据。chunk 2 到达时被丢弃，连接在没有 message_stop 的情况下断开。

### 根因 F：思考占位符注入破坏历史结构

**对应 Issue**: CC Switch #4208（OPEN）、PR #4210

CC Switch 的 `claude.rs` 在每次 tool_use 后向历史注入假 thinking 块，多轮累积后消息历史被扭曲，上游模型无法处理。

---

## 四、六个根因的叠加效应

```
调用工具（如 /docx）
  ↓
CC Switch 注入假 thinking 块到请求历史（#4208）→ 上游困惑
  ↓
上游返回 thinking-only 响应（#3654）
  ↓ 或
SSE chunk 被 streaming.rs 丢弃（#2354）
  ↓ 或
Failover 切换后 Base URL 未同步（#3486）→ 所有请求 400
  ↓ 或
热切换引发 URL 自引用循环（#4014）→ 20+ 次 502
  ↓
熔断器闭合 或 CC Switch 发出错误的 end_turn
  ↓
Claude Code 收到 end_turn → 认为对话结束
  ↓
"running stop hook" → 正常退出，无报错
```

**这不是单一 bug，而是 CC Switch 中至少 6 个活跃 bug 的组合效果。**

其中 **#3486（failover Base URL 未同步）是唯一能解释"两个协议都出问题"的根因**，因为 failover 是所有 app 共享的代理层功能。

---

## 五、同类项目也有相同问题

| 项目 | Issue | 问题 |
|------|-------|------|
| **LiteLLM** | #29491 (#25561) | 缓冲/合并 SSE chunk 时改变块边界，丢失 `input_json_delta`，工具 input 为空 |
| **new-api** | #4697、#5345 | 未发出 `content_block_stop`/`message_delta`/`message_stop`，Claude Code 无法终结当前轮次 |
| **new-api** | #5229 | `thinking` 块导致内容索引错乱，tool_use 块索引不是 0，收到 `input={}` |
| **raine/claude-code-proxy** | commit da8e92f | `max_output_tokens` 截断后 `stop_reason` 被错误映射为 `end_turn` |

---

## 六、解决方案

### 即时方案（走 Alvus）

| 措施 | 效果 |
|------|------|
| **CC Switch 切到 Alvus provider** | Alvus 纯透传，不修改任何消息内容，绕过 CC Switch 所有整流器 |

### 快速验证方案

```bash
# 设置环境变量禁用 thinking 块，跳过所有 thinking 相关 bug
export CLAUDE_CODE_DISABLE_THINKING=1
```

如果设置后问题消失，则确认根因在 thinking 块处理上。

### 等 CC Switch 修复

| Issue | 修复进展 | 状态 |
|-------|---------|------|
| #3654（thinking-only 响应） | 开放，v3.16.1 未完全修复 | 🔴 |
| #4208（注入假 thinking 块） | PR #4210 已提交，95 测试通过 | 🟡 等合并 |
| #2354（SSE chunk 丢弃） | #2366 已修复 | ✅ |
| #4341（Codex 中断） | 开放，无修复 PR | 🔴 |

---

## 七、验证方法

1. **直接走 Alvus 测试**（推荐）：看工具调用是否正常
2. **设 `CLAUDE_CODE_DISABLE_THINKING=1`**：看问题是否消失
3. **抓包验证**：捕获 SSE 事件流，检查 `message_delta` 中的 `stop_reason` 值
4. **降级 CC Switch 到 v3.14.1**：看问题是否消失（确认是否为 v3.16.0+ 引入）
5. **更换模型**：换非 reasoning 模型看问题是否消失

---

## 八、参考资料

### CC Switch 相关 Issues

| # | 标题 | 状态 | 关联 |
|---|------|------|------|
| #3486 | Failover 后 API Base URL 未同步 | 🔴 OPEN | **最高概率**，影响两个协议 |
| #3654 | DeepSeek V4 tool-use 后无最终回答（仅 thinking） | 🔴 OPEN | 精确匹配 Claude Code 症状 |
| #4208 | 合成 thinking placeholder 破坏长会话 | 🔴 OPEN | PR #4210 修复中 |
| #4014 | 热切换 URL 自引用循环 → 502 | 🔴 OPEN | 压测切换 provider 时触发 |
| #3488 | 缺内容健康检查（HTTP 200 错误被忽略） | 🔴 OPEN | 上游返回 HTML/验证码时不报错 |
| #2354 | 工具调用后 connection error（SSE chunk 丢弃） | ✅ 已修 (#2366) | |
| #4341 | Codex 接入第三方模型对话自动中断 | 🔴 OPEN | 多个用户确认 |
| #3645 | thinking-only 无 text 块 | 🔴 OPEN | v3.16.0 回归 |
| #2433 | 200+error body 检测/failover | ✅ 已合并 | 可触发 failover |
| #4531 | Codex 400 tool_call_id | 🔴 OPEN | 工具调用后会话死 |

### 同类项目 Issues

| 项目 | # | 问题 |
|------|---|------|
| LiteLLM | #29491 | 合并 SSE chunk 时丢失 input_json_delta |
| new-api | #4697 | 未发出 content_block_stop/message_delta/message_stop |
| new-api | #5229 | thinking 块导致内容索引错乱 |
| claude-code | #58756 | 空文本+空 thinking+单 tool_use 后静默终止 |
| claude-code | #47931 | 并行 tool_results 后会话静默终止 |
| claude-code | #16785 | 通过 BASE_URL 代理时 stop_reason 无法解析 |

### 协议文档

- Anthropic Messages API 流式文档
- Claude Code 源码: `query/stopHooks.ts`
- cc-switch: `claude.rs` normalize_anthropic_tool_thinking_history
