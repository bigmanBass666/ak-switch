# CC Switch (farion1231/cc-switch) Research Findings

> Research date: 2026-06-22
> Sources: GitHub README, official user manual (en/zh), GitHub Issues, community tutorials

---

## 1. What CC Switch Is

CC Switch is a **cross-platform desktop GUI application** (built with Tauri 2 / React / Rust) that serves as a **unified management panel** for AI coding CLI tools. It manages providers, API keys, MCP servers, prompts, skills, proxy routing, session history, and usage statistics across **seven supported tools**:

- Claude Code
- Claude Desktop
- Codex (OpenAI)
- Gemini CLI
- OpenCode
- OpenClaw
- Hermes Agent

**Key differentiator**: Instead of manually editing JSON/TOML/`.env` files for each CLI tool, you get a single visual interface with 50+ built-in provider presets, one-click switching, system tray quick-switch, and unified MCP + Skills management.

**Data storage**: All provider data is stored in a **SQLite database** at `~/.cc-switch/cc-switch.db`, with device-level settings in `~/.cc-switch/settings.json`. Auto-backups are kept in `~/.cc-switch/backups/` (last 10).

---

## 2. How Proxy Routing Works (App Takeover / Local Routing)

### Core Concept

CC Switch can act as a **local HTTP proxy** that intercepts API requests from CLI tools and forwards them to the configured provider. This is referred to as **"App Takeover"** (or **"Local Routing"** in newer docs, or **"代理服务/Proxy Service"** in Chinese).

### Two Key Parts

#### 2.1 Proxy Service (Local HTTP Proxy)

- Starts an HTTP proxy server on `127.0.0.1:15721` (configurable)
- All API requests flow through this local proxy
- Features: request logging, usage stats, failover support, centralized management

#### 2.2 App Routing (Takeover)

- When enabled, CC Switch **modifies the CLI tool's config file** to point the API endpoint to the local proxy
- The proxy then forwards requests to the actual provider endpoint

**Claude config change (routing mode):**
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:15721"
  }
}
```

**Codex config change:**
```toml
base_url = "http://127.0.0.1:15721/v1"
```

**Gemini config change:**
```bash
GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:15721
```

### How CC Switch Interacts with a Backend Proxy Like Alvus

When using CC Switch with a local backend proxy (like Alvus):

```
CLI Tool (Claude/Codex/Gemini)
    |
    v
CC Switch Local Proxy (127.0.0.1:15721)
    |-- records request logs
    |-- performs API format conversion (if needed)
    |-- applies failover logic (if configured)
    v
Provider Endpoint (e.g., Alvus running locally at 127.0.0.1:11434/v1)
    |
    v
Upstream API (Anthropic / OpenAI / etc.)
```

**Key point**: CC Switch is configured with a "Provider" entry that points to Alvus as the backend. When routing is enabled, the CLI tool sends requests to CC Switch's local proxy, which then forwards them to Alvus. The provider's `base_url` in CC Switch must point to Alvus's address.

### Request Flow (Detailed)

1. CLI tool sends API request to `http://127.0.0.1:15721` (the CC Switch proxy)
2. CC Switch identifies the request source (Claude/Codex/Gemini)
3. CC Switch looks up the currently enabled provider for that app
4. CC Switch records the request log and usage stats
5. CC Switch forwards the request to the provider's actual endpoint (e.g., Alvus)
6. Alvus further forwards the request upstream (e.g., Anthropic API)
7. The response flows back through the same chain

### API Format Conversion

The proxy supports **automatic API format conversion** for providers configured with non-Anthropic formats:

| Provider API Format | Proxy Behavior |
|---------------------|----------------|
| Anthropic Messages | Pass-through (no conversion) |
| OpenAI Chat Completions | Converts Anthropic requests <-> OpenAI Chat format |
| OpenAI Responses API | Converts Anthropic requests <-> OpenAI Responses format |

This is configured per-provider in the Advanced Options when adding/editing a Claude provider.

### Routing vs Non-Routing Mode

| Aspect | Routing Mode | Non-Routing Mode |
|--------|-------------|------------------|
| Config change | Writes local proxy URL to CLI config | Writes provider URL directly to CLI config |
| Provider switch | Instant (no restart needed) | Requires CLI restart |
| Logging | Yes (all requests recorded) | No |
| Failover | Available | Not available |
| Latency | ~<10ms additional | None |

---

## 3. How Logging Works

### 3.1 Proxy Request Logs

