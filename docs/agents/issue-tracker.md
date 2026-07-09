# Issue Tracker：GitHub

本仓库的 Issue 和 PRD 都保存在 GitHub Issues 中。所有操作使用 `gh` CLI。

## 约定

- **创建 Issue**：`gh issue create --title "..." --body "..."`。多行正文用 heredoc。
- **读取 Issue**：`gh issue view <编号> --comments`，用 `jq` 过滤评论并同时获取标签。
- **列出 Issue**：`gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'`，配合 `--label` 和 `--state` 过滤。
- **评论 Issue**：`gh issue comment <编号> --body "..."`
- **添加/删除标签**：`gh issue edit <编号> --add-label "..."` / `--remove-label "..."`
- **关闭**：`gh issue close <编号> --comment "..."`

仓库信息通过 `git remote -v` 自动推断——`gh` 在仓库克隆目录内会自动识别。

## PR 是否作为需求入口

**否。** 外部 PR 不作为 triage 流程的需求来源。

## 当技能说"发布到 issue tracker"

创建一条 GitHub Issue。

## 当技能说"获取相关 ticket"

运行 `gh issue view <编号> --comments`。

## 导航操作（Wayfinding）

由 `/wayfinder` 使用。**地图**是一个带子 Issue 的单一 Issue。

- **地图**：一张标记为 `wayfinder:map` 的 Issue，包含 Notes / Decisions-so-far / Fog 正文。`gh issue create --label wayfinder:map`。
- **子 ticket**：关联到地图的 Issue（通过 GitHub 子 Issue 机制，或在地图正文中使用任务列表 + 子 Issue 正文顶部加 `Part of #<编号>`）。标签：`wayfinder:<类型>`（`research`/`prototype`/`grilling`/`task`）。被认领后，ticket 会分配给负责的开发人员。
- **阻塞依赖**：使用 GitHub 原生 Issue 依赖关系。`gh api --method POST repos/<owner>/<repo>/issues/<子Issue编号>/dependencies/blocked_by -F issue_id=<阻塞者数据库ID>`。`<阻塞者数据库ID>` 通过 `gh api repos/<owner>/<repo>/issues/<编号> --jq .id` 获取（不是 Issue 编号 `#N`，也不是 `node_id`）。如果依赖关系不可用，回退到在子 Issue 正文顶部写 `Blocked by: #<编号>, #<编号>`。当所有阻塞者都被关闭时，ticket 才算解除阻塞。
- **前沿查询**：列出地图的开放子 Issue（`gh issue list --state open`，范围限定在地图的子 Issue/任务列表），排除有未关闭阻塞者或已被分配的任务；第一个匹配的获胜。
- **认领**：`gh issue edit <编号> --add-assignee @me`——会话的第一次写入操作。
- **解决**：`gh issue comment <编号> --body "<答案>"`，然后 `gh issue close <编号>`，最后在地图的 Decisions-so-far 中追加上下文指针（gist + 链接）。