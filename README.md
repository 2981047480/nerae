# Nerae

面向自由教练与线上学员的「教练广场 + 训练计划广场 + AI 辅助」小程序。

- 学员（首期 18–28 岁，增肌 / 减脂）：跟现成计划练（训练 + 饮食；浏览 / 开始 / 记录 / 发布 / 打卡），需要被管时按教练自主定价选教练
- 教练：自主定价入驻、发布训练/饮食计划、AI 副驾、学员管理与结构化交付
- A 阶段**平台内不收款、不代收、不分账**：预约与咨询在平台内，成交在平台外，回平台建立服务关系完成交付

## 当前状态

**方案缺口修复 + 可委派开发规划；尚无业务应用。**

- 已有：PRD v1.10、ER v1.2、OpenAPI 1.2.0（99 路径）、Python 契约回归、APP_FLOW、UI_SPEC、37 个路线图工作包 + 25 张执行卡的单一 DAG 与只读任务包工具
- 不存在 / 未接受：Go 服务、正式 uni-app 应用、数据库迁移、真实 HTTP 权限/并发、MiMo 真实调用、微信真机、生产部署；本地Git仅为UI隔离准备，工程工具链尚未完整接受
- 任务状态以 tasks.json / plan.py 为准。第三轮 UI 已明确授权，新增 D01-01/02/03/I，先55项文稿，再指定 DeepSeek 实现四种代表情境和全量16界面族，Lead验收。正式U01–U07仍待工程与已接受设计基线；未细拆工作包不能直接派发。

详细现状见 [CURRENT_STATE](docs/CURRENT_STATE.md)，本轮任务细化与验证见 [PLAN_REFINEMENT](docs/PLAN_REFINEMENT.md)；此前契约/流程修复快照见 [修复对照 v1.2](docs/方案修复对照-v1.2.md)。

## 先读（按顺序）

| 文档 | 回答什么 |
|---|---|
| [AGENTS.md](AGENTS.md) | 工作约定：真源、边界、委派、验证、上下文预算 |
| [CURRENT_STATE](docs/CURRENT_STATE.md) | 现在有什么、没有什么、下一批候选 |
| [开发计划](docs/IMPLEMENTATION_PLAN.md) | 里程碑、工程结构、领域 owner、批次出口 |
| [APP_FLOW](docs/APP_FLOW.md) | 用户任务 F01–F07、日期语义、异常与动作预算 |
| [设计推导](docs/design/DESIGN_FOUNDATION.md) | 产品基因、用户需要、体验层级、取舍和待验证假设 |
| [UI_SPEC](docs/UI_SPEC.md) | 旧视觉基线撤销后的规格状态与行为质量边界 |
| [DECISION_LOG](docs/DECISION_LOG.md) | 已定取舍与重开条件 |
| [RELEASE_GATES](docs/RELEASE_GATES.md) | G0–G5 放行条件、申诉 SOP、试点统计 |
| [VALIDATION_PLAN](docs/VALIDATION_PLAN.md) | 场景 C01–C16、性能目标、视觉门槛 |
| [AGENT_ORG](docs/AGENT_ORG.md) | 角色、worktree、任务包、review 与集成协议 |
| [任务 DAG](docs/tasks/tasks.json) | 工作包与执行卡的依赖、写作用域、验收与证据 |
| [首批细化与吸收记录](docs/PLAN_REFINEMENT.md) | 五项改进、批内集成、内容责任与当前验证 |
| [工程标准](docs/ENGINEERING_STANDARDS.md) | 分层、错误/事务、环境、CI、后台与内容责任 |
| [统一完成定义](docs/DEFINITION_OF_DONE.md) | 七项清单、同提交证据、review和上手试验 |
| [可读任务目录](docs/tasks/README.md) | 从 DAG 生成的任务、依赖与估工速查 |
| [脚本 tools](scripts/plan.py) | `ready` / `packet` / `check`；只读，不改状态 |
| [交接摘要](docs/HANDOFF_SUMMARY.md) | 本轮结论与首批并行建议 |

## 契约与产品依据

| 文档 | 内容 |
|---|---|
| [PRD v1.10](docs/PRD.md) | 产品边界、页面、AI 规范、安全状态机 |
| [系统架构与技术选型](docs/系统架构与技术选型.md) | 腾讯云 + Go 模块化单体 + uni-app；成本与 App 演进 |
| [数据模型与 ER v1.2](docs/数据模型与ER设计.md) | 版本、确认、授权、证据与幂等；非 DDL |
| [OpenAPI 1.2.0](api/openapi.yaml) | 契约唯一真源；[使用说明](api/README.md) |
| [领域词汇](CONTEXT.md) | 服务关系、授权、草稿、发布确认、饮食证据 |
| [审核报告](docs/方案审核-2026-10-23.md) | R01–R10 的原始证据与最小修正建议 |
| [修复对照 v1.2](docs/方案修复对照-v1.2.md) | 十项发现修订时的状态、实测结果与未决项；后续规划以PLAN_REFINEMENT为准 |
| [成本与资质](docs/小程序上线成本与资质.md) | 资质口径、三档 AI 成本、供应商事实 |
| [市场事实清单](docs/市场事实清单.md) · [海外对标](docs/海外私教AI教练_事实清单.md) | 竞品、客单、监管与证据（历史材料） |

设计状态：旧原型、备份、截图、token与生成简报已删除，不作新基线。[第三轮文稿与进度](docs/design/round-03/README.md)逐项落实设计意图，目标为高级质感、美观与配套动画；由用户指定 DeepSeek 实现，Lead验收，内部设计推理不得进入产品。当前尚无已接受的新UI。[旧清理清单](docs/design/UI_CLEANUP_20261024.json)保留实际删除校验。

交互图（Archify 历史快照，字段以当前 ER/OpenAPI 为准）：[架构](docs/diagrams/nerae-architecture.html) · [ERD](docs/diagrams/nerae-erd.html) · [信息架构](docs/diagrams/nerae-ia.html)。

## 怎么开始

```sh
# 看当前可做的任务与候选
api/tests/.venv-py311/bin/python scripts/plan.py ready

# 取一个限定任务包（含必读、写入边界、验收命令）
api/tests/.venv-py311/bin/python scripts/plan.py packet F01-01

# 校验 DAG、依赖与文件所有权
api/tests/.venv-py311/bin/python scripts/plan.py check

# 契约回归与正式 lint
PYTHONDONTWRITEBYTECODE=1 api/tests/.venv-py311/bin/python api/tests/run_contract_tests.py --summary
npx --yes @redocly/cli@1.34.3 lint api/openapi.yaml
```

`make ...` 是 F01/F02 要建立的验收接口，目前不存在；不要把它当作已可用 CI。

## 已知边界

- 静态契约测试通过，不证明 HTTP、数据库、权限、并发、真实模型或微信真机行为
- 公开陌生获客保持关闭，需 L01 外部证据 + V02 双人实测（G3）
- 健康信息采集同意与关系授权分开；撤权/结束即时阻止未来访问
- 不做支付、不做医疗诊断、不给疾病/孕产妇饮食方案

## 历史规划

早期 59 项波次计划与相关规范已归档到 [docs/archive/planning-v0](docs/archive/planning-v0/README.md)，**不再作为实现依据**。
