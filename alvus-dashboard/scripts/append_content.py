# -*- coding: utf-8 -*-
"""Append the main content and closing tags to keys.html"""
import os

# Read the partial file to check current state
filepath = r'd:\Test\Alvus-fork\alvus-dashboard\pages\keys.html'
with open(filepath, 'r', encoding='utf-8') as f:
    existing = f.read()

# Verify it ends with </header>
assert existing.rstrip().endswith('</header>'), f"Unexpected ending: {existing.rstrip()[-100:]}"

# Content to append
content = """
    <main class="content">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:1.25rem;">
        <div style="display:flex;align-items:center;gap:0.5rem;">
          <input type="text" placeholder="输入新的 API Key..." style="background:var(--color-surface);border:1px solid var(--color-border);border-radius:var(--radius-sm);padding:0.5rem 0.75rem;font-size:0.85rem;color:var(--color-text-primary);width:320px;">
          <button style="background:var(--color-primary);color:var(--color-text-inverse);padding:0.5rem 1rem;border-radius:var(--radius-sm);border:none;font-weight:500;cursor:pointer;font-size:0.85rem;">添加</button>
        </div>
        <div>
          <select style="background:var(--color-surface);border:1px solid var(--color-border);border-radius:var(--radius-sm);padding:0.45rem 0.65rem;font-size:0.85rem;color:var(--color-text-primary);outline:none;">
            <option value="nvidia-main">nvidia-main</option>
            <option value="openai-fallback">openai-fallback</option>
            <option value="custom-test">custom-test</option>
          </select>
        </div>
      </div>

      <div class="card" style="padding:0;overflow:hidden;">
        <table style="width:100%;border-collapse:collapse;font-size:0.85rem;">
          <thead>
            <tr style="border-bottom:1px solid var(--color-border);">
              <th style="padding:0.75rem 1rem;font-size:0.75rem;font-weight:500;text-transform:uppercase;letter-spacing:0.04em;color:var(--color-text-dim);text-align:left;width:40px;">#</th>
              <th style="padding:0.75rem 1rem;font-size:0.75rem;font-weight:500;text-transform:uppercase;letter-spacing:0.04em;color:var(--color-text-dim);text-align:left;">Key</th>
              <th style="padding:0.75rem 1rem;font-size:0.75rem;font-weight:500;text-transform:uppercase;letter-spacing:0.04em;color:var(--color-text-dim);text-align:left;">名称</th>
              <th style="padding:0.75rem 1rem;font-size:0.75rem;font-weight:500;text-transform:uppercase;letter-spacing:0.04em;color:var(--color-text-dim);text-align:left;">状态</th>
              <th style="padding:0.75rem 1rem;font-size:0.75rem;font-weight:500;text-transform:uppercase;letter-spacing:0.04em;color:var(--color-text-dim);text-align:left;">RPM</th>
              <th style="padding:0.75rem 1rem;font-size:0.75rem;font-weight:500;text-transform:uppercase;letter-spacing:0.04em;color:var(--color-text-dim);text-align:left;">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr style="border-bottom:1px solid var(--color-border);transition:background 0.1s ease;" onmouseover="this.style.background='var(--color-surface-hover)'" onmouseout="this.style.background='transparent'">
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">1</td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">nvapi-abc1...x1yz</span></td>
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">Key-01</td>
              <td style="padding:0.75rem 1rem;">
                <span style="display:inline-flex;align-items:center;gap:0.4rem;">
                  <span style="display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--color-state-success);"></span>
                  <span style="font-size:0.8rem;font-weight:500;color:var(--color-state-success);">Ready</span>
                </span>
              </td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">50</span></td>
              <td style="padding:0.75rem 1rem;">
                <div style="display:flex;gap:0.4rem;">
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #f59e0b;background:transparent;color:#f59e0b;cursor:pointer;">冷却</button>
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #ef4444;background:transparent;color:#ef4444;cursor:pointer;">删除</button>
                </div>
              </td>
            </tr>
            <tr style="border-bottom:1px solid var(--color-border);transition:background 0.1s ease;" onmouseover="this.style.background='var(--color-surface-hover)'" onmouseout="this.style.background='transparent'">
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">2</td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">nvapi-def2...x2yz</span></td>
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">Key-02</td>
              <td style="padding:0.75rem 1rem;">
                <span style="display:inline-flex;align-items:center;gap:0.4rem;">
                  <span style="display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--color-state-warning);"></span>
                  <span style="font-size:0.8rem;font-weight:500;color:var(--color-state-warning);">Cooling</span>
                </span>
              </td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">30</span></td>
              <td style="padding:0.75rem 1rem;">
                <div style="display:flex;gap:0.4rem;">
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #10b981;background:transparent;color:#10b981;cursor:pointer;">恢复</button>
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #ef4444;background:transparent;color:#ef4444;cursor:pointer;">删除</button>
                </div>
              </td>
            </tr>
            <tr style="border-bottom:1px solid var(--color-border);transition:background 0.1s ease;" onmouseover="this.style.background='var(--color-surface-hover)'" onmouseout="this.style.background='transparent'">
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">3</td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">nvapi-ghi3...x3yz</span></td>
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">Key-03</td>
              <td style="padding:0.75rem 1rem;">
                <span style="display:inline-flex;align-items:center;gap:0.4rem;">
                  <span style="display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--color-state-error);"></span>
                  <span style="font-size:0.8rem;font-weight:500;color:var(--color-state-error);">Disabled</span>
                </span>
              </td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">0</span></td>
              <td style="padding:0.75rem 1rem;">
                <div style="display:flex;gap:0.4rem;">
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #10b981;background:transparent;color:#10b981;cursor:pointer;">启用</button>
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #ef4444;background:transparent;color:#ef4444;cursor:pointer;">删除</button>
                </div>
              </td>
            </tr>
            <tr style="border-bottom:1px solid var(--color-border);transition:background 0.1s ease;" onmouseover="this.style.background='var(--color-surface-hover)'" onmouseout="this.style.background='transparent'">
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">4</td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">nvapi-jkl4...x4yz</span></td>
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">Key-04</td>
              <td style="padding:0.75rem 1rem;">
                <span style="display:inline-flex;align-items:center;gap:0.4rem;">
                  <span style="display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--color-state-success);"></span>
                  <span style="font-size:0.8rem;font-weight:500;color:var(--color-state-success);">Ready</span>
                </span>
              </td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">45</span></td>
              <td style="padding:0.75rem 1rem;">
                <div style="display:flex;gap:0.4rem;">
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #f59e0b;background:transparent;color:#f59e0b;cursor:pointer;">冷却</button>
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #ef4444;background:transparent;color:#ef4444;cursor:pointer;">删除</button>
                </div>
              </td>
            </tr>
            <tr style="transition:background 0.1s ease;" onmouseover="this.style.background='var(--color-surface-hover)'" onmouseout="this.style.background='transparent'">
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">5</td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">nvapi-mno5...x5yz</span></td>
              <td style="padding:0.75rem 1rem;color:var(--color-text-dim);">Key-05</td>
              <td style="padding:0.75rem 1rem;">
                <span style="display:inline-flex;align-items:center;gap:0.4rem;">
                  <span style="display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--color-state-warning);"></span>
                  <span style="font-size:0.8rem;font-weight:500;color:var(--color-state-warning);">Cooling</span>
                </span>
              </td>
              <td style="padding:0.75rem 1rem;"><span class="mono" style="font-size:0.8rem;">20</span></td>
              <td style="padding:0.75rem 1rem;">
                <div style="display:flex;gap:0.4rem;">
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #10b981;background:transparent;color:#10b981;cursor:pointer;">恢复</button>
                  <button style="font-size:0.75rem;padding:0.25rem 0.5rem;border-radius:var(--radius-sm);border:1px solid #ef4444;background:transparent;color:#ef4444;cursor:pointer;">删除</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </main>
    <script>lucide.createIcons();</script>
</body>
</html>
"""

with open(filepath, 'a', encoding='utf-8') as f:
    f.write(content)

print("Appended successfully. File size:", os.path.getsize(filepath))