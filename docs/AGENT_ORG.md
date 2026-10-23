# 原生多 agent 协作与接受协议

目标是复现有效的并行开发方式：独立上下文、依赖控制、隔离改动、产物review、统一集成。Cursor官方说明的异步子任务和独立Git worktree提供了对标依据；本项目采用本环境原生工具，不承诺复制Cursor所有界面/云端功能。[多agent说明](https://prod.cursor.com/help/ai-features/multi-agent) · [worktrees说明](https://prod.cursor.com/docs/configuration/worktrees)

## 角色与原生模型

| 角色 | 责任 | 不能独立决定 |
|---|---|---|
| Lead | 目标、架构、范围、文件所有权、共享合同、集成、风险与接受 | 不替用户批准预算/外部发布/服务约定 |
| explorer / context steward | 限定文件核实、事实/不确定项和路径 | 不以摘要代替关键代码证据 |
| worker / ui-builder | 实现一个任务、局部验证、清晰提交 | 不越写入范围、不改公共规范/架构、不自报accepted |
| reviewer | 检查正确性、安全、维护性、合同与回归 | 不只看作者“都通过”陈述 |
| qa-verifier | 运行确定命令、实际截图、报告通过/失败/缺证据 | 不以低风险模型最终判断健康安全或视觉质量 |
| product-designer | 任务流程、认知负担、视觉和文案 | 不把个人风格当用户批准，不引入无减负价值的功能 |

工作包与执行卡都使用同一任务JSON；只有≤3有效人日的task/integration可派发。UI工作包继续暂缓，当前只推导产品与体验逻辑，本轮不生成 UI。任务 JSON 中记录建议模型策略：高判断用 `gpt-5.6-sol` high；普通实施偏好 `gpt-5.6-terra` medium；确定性验证可 `gpt-5.6-luna` medium。实际派发必须检查当时原生工具的可用型号，本轮子agent列表未提供Terra，不能虚报使用它；不可用时保留当前默认或由Lead选择可用Sol，并写实际理由。最终接受由Lead承担。没有运行子agent的任务不得填一个看似真实的模型运行记录。

## 派发前必须成立

1. 用户已授权本次并行执行。生成任务包不等于授权自动创建新聊天、发送消息或启动云任务。
2. `plan.py check`通过；依赖accepted且证据已review，任务在ready列表；外部条件与所需权限单独确认。
3. F01-01由Lead本地建立真实Git基线；其后任务开始前Git基线存在，工作区干净或已有改动归属清楚。优先复用适合的已附worktree；需要新隔离时用原生worktree工具，不误在主目录同时写。
4. 记录任务ID、owner、实际model/reasoning、基线/分支、工作区、端口、DB/Redis/对象前缀。环境只复制必要测试配置，不复制生产密钥或客户数据。
5. 发送 `packet <ID>` 输出，补充基线和上下文差异；必读通常4–6文件，相关代码按任务展开，历史市场/无关任务排除。目标报告约400–700中文字，长日志存文件。

## 运行与接缝

最多使用实际可用并发槽，本会话4槽包含Lead。典型为Lead+2worker+1reviewer；无独立reviewer槽时先让worker结束再review，不能丢接受检查。没有独立子代理授权/能力时按角色顺序执行并标明“同一代理顺序复核”，不冒充独立意见。

共享文件冲突不是靠提示词就能消失：OpenAPI、生成配置、锁文件、路由组合、导航、token、全局迁移顺序需共享owner。若worker发现必须修改它们，返回变更请求（原因、字段/路径、消费者、兼容性、反例）；Lead增合同任务并阻塞依赖，完成后再基于新基线继续。不得让两个分支各维护自己的临时合同。

worker提交只包含本任务allowlist；保留无关用户改动。reviewer核对实际diff和证据，发现越界/删除/假fixture完成即退回。Lead串行集成：核对基线 → 更新分支/处理冲突 → 跑相关合同+模块测试 → 合并 → 在合并版本跑跨模块接缝 → 记录accepted_revision与证据hash。worktree归档前确认制品已收录；不自动推生产。

## 任务卡与报告

任务卡就是 `tasks.json` 中一个对象加生成packet，包含用户结果、当前阶段、角色/模型策略、上下文、写域、非目标、依赖、验收、风险、失败协议。开始后补真实owner/model/base_revision；不要再复制一份手写卡而漂移。

报告保存执行卡白名单内的 `docs/evidence/<工作包>/<卡ID>/report.md`：status、实际模型与选择理由、基线/提交、具体结果、文件、用户/App责任是否保持、用到/排除上下文、命令退出码与截图、风险/取舍/未通过项、需Lead决定。status=success只表示作者完成；accepted须review与集成证据。

每小批先运行独立集成卡，完成定义与工程规则分别见[DEFINITION_OF_DONE](DEFINITION_OF_DONE.md)和[ENGINEERING_STANDARDS](ENGINEERING_STANDARDS.md)。pass项引用注册证据；不适用只用于允许项且须理由。证据revision须与accepted_revision一致；integration_result列集成提交与全部直接依赖。同一代理复核必须使用sequential_role_review并说明理由，不能伪装independent。

状态由Lead审查后显式修改任务JSON：planned→active→review→accepted；失败/等待可blocked。accepted需reviewed_by、review_mode、completion、accepted_revision与每项evidence的path/sha256/revision；完整细拆工作包还需全部子卡与整体标准通过。工具验证结构和hash，**不会自动判定证据内容充分**。任何未完成外部验证须保持partial或blocked，不用成功的单测覆盖。

失败协议：第一次保留错误并区分输入/环境/范围/能力；第二次同类错误收窄或升级适合模型；第三次无新路径则停止该路径并明确blocked。独立工作可继续。重新开始时记录新证据，不通过无限重试消耗预算。

## 首次上手与后续并行效果验证

F02-01由未参与本方案的执行者在限定上下文中完成；记录真实extra_context_requests/rework_cycles/boundary_violations/verification_reproducible与证据，缺项不可accepted。先解决边界/复跑缺陷，再根据实测修正文档；不预填一次通过或固定返工率。当前仅规划此试验。


F01/F02/A01/A02/I-A01完成并将候选工作包细拆后选择互不写同目录的A06（消息）和A08（咨询/教练）。使用相同已接受基线，各自完整测试后独立review，再由I01串行接入。

记录：总经过时间、实际有效开发时间、共享合同等待、合并冲突数/越界文件数、review退回次数、集成缺陷数、运行成本。接受要求为：0个未处理的越界/隐私问题，主路径接缝全部通过，证据可复跑，产物来源可追溯；速度只作测量，不预设“多agent必然更快”。本轮尚未执行这个试验。
