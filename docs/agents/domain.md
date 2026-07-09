# 领域文档

工程技能在探索代码库时如何消费本仓库的领域文档。

## 探索前请先阅读

- **`CONTEXT.md`**（项目根目录），或
- **`CONTEXT-MAP.md`**（如果存在）——指向每个上下文各自的 `CONTEXT.md`。阅读所有与当前主题相关的上下文。
- **`docs/adr/`**——阅读你将要工作的领域相关的 ADR。在多上下文仓库中，还需检查 `src/<上下文>/docs/adr/` 中的上下文特定决策。

如果这些文件不存在，**直接跳过**。不要标记缺失，不要建议提前创建它们。`/domain-modeling` 技能（通过 `/grill-with-docs` 和 `/improve-codebase-architecture` 调用）会在术语或决策真正被确定时惰性创建它们。

## 文件结构

单上下文仓库（大多数仓库）：

```
/
├── CONTEXT.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```

多上下文仓库（根目录存在 `CONTEXT-MAP.md` 时）：

```
/
├── CONTEXT-MAP.md
├── docs/adr/                          ← 系统级决策
└── src/
    ├── ordering/
    │   ├── CONTEXT.md
    │   └── docs/adr/                  ← 上下文特定决策
    └── billing/
        ├── CONTEXT.md
        └── docs/adr/
```

## 使用词汇表中的术语

当你的输出提到领域概念时（在 Issue 标题、重构提案、假设、测试名称中），使用 `CONTEXT.md` 中定义的术语。不要使用词汇表明确避免的同义词。

如果你需要的概念不在词汇表中，这是一个信号——要么你在发明项目不使用的语言（重新考虑），要么确实存在缺口（记录下来供 `/domain-modeling` 处理）。

## 标记 ADR 冲突

如果你的输出与现有 ADR 矛盾，请明确提出来，而不是默默覆盖：

> _与 ADR-0007（事件溯源订单）矛盾——但值得重新讨论，因为…_