When the proxy service is running and app routing is enabled, **every API request** that passes through the proxy is logged.

**Prerequisites:**
1. Proxy service must be started
2. App routing must be enabled for the relevant app
3. Log recording must be enabled in proxy settings

**Log contents (each request):**

| Field | Description |
|-------|-------------|
| Time | Request timestamp |
| App | Claude / Codex / Gemini |
| Provider | Provider used |
| Model | Requested model |
| Input Tokens | Input token count |
| Output Tokens | Output token count |
| Cache Read | Cache hit token count |
| Cache Creation | Cache creation token count |
| Latency | Request duration |
| Time to First Token | TTFB for streaming requests |
| Status | HTTP status code (200/400/401/429/500/etc.) |
| Total Cost | Estimated cost (USD) |
| Stream/Non-stream | Request type |

**Storage**: Logs are stored in the SQLite database in the `proxy_request_logs` table.

### 3.2 CLI Session Logs (v3.13+)

In addition to proxy logs, CC Switch can import usage data from CLI session history files:

- **Claude sessions**: Direct import from session logs
- **Codex sessions**: Precise parsing based on JSONL session logs
- **Gemini sessions**: Synced from Gemini CLI session logs

This does NOT require proxy interception; CC Switch periodically scans session directories.

### 3.3 Application Logs (Debug Logs)

CC Switch also has its own application-level logging for debugging:

| Setting | Description |
|---------|-------------|
| Enable Logging | Whether to record application logs |
| Log Level | error / warn / info / debug / trace |

These are separate from proxy request logs and contain the application's internal operations.

---

## 4. Viewing / Accessing Logs

### Method 1: GUI - Settings > Usage Tab

The primary way to view proxy request logs:
- Open **Settings > Usage** tab
- Filter by: app type, status code, provider, model, time range
- Click a request row to see detailed info (request params, response summary, error messages)

### Method 2: GUI - Proxy Panel

- The proxy panel shows real-time statistics: active connections, total requests, success rate, uptime
- Failover queue shows provider health status

### Method 3: Debug Log Files (for troubleshooting)

To see **detailed request/response content** (including actual API payloads):

1. Open **Settings > Advanced > Log Configuration**
2. Enable logging
3. Set log level to **debug** (or **trace** for maximum verbosity)
4. Check the log files in:

| OS | Log File Location |
|----|-------------------|
| **Windows** | `%APPDATA%\cc-switch\logs\` |
| **macOS** | `~/.cc-switch/logs/` |
| **Linux** | `~/.cc-switch/logs/` |

These debug logs contain the full request/response bodies, which is useful for diagnosing issues like 502 errors, format conversion problems, or why a request fails.

### Method 4: SQLite Database (Advanced)

All proxy request logs are stored in `~/.cc-switch/cc-switch.db` in the `proxy_request_logs` table. You can query it directly with any SQLite browser:

```sql
-- Example: view recent failed requests
SELECT * FROM proxy_request_logs WHERE status >= 400 ORDER BY created_at DESC LIMIT 50;
```

---

## 5. Log File Locations on Different OS

### CC Switch Own Data

| Item | Location |
|------|----------|
| Main database | `~/.cc-switch/cc-switch.db` |
| Device settings | `~/.cc-switch/settings.json` |
| Debug logs | **Windows**: `%APPDATA%\cc-switch\logs\` |
| Debug logs | **macOS/Linux**: `~/.cc-switch/logs/` |
| Auto-backups | `~/.cc-switch/backups/` (keeps last 10) |
| Skills | `~/.cc-switch/skills/` |

### CLI Tool Configuration Files

| Tool | Config Location |
|------|----------------|
| Claude Code | `~/.claude/settings.json`, `~/.claude.json` (MCP) |
| Codex | `~/.codex/config.toml`, `~/.codex/auth.json` |
| Gemini CLI | `~/.gemini/.env`, `~/.gemini/settings.json` |
| OpenCode | `~/.config/opencode/opencode.json` |
| OpenClaw | `~/.openclaw/openclaw.json` |
| Hermes | `~/.hermes/config.yaml` |

---

## 6. How to Debug "Claude Code Stops Mid-Session" from CC Switch's Side

This issue (Claude Code stopping unexpectedly mid-session) can have multiple root causes. Here is a systematic debugging approach using CC Switch's tools:

### Step 1: Check Proxy Health

- **GUI**: Look at the proxy panel -- is the proxy status green (running)?
- **Statistics**: Check "Active Connections" and "Success Rate" in the proxy panel
- **Failover queue**: Are any providers showing red (unhealthy)?

### Step 2: Enable Debug Logging

1. Go to **Settings > Advanced > Log Configuration**
2. Enable logging
3. Set log level to **debug** (or **trace**)
4. Reproduce the issue (run Claude Code until it stops)

### Step 3: Check Proxy Request Logs for Errors

In **Settings > Usage** tab, filter by:
- **Status code**: Look for 4xx/5xx errors around the time of the failure
- **Failed requests**: Check error details by clicking individual request rows
- **Common error patterns**:
  - **502 Bad Gateway**: Upstream provider (e.g., Alvus) is down or unreachable
  - **401 Unauthorized**: API key expired or invalid
  - **429 Too Many Requests**: Rate limited
  - **500 Internal Server Error**: Provider-side issue
  - **Timeout**: Network or provider latency issue

### Step 4: Check Failover Activity

If failover is configured:
- Check if failover was triggered during the session
- Look at how many times providers were switched
- A provider that keeps failing will be circuit-broken (skipped) for the configured recovery period

### Step 5: Check Circuit Breaker Status

In the proxy panel's failover queue:
- **Green**: Healthy (0 consecutive failures)
- **Yellow**: Degraded (1-2 consecutive failures)
- **Red**: Circuit broken (3+ consecutive failures, provider skipped)

If all providers are circuit-broken, all requests will fail until the recovery wait time expires.

### Step 6: Check Config File Integrity

- Ensure routing mode hasn't left a stale `ANTHROPIC_BASE_URL` pointing to the proxy when the proxy is stopped
- Check `~/.claude/settings.json` -- the `env.ANTHROPIC_BASE_URL` should be correct

### Step 7: Verify Backend Proxy (Alvus) Is Running

- If CC Switch is routing to Alvus, ensure Alvus is actually running and healthy
- Test Alvus directly: `curl http://127.0.0.1:11434/v1/models` (or whatever port Alvus uses)
- Check Alvus's own logs

