# 已归档：早期规划草案（planning-v0）

本目录是 2026-10-23 开发规划过程中产生的**早期草案**，已被当前真源取代，仅作历史保留：

| 归档文件 | 当年用途 | 现在以何为准 |
|---|---|---|
| 开发计划-v1.0.md | 59 任务波次计划 | [开发计划](../../IMPLEMENTATION_PLAN.md) + [tasks.json](../../tasks/tasks.json)（36 项 DAG） |
| 多Agent协作规范.md | 任务卡与协作规则 | [原生多 agent 协作协议](../../AGENT_ORG.md) + AGENTS.md |
| 工程规范.md | Go/前端工程约束 | [AGENTS.md](../../../AGENTS.md) + [VALIDATION_PLAN](../../VALIDATION_PLAN.md) + 各任务卡 |
| 设计系统与UI规范.md | 视觉规范草案 | [UI_SPEC](../../UI_SPEC.md) + [design/tokens.json](../../../design/tokens.json) |
| backlog/ | 59 任务 YAML 与校验器 | [tasks.json](../../tasks/tasks.json) + [scripts/plan.py](../../../scripts/plan.py) |

**不要**把这里的任务编号、文件路径、工期或视觉取值当作当前实现依据。两份计划的内容有重叠，但版本、任务 ID、真源路径均不同；以 `docs/CURRENT_STATE.md` 指向的文档为准。

保留原因：其中的检查清单、成本估算与失败教训仍有参考价值；归档而不是删除，便于对照「为什么改成了现在的结构」。
