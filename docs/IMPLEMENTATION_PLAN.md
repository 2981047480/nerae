# Nerae 开发计划

版本 1.1 · 2026-10-23。目标：让学员少操作地开始并坚持训练，让教练可持续交付；用清楚的模块、合同和证据支持多个原生 agent 分工，持续迭代可用且美观的产品。

## 1. 从哪里开始

现状是产品/架构文档、OpenAPI、静态测试。本轮补了方案断点、流程、视觉参考、37个路线图工作包（原36项+S01内容基线）、21张执行卡与分发检查工具；没有业务服务器、应用工程或Git仓库。因此第一轮交付是可复现工程与设计基线，不能直接把接口目录分给多个agent自由实现。

| 维度 | 当前资产 | 本计划补足 | 完成证据 |
|---|---|---|---|
| 产品 | 方向、两批范围、训练/饮食/教练/AI | 单一默认动作、日期语义、草稿恢复、双向回复、陌生获客门槛 | APP_FLOW + 场景 C01–C16 |
| 合同 | v1.2 动作快照、回读、安排、消息、AI状态与建议应用 | 业务实现与对象权限/版本/并发 | API生成、真实HTTP/DB负例 |
| 工程 | Python契约测试，尚无业务代码 | monorepo、模块化单体、依赖锁、CI、可恢复开发环境 | F01/F02子卡：干净构建、隔离双工作区、EG01–EG07；各批集成验证EG08 |
| 视觉 | 用户选择克制专业，本轮参考稿 | 真正uni-app组件、中文层级、所有状态与真机 | D01/U01/V01/V03截图与任务走查 |
| 维护 | 分散文档与历史审核 | 真源、模块owner、变更与迁移、观测/恢复/维护节奏 | O03/M01演练与更新记录 |
| 多agent | 本轮未实际并行开发 | 依赖DAG、文件隔离、任务包、独立review、串行集成 | F02-01陌生agent上手记录 + 后续两个模块并行试运行报告 |

本轮工程阅读顺序：本页看全局 → ENGINEERING_STANDARDS/DEFINITION_OF_DONE看规则 → tasks.json/packet看限定任务 → APP_FLOW/ER/API按相关章节 → VALIDATION_PLAN看证据。UI_SPEC留到后续设计阶段读取。历史市场分析按具体问题再读；旧架构/ER HTML不作为当前合同。

## 2. 里程碑与价值出口

| 里程碑 | 用户能得到什么 | 主要任务 | 放行要求 |
|---|---|---|---|
| M0 工程与体验基础 | 可运行的真实壳层与明确的主路径参考 | F01-01至F02-I工程卡；L01独立推进；D01按第三轮四张小卡推进独立UI样机，正式U01仍待基线 | 可复现构建，生成无漂移，组件状态与隔离环境成立；UI样机不冒充工程验收 |
| M1 P0-A内部交付 | 教练草稿可恢复发布，学员按日开始/记录，双方可回复 | A01–A08、S01-A、O01、I-A01/02/03、U02–U05、I01/V01 | 从空库跑通；权限/并发/弱网/重启/真机证据 |
| M2 陌生获客验证 | 原本不认识的双方能通过允许渠道达成并确认服务 | L01/V02，O03上线运维条件 | 对应主体/类目证据 + 双端真实试验；私域不替代 |
| M3 P0-B内容与免费使用 | 找模板、开始、发布、饮食打卡、确认调整、分享；申诉可处理 | B01–B05、S01-B、I-B01/02、U06/U07、O02/O03、I02/V03 | 全主路径与P0-A回归；运营、审核与内容来源就绪 |
| M4 试点结果 | 可判断是否减轻学员/教练负担、是否带来客户 | V04 | 成熟观察窗、来源分组、成本/人工时间与真实分母 |
| 后续 | 周报/邀请/媒体、App与持续维护 | P01/P02/M01 | 按试点证据再拆小任务；不影响首批价值优先级 |

P0-A不等于删掉饮食/计划广场，P0-B也不等于自动公开上线。L01可与工程并行，但公开陌生获客有硬门槛。评价若提前开放，O02的最小申诉SOP必须同步提前，不得等P1完整后台。

## 3. 目标工程结构与职责

以下路径为待建立结构。F01以实际工具链验证后锁定版本，不复制任意“最新版本”清单。

```text
api/                         OpenAPI唯一真源、生成配置、静态合同测试
apps/api/
  cmd/{api,worker}/           进程入口；无业务规则
  internal/server/           路由组合、错误和生命周期
  internal/middleware/       认证上下文、限流、请求验证、幂等接入
  internal/gen/              生成类型与接口，禁止手改
  internal/modules/          每个模块：handler/application/domain/repository
  internal/rules/            纯规则、版本、经过复核的输入/输出条件
  internal/platform/         微信/COS/MiMo外部适配
  migrations/<module>/       模块独立迁移；集成时明确全局排序
apps/client/src/
  app/ navigation/ stores/   壳层与公共状态；不存长期健康正文
  features/<task-area>/      视图、viewmodel、API适配、功能测试
  platform/                  登录、存储、导航、推送等端能力
  styles/                    token映射与基础排版
apps/admin/src/              独立管理身份、必要运营工作流
packages/contracts/         由OpenAPI生成TS；客户端不另造接口类型
packages/ui/                token、基础组件与状态示例，不放业务规则
design/                     视觉token、交互参考、评审记录
tests/{e2e,visual}/          跨模块闭环与界面证据
infra/ scripts/             隔离环境、CI、迁移、恢复与只读任务工具
content/{seed,manifests}/    来源/版本/审核清单；测试fixture不混入交付内容
docs/tasks/                 单一任务DAG；报告另存docs/evidence
```

