# Nerae 项目工作约定

## 先定位当前任务

先读 `docs/CURRENT_STATE.md`，再用 `docs/tasks/tasks.json` 的任务 ID 和 inputs 定界。若存在 `RETRO.md`，只读相关条目。用户最新指令优先于项目默认流程。项目当前处于契约修订与开发规划阶段，不把未来目录当作已存在应用。

真源：PRD 定义产品边界；APP_FLOW 定义用户任务/日期/恢复规则；OpenAPI 定义 wire contract；ER 定义持久化和事务不变量；ENGINEERING_STANDARDS 定义工程规则、DEFINITION_OF_DONE 定义接受证据；UI_SPEC 指向当前第五轮价值呈现，docs/design/round-05/ 是当前范围与验证，round-04/ 的两种记录语义继续生效，其余旧视觉仅为历史，具体视觉接受以实际证据为准，旧稿不作基线；DECISION_LOG 记录取舍；tasks.json 定义依赖与写入范围。冲突先报告并收敛，不自行任选一份。旧审核、旧 diagrams、市场资料是历史证据，不覆盖当前规格。

## 产品与实现边界

- 用户作目标、适用性、服务约定和重要确认；App 接管重复资料、恢复、保存、导航、通知与结果说明。AI 只作有证据的草稿/建议，不替代确认和专业安全审查。
- 保留 A 阶段不收款、不代收、不分账；免费训练/饮食广场、教练自主定价和 AI 分层。未满18岁不提供服务。
- 保留 uni-app Vue3/TS + Go/Gin 模块化单体；不引入微服务、外部模型 Router、常驻代理路由服务或自动升档模型。
- 健康采集同意与关系授权分开。撤权/结束即时阻止未来访问、AI与通知；客户端角色、内容标签和缓存均不授予权限。
- 改业务前读相关代码。领域规则在服务端统一实现，页面不直连数据库或模型；现有 API 的 operationId 不随意改。

## 委派与集成

仅在用户授权并行执行时派发 agent。用户已授权本目标使用 GPT-6 Luna 子代理；Lead 负责范围、最终产品/架构/安全/视觉判断、依赖、集成和验收，原生子代理只接有边界的任务包。

- `api/openapi.yaml`、生成配置、根依赖锁、共享路由/导航、设计 token 和全局迁移顺序为共享写入点，必须单 owner 串行。模块需要改共享接口时先交变更请求，再追加依赖明确的合同任务。
- 一任务一隔离 worktree，先确认真实 Git 根与基线；业务工程前无 Git 时由 Lead 本地先完成 F01-01，不能假称已隔离。本次独立 UI 样机由 Lead 先建立最小 Git 基线与忽略保护后隔离；这不代表 F01-01 的完整工具链已接受，不启动数据库或业务工程。不同 worktree 使用不同端口、测试数据库、对象前缀和 Redis 命名空间。
- 并行上限按实际工具能力；本会话最多 Lead + 3 个子代理，不能把任务 DAG 所有节点同时启动。
- 任务开始登记真实 owner/model/reasoning/base revision；不得声称使用不可用的模型。优先策略在 tasks.json，实际路由只用原生工具。
- agent 返回 status、提交/文件、命令退出码、证据、风险和需决策项；独立 review 后才 accepted。Lead 串行集成并在合并后的同一版本重测接缝。

工作包只用于路线图与最终接受；只有≤3有效人日的task/integration执行卡可派发。用户已授权 Lead 直接实现并继续第五轮设计，覆盖第三轮的指定 DeepSeek 执行者限制；也允许本目标使用 GPT-6 Luna 子代理，实际委派须遵守写域和工作树规则。当前 R05 范围见 docs/design/round-05/BRIEF.md；保留 R04 默认训练后记录、可切换每组完成后记录，两模式共享记录，禁止训练播放器及任何训练/休息倒计时。U01–U08 正式业务界面卡仍等待工程与设计基线。本轮设计推理不得进入产品。首批执行卡由ready计算，不将未细拆工作包直接分给agent。每小批有独立集成卡，七项完成清单、同一接受提交证据、真实review模式通过后才能accepted。详见docs/DEFINITION_OF_DONE.md。

## 验证与上下文预算

现在可运行（Python 3.11+，本工作区已有隔离解释器）：

```sh
PYTHONDONTWRITEBYTECODE=1 api/tests/.venv-py311/bin/python api/tests/run_contract_tests.py --summary
api/tests/.venv-py311/bin/python scripts/plan.py check
api/tests/.venv-py311/bin/python -m unittest discover -s scripts/tests -v
api/tests/.venv-py311/bin/python scripts/plan.py packet F01-01
```

`make ...` 是 F01/F02 要建立的验收接口，目前不宣称存在。静态测试不能代替 HTTP/DB、真实供应商、微信真机和专业审核。新增行为须有有效正反例；普通文案不要求无意义镜像测试。Visual Gate 必须实际看截图/屏幕；QA Gate 必须覆盖本任务的正常、空、失败、边界、重复与回归状态。

默认必读不超过任务 inputs + 受影响代码/测试；可选文档按问题取用；排除全部历史市场材料、旧图和他任务日志。Lead 交接摘要控制在约 500–800 中文字；单任务报告约 400–700 字，证据用路径不贴整段日志。不要为此维护第二套产品规格。

出错如实记录。第一次区分输入/环境/范围/实现；第二次同类错误重评路径与模型；第三次重复阻塞停止该路径并报告 blocked，继续独立可做工作。只有在证据通过时更新任务状态，不能为“好看”补成功或假数据。

本轮方案修复与开发规划不等于应用发布，发布条件见 `docs/RELEASE_GATES.md`。复杂任务记录角色、范围、非目标、验收和风险；文件改动前简述预期影响。新增已确认决定写 DECISION_LOG；真正重复/难得经验才更新 RETRO，不复制聊天记录。