### Step 8: Check for Known Proxy Issues

From the documentation and community reports:

| Symptom | Likely Cause | Solution |
|---------|-------------|----------|
| Requests fail after enabling routing | Proxy not running / provider config wrong | Check proxy status, verify provider endpoint |
| Config not restored after disabling routing | Proxy exited abnormally | Manually edit provider and re-save |
| Request timeout | Network / provider server / proxy config | Check network, test direct API access |
| 502 errors | Upstream provider down | Check if Alvus is running; check network |
| Failover not triggering | Proxy not running / takeover not enabled / no backup providers | Verify all 3 prerequisites |
| Old token causing 401 | Token restored from backup | Edit cc-switch.db to remove old tokens |

### Step 9: Check Log Files for Partial Responses

In debug log files (`~/.cc-switch/logs/` or `%APPDATA%\cc-switch\logs\`):
- Look for **streaming interruptions** -- partial responses that stop mid-stream
- Check for **timeout entries** in streaming requests
- Look for **connection reset** errors

### Step 10: Cross-reference with CLI Tool's Own Logs

- Claude Code session logs: `~/.claude/sessions/` (or VS Code extension logs)
- Compare timestamps with CC Switch proxy logs to find the exact point of failure

---

## 7. Failover Mechanism Details

### How Failover Works

When a request fails, CC Switch can automatically try the next provider in the queue:

```
Request -> Primary Provider -> Failed? -> Retry (up to max retries) -> Next Provider in Queue -> ...
```

### Circuit Breaker States

| State | Description |
|-------|-------------|
| Closed | Normal state, requests allowed |
| Open | Circuit broken, provider skipped |
| Half-Open | Attempting recovery (sends probe requests) |

### Default Circuit Breaker Settings

| Setting | General Default | Claude Default |
|---------|----------------|----------------|
| Failure Threshold | 4 consecutive failures | 8 consecutive failures |
| Recovery Success Threshold | 2 successes | 3 successes |
| Recovery Wait Time | 60 seconds | 90 seconds |
| Error Rate Threshold | 60% | 70% |
| Min Requests Before Rate Calc | 10 | 15 |
| Max Retries | 3 | 6 |
| Stream First Byte Timeout | 60s | 90s |
| Stream Idle Timeout | 120s | 180s |

### Failover Logs

Each failover event records: time, original provider, new provider, failure reason. These are visible in the request logs in usage statistics.

---

## 8. Configuration Files and Data Storage

### CC Switch Storage Directory: `~/.cc-switch/`

```
~/.cc-switch/
├── cc-switch.db         # SQLite database (SSOT for all config)
├── settings.json        # Device-level UI settings
├── skills/              # Skill SSOT directory
├── skill-backups/       # Skill backups (created on uninstall)
└── backups/             # Database auto-backups (keeps 10)
```

### SQLite Database Tables

| Table | Contents |
|-------|----------|
| providers | Provider configurations |
| provider_endpoints | Provider endpoint candidate list |
| mcp_servers | MCP server configurations |
| prompts | Prompt presets |
| skills | Skill installation status |
| skill_repos | Skill repository configurations |
| proxy_config | Proxy configuration |
| proxy_request_logs | Proxy request logs |
| provider_health | Provider health status |
| model_pricing | Model pricing |
| settings | App settings |

### Configuration Priority

1. **CC Switch Database** (Single Source of Truth)
2. **Live Configuration Files** (written when switching providers)
3. **Backfill Mechanism** (reads from live files when editing current provider)

---

## 9. Known Issues with Proxy Routing

### Issue: Proxy Service Fails to Start
- **Cause**: Port 15721 already in use
- **Solution**: Check with `netstat -ano \| findstr :15721` (Windows) or `lsof -i :15721` (macOS/Linux); change port or kill the occupying process

### Issue: Config Not Restored After Disabling Routing
- **Cause**: Proxy exited abnormally (crash, force kill)
- **Solution**: Manually edit the provider and re-save, or re-enable then disable routing

### Issue: Old Tokens Restored from Backup
- **Cause**: CC Switch's backfill mechanism may restore expired tokens from its database
- **Solution**: Manually edit the provider in CC Switch, verify the API key is current, and save

### Issue: API Format Confusion (Claude + OpenAI-compatible providers)
- **Cause**: When using Claude Code with an OpenAI-compatible provider (like Alvus), the `apiFormat` must be explicitly set (e.g., `openai_responses` or `openai_chat`)
- **Solution**: In provider advanced options, set the correct API format for the upstream provider

### Issue: Streaming Interruptions Mid-Response
- **Potential causes**:
  - Stream idle timeout (default 120s general, 180s for Claude)
  - Backend proxy (Alvus) connection timeout
  - Network interruption
- **Solution**: Check timeout settings in failover config; verify backend proxy health

### Issue: All Providers Circuit-Broken
- All providers in the failover queue are being skipped
- **Solution**: Wait for recovery wait time to expire, or restart proxy service to reset states

### Issue: Failover Triggering Too Frequently
- **Cause**: Unstable primary provider or circuit breaker threshold too low
- **Solution**: Increase failure threshold and recovery wait time; consider changing primary provider

---

## 10. Additional Debugging Techniques

### Using the Usage Query Feature

- Providers configured with Usage Query templates can show remaining quota/balance
- Useful for knowing if a provider has been rate-limited or exhausted

### Model Test (Stream Check)

Settings > Advanced > Model Test Config allows testing if a provider endpoint is actually working:
- Sends a short test request
- Measures latency and TTFB
- Reports health status: Healthy (green), Degraded (yellow), or Unavailable (red)

### Speed Test

Each provider card has a speed test option to measure endpoint latency before using it in a real session.

### Global Outbound Proxy

CC Switch can itself use an outbound HTTP/HTTPS proxy for external API access. If you're using a VPN or system proxy, this may interact with the local routing proxy.

---

## 11. Summary: How CC Switch Communicates with Backend Proxies (Like Alvus)

1. **User configures a "Provider" in CC Switch** with Alvus's endpoint (e.g., `http://127.0.0.1:11434/v1`) as the `base_url`
2. **User enables proxy/local routing** in CC Switch
3. **CC Switch modifies the CLI tool's config** to point `ANTHROPIC_BASE_URL` (for Claude) to `http://127.0.0.1:15721`
4. **When the CLI tool makes API calls**, they go to CC Switch's proxy
5. **CC Switch records the request** and forwards it to Alvus
6. **Alvus processes/reformats/forwards** the request to its upstream API
7. **The response flows back**: Alvus -> CC Switch (logged) -> CLI tool

This means CC Switch is **NOT a replacement for Alvus** -- it is a **management layer that sits between the CLI tool and Alvus**. Alvus handles the actual API relay/proxy to the upstream provider (Anthropic, OpenAI, etc.), while CC Switch handles:
- Unified provider switching
- Request logging and usage tracking
- API format conversion (if needed)
- Failover between providers
- Session management
- MCP/Skills/Prompts management