| 领域owner | 所有事实 | 对外窄接口/事件 | 不该做什么 |
|---|---|---|---|
| auth/user A01 | 登录主体、年龄、本人档案/采集同意 | AuthContext、ProfileProjection | 不授予教练健康访问权 |
| relation A02 | 服务说明、双方确认、grant当前版本 | CheckAccess；RelationEnded/GrantRevoked | 不直接发布计划或生成消息正文 |
| catalog/plan/delivery A03 | 动作版本、内容版本、发布版 | ReadVisibleVersion/CreateRevision/Publish | 不按当前日期猜应练内容 |
| schedule/workout A04 | 当前安排、应练日期、实际会话 | Today/Start/Complete/Reschedule | 不重写历史内容或实际日期 |
| ai A05 | 调用/模型配置/usage/失败 | TypedCandidate + provenance；不直接业务写入 | 不自动换模型或直接发布/发送 |
| conversation/notify A06 | 线程、消息、读取位置、投递尝试 | SendText/Read；OutboxEvent | 不把推送成功当已读或授权 |
| coachwork A07 | 可重建待办和授权投影 | ScopedStudentView/TaskList | 不复制长期健康主数据 |
| appointment/coach A08 | 咨询、报价版本、自报、来源 | ContactStatus/TermsSnapshot | 不制造支付事实 |
| publish/plaza/engagement B01/B02/B05 | 公开版本、确认/审核、分享/统计 | PublishApproved/ContentHidden | 不绕过审核复用私人资料 |
| diet/anomaly B03 | 自评、观察、来源、规则版本与复核 | EvidenceAssessment | 不由计划量推实际摄入或诊断 |

用例级事务由应用服务拥有；跨模块通过接口或事务内outbox协作，不跨模块直接写表。需要跨表原子性时由上层应用用例明确参与者和事务边界，不用分布式消息代替必须同步生效的撤权。Redis只作可重建缓存/排队/预算预留；MySQL保留最终事实。

## 4. 从用户任务到接口、异常与运营

| 流程 | 合同锚点 | 开发任务 | 必须处理的异常/运营 | 验收 |
|---|---|---|---|---|
| F01开始训练 | exercises、training-starts、me/sessions | A01/A03/A04/U02 | 登录回跳、下架、双击、同步失败、内容来源 | C01/C05/C10 |
| F02今天/日历 | training-schedule、occurrences、today-training | A04/A07/U02 | 暂停、漏练、改期、版本/时区、无授权提醒 | C03/C04/C06 |
| F03继续交付 | coach/plans GET/PUT、publish、coach/ai/plan-draft | A03/A05/A07/U03 | 草稿丢失、412、缺项/阻断/503、专业规则 | C02/C08/C09 |
| F04交流 | relations/messages、messages/read、notifications | A02/A06/U04 | 无咨询、无计划、只读、撤权、敏感留存/客服审批 | C07/C08 |
| F05咨询到关系 | appointments、coach/relations、confirm、grants | A08/O01/U05/L01/V02 | 预填、拒通知、渠道不可用、不同条款、投诉 | C10/C14/C15 |
| F06调整 | plans/adjustments、plan-adjustments/apply | B04/U07 | base/hash冲突、过期、双确认、教练计划不可改 | C13 |
| F07发布/饮食 | publish/drafts、publish/plans、diet/checkins、anomalies | B01/B02/B03/B05/U06/U07/O02 | 估算确认、初审、下架、份量未知、过敏授权、申诉 | C11/C12/C15 |

所有功能另需C16运维恢复和基础错误状态。当前OpenAPI仍有历史宽泛响应，不能认为v1.2已完整消除所有技术债；模块实现前用实际用户场景检查对应读写模型，缺字段先交共享owner收敛后再写界面。

未作为P0阻塞的能力：训练照片、完整媒体发布、复杂邀请海报、自动周报、复杂排名和原生App。必要证书上传、站内消息、公开内容审核、最小申诉绝不是可省略项。订阅推送可以降级为站内可查，拒通知时核心任务必须成立。

## 5. 任务包与安全并行

37个路线图工作包与25张task/integration执行卡共享同一个 tasks.json。工作包不可派发，packet <工作包ID>只列细化状态和子卡。后续每次只拆接下来可开工的少量卡，不把未来全部细节一次写死。用户在清理旧UI之后明确恢复设计与UI，D01-01/02/03/I按逐项文稿、四种代表情境、全量UI和集成验收串行推进，指定DeepSeek实现、Lead验收；正式U01–U07仍待工程与已接受设计基线。当前证据与实际模型看docs/design/round-03/STATUS.md。

```sh
api/tests/.venv-py311/bin/python scripts/plan.py check
api/tests/.venv-py311/bin/python scripts/plan.py ready
api/tests/.venv-py311/bin/python scripts/plan.py packet F01-01
api/tests/.venv-py311/bin/python scripts/plan.py packet F02-01
api/tests/.venv-py311/bin/python scripts/plan.py catalog
```

首批拆分：

| 工作包 | 执行顺序与独立交付 |
|---|---|
| F01 | F01-01 Git/工具链 → F01-02 Go骨架 与 F01-03客户端/后台构建骨架 → F01-04隔离环境 → F01-05自动检查/CI → F01-I干净检出集成 |
| F02 | F02-01生成与陌生agent上手 → F02-02错误/请求边界 → F02-03真实库幂等/恢复 → F02-I transport接线与门禁 |
| L01 | L01-01缺口盘点 → L01-02主体/渠道 和 L01-03条款/留存 → L01-I证据/配置复核；外部缺材料保持未通过 |

当前ready仅F01-01与L01-01。先由Lead本地建立真实Git基线，再进行隔离worktree；工具链锁/共享依赖仍由单owner串行收口。工具只读，不启动agent、不改状态、不发消息、不合并。

小批集成提前到完整I01/I02之前：I-A01身份/关系 → I-A02计划/日程/训练 → I-A03消息/咨询/运营；I-B01发布/分享/申诉 → I-B02饮食/建议。它们只接线与验证已有模块，按DAG串行写共享server/cmd，发现新业务另开卡。A03/A06/A08在I-A01之后进入；V01/V03分别要求S01-A/S01-B真实可交付内容。

I01/I02目前仅部分细拆，完整客户端接缝要在后续设计完成后继续拆；不能因早期API集成通过而接受整个工作包。推荐后续并行试验仍为A06+A08，须先细拆成≤3人日执行卡，并经过用户实际并行授权。

[工程标准](ENGINEERING_STANDARDS.md)统一分层、编码、事务、命令和EG01–EG08；[完成定义](DEFINITION_OF_DONE.md)统一七项清单、证据revision、真实review模式、集成覆盖与上手记录。规则由plan.py验证结构与hash，Lead判断证据质量。F02-01使用陌生执行者验证上下文是否足够，记录真实请求和返工；本轮仅规划，尚未执行。

## 6. 估工与进度判断

执行卡 `effort_days=[低,高]` 是**一名熟悉领域的开发者完成实施与局部验证的人日估计**，不是agent运行时间、日历承诺或已到岗人数。外部审核、供应商准入、试点观察窗另计。架构/安全评审、跨端实际坑和内容准备可能增大区间。

完整细拆工作包的估工等于子卡有效人日之和；比旧粗估增加的部分明确包含集成/复核，不把拆卡当作节省工量。pending/partial包仍是待校准粗估。先完成F01/F02及一个垂直切片，再用实测返工/验证/集成耗时修正；每两周滚动一次。预测日期 = 在实际人力/并行槽限制下的依赖关键路径 + review/集成等待 + 外部门槛，不能把总人日简单除以agent数。每轮预留约20%容量给集成和未预见错误，这是排期假设不是效率保证。

看四种进度：可复现的用户闭环、通过的门槛、已接受任务、仍缺证据。接口数/文档页数/生成代码行数只作库存；未成熟留存窗口不染绿。暂停新增功能的触发：P1正确性/隐私问题、核心记录无法恢复、重复越界冲突、视觉动作负担>6而无整改。

## 7. 持续维护与质量预算

- PR按一个用户结果或技术边界提交；描述触发、前后行为、验证和限制。生成文件与手写逻辑分开审。每次改概念同步相关代码/合同/模板/当前文档。
- 迁移expand/contract，保留上一个应用兼容，生产禁止自动重建库。每个任务单独编号，集成owner排顺序并执行空库/旧库升级/恢复检查。
- 依赖和代码生成器锁版本；每月在独立分支审升级/安全影响，完整构建再合并。模型配置升级另走预算/条款/质量评测，不与普通依赖升级混在一起。
- 错误码集中映射成可理解中文，服务端保留requestId；内部版本/状态枚举只在帮助用户纠错时展示。新人先能用CURRENT_STATE+任务包启动，再考虑新增文档。
- UI维护共享token/组件，不让不同agent各自发明按钮、圆角、颜色和空状态；每版检查关键屏幕/长文/键盘/减少动态效果。
- 每版记录错误率、P95、同步失败、撤权传播、队列积压、费用和人工复核分钟；每季度恢复演练。真实重复问题才写RETRO，稳定规则再升AGENTS。

## 8. 本轮交付范围

本轮把方案缺口修到可实施的合同/规则，交付详细计划和可分发任务；静态与参考稿检查见修复对照。应用开发、真正多agent运行收益、真实健康专业审核、外部资格和渠道仍需对应任务证据。只有完成L01/V02才可关闭R06的外部链路缺口，不将“有任务”写成“已上线”。
