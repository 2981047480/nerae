# D01-03 全量扩展 · 进度日志

- 开始：2026-10-24 13:04 +08:00
- 会话：DeepSeek Harness `session-15de3174-0b22-47b7-9574-29dd3b032a8c`（新工作区新会话）
- 执行：opencode-go / `opencode-go/deepseek-v4.1-flash`；reasoning Medium（Lead 实施调整，非人类指定等级）
- 工作树：`/private/tmp/nerae-ui-complete`；分支 `ui/full-round-three`；Git 基线 `b3949bfbd0ec5e5b6d88594c3c3948b47681b8ed`
- 端口：`127.0.0.1:4187`（旧 4186 为首批已接受预览，不复用、不停止）
- 写域：`design/prototype/**`、`docs/evidence/D01/round-03/worker/**`
- 工具链：`/opt/homebrew/bin/node` v26.0.0；`/opt/homebrew/bin/pnpm`；依赖按既有 `pnpm-lock.yaml`

## 收束状态（2026-10-24 15:35 +08:00）

- 候选源码已由 Lead 原样冻结在提交 `6dba9b6085c9a7f0b2ac84cf0fa8f290d4a7871d`（tree `c8f84ef126e0c140e100600738eb703936fbd349`，分支 `ui/full-round-three`）；本提交只是 candidate，尚未 accepted。初始基线 `b3949bfbd0ec5e5b6d88594c3c3948b47681b8ed` 是历史输入，不回退。
- 上一轮在 336 步后因 TRANSPORT Connection error 中断；内部 5/5 重试失败，不是正常完成。本文件此后只做收束记录，不再改 `design/prototype/**`。
- 最新同版执行者检查：`vue-tsc --noEmit` exit 0；`vue-tsc && vite build` exit 0；`vitest run` 75/75 passed、exit 0。日志见 `logs/d01-03-{typecheck,build,test}-candidate.log`。
- 实际浏览器证据：320/390/430/768/1440 截图 58 张；390×900 与 320×900 共 18 屏 DOM 命中区审计均为 0 个小目标；VR08 动作条在 390 的 top 1446、首条正文 749–805，不遮挡。证据见 `screenshots/`、`logs/bounds-audit-{390,320}-final.json`。
- 以上为执行者检查/本地浏览器证据，不等于 Lead 冻结版独立验收；服务端、真机、真实登录/健康鉴权/AI/消息/支付仍未测。

## fix01（2026-10-24 17:24 +08:00）

- 基线：Lead 冻结 `6dba9b6085c9a7f0b2ac84cf0fa8f290d4a7871d`；本轮只在 `design/prototype/**` 与 `docs/.../worker/**` 修复，未改 Git HEAD、未 reset 到 b394、未停 4187。
- EA17：`replyToStudent` 按 `studentId` 复用/创建会话；新增 `conv-zhou-lan`/`rel-zhou-lan`；旧合法状态缺关联时按 active 学员恢复关系与会话，回复可直接打开周岚；未知旧约定不借别人的包/价格，标 needsReview/priceKnown=false。
- EA18：健康授权只从 relation 派生，删除 students 副本；教练列表/详情/头部/最近事实/未完成工作/待办/证据/消息正文/摘要/训练引用统一裁剪；撤权不再清空共享消息正文，学生本人历史保留；private 观察即使授权也不给教练；结束关系同样约束。
- EA19：会话删除共享 `unread`，改为双方 `readBy` 游标；未读按 stable message id/对方已成功消息计算；只推进当前方；sending/failed 不投给接收方；接收方列表/线程/摘要/引用/空态不露失败正文，重试/删除只限作者。失败→同 ID 成功只计一次。
- EA20：`conversation.draft` 改为 `drafts.student/coach`；编辑/切换/刷新/发送清空只影响当前参与者；旧 raw.draft 无可信 owner 时进 `unassignedLegacyDraft`，不进入任一方 composer/list/摘要；历史 `draftOwnerInferred` 也退回未归属，原文保留。
- VR09：MessagesView 用 `formatTime/formatDateTime`，UTC 07:38 显示本地 15:38；未改存储时间戳。
- 最终执行者检查：typecheck exit 0；build exit 0；vitest **83/83** exit 0。实际浏览器 390×900 复核：周岚回复、layout、失败消息接收面、双方草稿、旧状态恢复、时间与撤权读取面；截图/日志见 `fix01-390-*.png`、`logs/fix01-*.json`。
- 未测：真实服务端鉴权/消息；五宽度/200% 全量/全部动效；旧草稿无法自动判定 owner 的可视恢复（仅保留原文，不显示）。

## fix02（2026-10-24 20:23 +08:00，恢复完成）

- 第 4 轮约 579 步 TRANSPORT / Connection error、5/5 重试失败；Lead 原样冻结恢复点 `aaf1fe0a8e9506b72c56157139eeda100d71a709`。本轮在恢复点继续，未 reset 6a、未改 Git HEAD。
- EA21：`incomingView` 按 `incomingHistoryStudentId` 读取该学员 `coachSnapshots/active` 的不可变 delivery；旧 v7 不被 v8/周岚上下文覆盖；缺失引用进入“引用不可用”，不 fallback 最新。教练点周岚计划进入只读“教练查看交付版本”，diff 对周岚现用 v3，不混林澈现用版；发布周岚也生成可点的计划引用。训练引用写入 `recordsFocusId` 并展开原记录；缺失不展开 10/22；服务包按 ID 找所属教练/包。
- EA22：同包 v3 759 显示“尚未确认”，v2 699/4周旧约定单独保留；不同包同源。
- EA23：empty/single/insufficient 用同一 `recordViewRows` 投影；insufficient 图表/摘要/明细/点评一致为空，单条 delta null。
- EA24：preview coach empty 使 filteredCoaches/结果计数为 0，保留筛选；loading/error 计数不假装 3。
- EA25：B 训练日 40 分钟、4 动作、高位下拉器/哑铃按动作；Today 动作数/时长/器械来自 session；非 4 动作内容用 facts 数量。
- EA26：AI 候选只说明改草稿与保存/安排，公开审阅是另一选择。
- EA27：add/remove/undo/update 全部同步计划草稿状态与 todo-draft；发布/保存/撤销回原内容按真实 dirty 完成，失败不完成；回复/授权不自动完成。
- VR09/10/11/12：保存时间统一本地格式；撤权确认主语改为“教练不能替你恢复”；320/200 相关安排不再横向裁切、S03 名称/组次叠排且图片区不叠字/不横向裁切；缺图提示左上不压日期，替代文本保留。
- 最终执行者检查：typecheck exit 0；build exit 0；vitest **91/91** exit 0。实际 390 浏览器复核 EA21/22/23/24/25/27、VR10、320/200、缺图；截图 `fix02-390-*.png`、`fix02-320-*.png`，日志 `fix02-browser*.json`。
- 未测：Lead 冻结版全 16 族/五宽/200 全屏/动效、真实服务端权限/消息、真机；候选未 accepted。

## fix05（2026-10-25 14:46 +08:00）

- 基线 Lead 冻结 `ae141ae639fde8234b7782bf79775f78cc5e04c8`；未改 Git HEAD，4186/4188/4189 未触碰。
- M02：`.plan-focus-ghost` 从 App.vue scoped 移到全局 `base.css`，动态 body 节点实际命中 `position:fixed/z150/pointer-events:none/cover/center`；390 Full Motion 实测首次 ghost 169ms、280ms、视口内、目标 350×320；快速返回/再进入无遗留，Reduce Motion 不创建。
- M12：`retryPersist` 仅在实际 `persistNow()` 成功后清除 `sync-failed/sync-pending` 预览标志；真实存储失败保留失败块、输入和 3 组。实际按钮正常/失败恢复/Reduce Motion 均验证，重复重试不增组。
- 最终执行者检查：typecheck exit 0；build exit 0；vitest **102/102** exit 0。证据 `fix05-*.png/json`。
- 保留 fix04 引用/迁移与 M04/M05；未测 Lead 全矩阵/真实后端/真机；候选未 accepted。

## fix05（2026-10-25 14:54 +08:00）

- 基线 Lead 冻结 `ae141ae639fde8234b7782bf79775f78cc5e04c8`；未改 Git HEAD，4186/4188/4189 未触碰。
- M02：`.plan-focus-ghost` 从 App.vue scoped 移到全局 `base.css`，动态 body 节点实际命中 `position:fixed/z150/pointer-events:none/cover/center`；390 Full Motion 实测首次 ghost 169ms、280ms、视口内、目标 350×320；快速返回/再进入无遗留，Reduce Motion 不创建。
- M12：`retryPersist` 仅在实际 `persistNow()` 成功后清除 `sync-failed/sync-pending` 预览标志；真实存储失败保留失败块、输入和 3 组；正常/失败恢复/Reduce Motion 按钮实测，重复重试不增组。
- V04：删除 `.package-tab-state` 的 `opacity:.8`，未选中“学员可见”opacity=1、12px、rgb(94,107,112)/canvas rgb(243,242,238) 实测 **4.9165:1**；选中态/隐藏态/三服务包/200字号保留。
- 最终执行者检查：typecheck exit 0；build exit 0；vitest **102/102** exit 0。证据 `fix05-*.png/json`。
- 保留 fix04 引用/迁移与 M04/M05；未测 Lead 全矩阵/真实后端/真机；候选未 accepted。



- 基线 Lead 冻结 `55994603461ee5710a5be4f70b1180d445dba5c5`；未改 Git HEAD，未触碰 4186/4188/4189。
- training 引用改走引用导航（保留 stack/来源），冷恢复 9/29 记录后 goBack 回 Gu messages；直接主导航记录仍 root。未知记录显式不可用、不展开默认。
- 旧缓存迁移按原始字段存在性/合法 actor：旧 coach messages 缺 selection.actor 时保留 parsed.actor coach；旧 incoming 缺 owner/version/readOnly 时保留可信读者并标引用不可用，不落 Lin latest；旧 records 引用缺 focus 时 explicit=true/id=null，正常直接入口仍默认最近记录。
- `incomingView` 对 refMissing 直接 null；未知学员 header 不再回落林澈。
- 最终执行者检查：typecheck exit 0；build exit 0；vitest **101/101** exit 0。实际 390 浏览器：9/29 引用冷恢复/返回、旧 coach messages、旧 incoming 缺失、旧 records 引用与直接入口；证据 `fix04-*.png/json`。
- 保留 fix03 的 EA28/29、VR13/14、M02/M04/M05；未测 Lead 全矩阵/真实服务端/真机；候选未 accepted。

## fix03（2026-10-24 22:13 +08:00）

- 基线 Lead 冻结 `a289b090b5b172cf027b37dd0d5535659dd68364`；未改 Git HEAD、未回滚 aaf/6a。4187 保留；4188/4186 未触碰。
- EA28/29：`SelectionState` 持久化 actor 与 incoming 引用（studentId/version/readOnly/refMissing）及 recordsFocus；共享 messages/incomingPlan 导航与 goBack 保留当前读者角色；冷加载从 selection 恢复，不再默认林澈 latest。教练周岚旧交付只读、返回仍为教练周岚消息；缺失引用刷新仍不可用；训练引用刷新仍展开原记录；学员旧7刷新/返回仍学员。教练只读入口 `confirmAdoption`/openAdoptSheet 拒绝采用。
- VR13：新增 `coach-messages` 预览情境；教练角色第二“消息”入口保持教练列表/会话。
- VR14：320/200 `.change-summary-head` 叠排，`change-sub` 换行；不缩字号不截字。
- M05：`resumeRest` 先同步 `clock.now` 再设置 deadline/启动 ticker，继续首帧不再用暂停时陈旧 now；正反例覆盖暂停 5.5s 后继续与 1.3s 递减。
- M04：完成组进入休息后，`.rest-recorded--just` 在可见休息反馈上执行 160ms `set-confirm`，与真实记录同帧，不延迟。
- M02：plan bridge 等待 out-in 后 `[data-plan-hero]` 挂载（rAF 轮询 600ms），实测首次 ghost 162ms、动画 280ms；快速返回/Reduce Motion 无遗留。
- 最终执行者检查：typecheck exit 0；build exit 0；vitest **97/97** exit 0。实际 390/320 浏览器：open→reload→back（学员/教练/缺失/训练）、教练消息入口、审阅标题 214×37.5、M02/M04/M05；证据 `fix03-*.png/json`。
- 未测：Lead 全矩阵/全部动效、真实服务端/真机；候选未 accepted。

## fix02 恢复检查点（2026-10-24 19:40 +08:00）

- 第 4 轮在约 579 步以 TRANSPORT / Connection error、内部 5/5 重试失败结束，**不是正常交回**。Lead 已把当时落盘源码原样冻结为 `aaf1fe0a8e9506b72c56157139eeda100d71a709`；不回滚 6a5609，不改 Git HEAD。
- 当前源码已有 EA22/23/24/25/26/27、VR09/10/11/12 的大部分修复；恢复点独立 typecheck/原 83 测试 exit0（Lead 证据）。本轮先完成 EA21 剩余：引用所属学员/读取方、跨学员快照、缺失不 fallback、教练只读、训练引用展开原记录。
- 待办：EA21 正反例 + 原回归；一次最终 typecheck/build/test、针对性 390 浏览器检查；写 D01-03-fix02-report.md/json 与三份 SHA 清单。未接受，全矩阵仍由 Lead 做。

## 本轮目标

沿已接受的 S01/S02/S04/S15 补齐全部状态，并实现 S03、S05–S14、S16；形成 16 族、55 项、F01–F07、M01–M12 的可操作本地预览。保留 fix01/fix02 历史报告，新报告另写 `D01-03-report.md/json`。

## 已读输入（实际读取）

- `AGENTS.md`、`RETRO.md`、`docs/CURRENT_STATE.md`、`docs/UI_SPEC.md`
- `docs/design/round-03/05_UI_BUILD_SPEC.md`、`06_EXECUTION_PACKET.md`、`07_ACCEPTANCE.md`、`03_VISUAL_AND_MOTION.md`、`02_SCENARIOS_AND_INTERACTIONS.md`、`01_INTENT_TO_EXPERIENCE.md`（G/N/L 部分）
- `docs/design/round-03/coverage.json`（55 项 ID/分组/关联 S 族）、`STATUS.md`
- 首批源码：`src/App.vue`、`types.ts`、`data/sample.ts`、`state/store.ts`、`TodayView`、`ExploreView`、`TrainingView`（模板与交互）、`PreviewControl`、`AppHeader`、`TabBar`、`BaseSheet`、`PhotoFrame`、tokens/base CSS、三份测试、`README.md`、`ASSETS.md`
- 首批历史：`worker/progress.md`、`report.md`、`fix02-report.json`（只作历史，不作本任务报告）

## 已决定结构（本会话）

- 路由扩展为一个稳定的 Screen 联合：学员 `today/explore/planDetail/training/records/messages/incomingPlan/myPlan/aiAssist/diet/coachDetail`；教练 `coachTodo/coachStudents/coachStudentDetail/coachServices/coachPlan`；消息共用 `messages`，按 actor 读取各自会话。
- 持久化 schema 升为 2，新增 `history` 导航栈、`todayMode`、`preview`（每屏状态选择）、`student`（现用计划/收到的交付）、`coaches`、`relations`、`conversations`、`todoItems`、`ownPlan`、`ai`、`diet`、`services`、`records`。保留原 `session/inputs/favorites/explore/coach` 字段与既有动作，避免首批通过项倒退。
- 交付/采用分开：`coach.deliveries`（不可变交付快照）为唯一交付事实源，`student.incoming` 从同一快照生成；`student.plan` 现用 v6 不自动改，采用才改为 v7，历史 session 不改；`ownPlan` 是学员自己的内容，与教练计划分离。
- 消息以稳定 message id 发送/失败重试；撤权时删除健康原文并置占位，不能从 DOM/title/摘要回读。服务确认与健康授权分开；价格修改不改 `relation.agreedPrice`。
- 预览控制分“角色 / 情境 / 当前屏状态 / 减少动态 / 文字大小 / 重置”，设计解释不进入产品。
- 共享样式新增 `styles/screens.css`（页面、工具条、分段、字段、状态、空态、列表、消息、图表等通用类），各屏保留自己的特有构图，避免一屏一套皮肤。
- 验证：逐阶段 `pnpm typecheck`；最后 `pnpm build`、`pnpm test`；服务 4187 仅本机。

## 错误与限制（当前）

- 会话开始时 Git 无改动属正常，旧报告不代表本任务；已建立本文件；用户复核时本文件刚创建，存在时序差，现已按纠偏补齐。
- 首次 `pnpm typecheck` 因依赖未安装触发自动安装，且 pnpm 11 的 ignored-builds 报错；已用 `pnpm install --ignore-scripts` 按既有锁安装，后续直接调用本地 `vue-tsc`/`vite`/`vitest`。
- 修过类型错误：未使用导入、`Screen` 预览状态缺键、会话冲突字段 `id`、导航选择恢复、可选参数点击处理器；当前 `vue-tsc` exit 0。
- 已完成本地浏览器截图（320/390/430/768/1440）与 390/320 DOM 命中区审计；真机、真实服务、真实 AI 未做，最终报告逐项明示。
- 旧 4186 服务不停止、不复用；本轮使用 4187。

## 阶段与状态

| 阶段 | 内容 | 状态 |
|---|---|---|
| 0 | 读当前 AGENTS/RETRO/CURRENT_STATE/05/06/07/03/02；核对基线、依赖、旧服务 | 完成 |
| A | 内容与版本：S03/S09/S10 与共享状态、导航、预览控制基础 | 源码落盘；待回归 |
| B | 双方服务：S06/S07/S13/S14/S16 | 源码落盘；待回归 |
| C | 交流：S08 与引用、草稿、重试、撤权 | 源码落盘；待回归 |
| D | 积累与建议：S05/S12/S11 | 源码落盘；待回归 |
| E | 既有四族状态补齐、测试、构建、证据与交回 | 进行中 |

## 已落盘（实际文件）

- `src/types.ts`（schema 2、Screen 16 族、选择状态、消息/记录/服务/AI/饮食类型）
- `src/data/extended.ts`（历史记录、教练与服务包、关系、消息、待办、学员、自己的计划、AI 模板、饮食自评、服务草稿、预览状态）
- `src/state/store.ts`（导航栈与持久化 selection、训练/记录、计划详情开始与替换归档、收到计划采用、自己的计划审阅、消息发送/重试/撤权、教练待办/学员/服务、AI、饮食、预览状态）
- 视图：`PlanDetailView` S03、`IncomingPlanView` S09、`MyPlanView` S10、`AiAssistView` S11、`RecordsView` S05、`MessagesView` S08、`CoachDetailView` S07、`CoachTodoView` S13、`CoachStudentsView`/`CoachStudentDetailView` S14、`CoachServicesView` S16、`DietView` S12；`TodayView` 八态、`ExploreView` 教练模式与加载/失败/空/下架、`AppHeader`/`TabBar` 4+4 稳定导航、`PreviewControl` 角色/情境/当前屏状态。
- 共享样式 `src/styles/screens.css`；`App.vue` 注册 16 族与全局面条。

## Lead 反馈处理（EA/VR）

- EA03 刷新丢当前对象：新增持久化 `state.selection`（计划/训练日/教练/服务包/学员/会话/记录范围与动作/自己的训练日/AI 模板/教练训练日与模式），`persistNow` 同步，冷重载恢复；打开弹层、确认 hash、焦点不持久化。已补 store 冷重载正例。
- EA04 替换丢失确认记录：替换前把进行中/已完成会话快照归档进 `records`，进行中会话标记未完成与已记录组，不伪称完成；取消替换不改状态。已补正反例。
- EA05 消息标题写死：改为按当前会话 `coachName`/学员渲染；进入江遥、何岸会话时标题、服务入口、草稿与发送对象同源。
- EA06 产品正文混入实现/验收说法：逐段替换教练待办、教练详情、学员详情、学员列表、探索空态、饮食、记录、AI 的“假指标/不调用模型/样例集合”等表述，保留来源、版本、限制和健康范围。
- EA07 所选训练日：`startPlanNow` 使用当前所选 `dayId`，会话/开始摘要/替换摘要同源；刷新保留所选训练日；同计划同训练日重复开始返回同一会话。
- EA08 再次开始：会话增加 `dayId` 与 `sessionSequence`，每次明确替换后的新开始有唯一 sessionId；归档按唯一会话 ID，前后已确认组都保留；重复点击当前同一次开始仍幂等。
- EA09 教练计划串人：新增每学员 `coachSnapshots` 与 `activeCoachStudentId`；待办/详情进入周岚读写周岚计划，切回林澈恢复其 v6 现用/v7 已交付；发布/证据/收件人按当前学员；列表/详情区分现用与已交付版本。
- EA10 未授权健康内容：周岚训练证据标记不可见，列表/详情头部/最近事实/证据/展开/辅助文字统一裁剪；计划与服务非健康信息保留；关系结束沿用只读与不可见。
- EA11 公开审阅：分享预览展示每动作组数/目标/组间休息/非空公开备注，私人健康备注继续排除；旧确认失效与待审核逻辑不变。
- EA12 登录/年龄边界：新增本地 `auth` 与开始准入；匿名开始先登录再确认年龄，取消不建训练；未成年停止服务但可浏览；未知年龄不能换内容绕过；已登录成年林澈路径不增加确认；不接真实登录或短信。
- EA13 服务字段同源：保存同步 summary/scope→includes/goals/response/price/unit/cycle 到实际教练详情与咨询；既有约定使用独立快照字段（名称/价格/单位/周期），不随新报价；只对实际变化包增版。
- EA14 价格校验：价格输入保留原始字符串，0–9999 整数校验，负数/空/非法就近报错并禁止保存；0 元可明确使用，不再静默变免费。
- EA15 完成后的更换：只要存在未来安排/已完成会话，更换内容先打开替换确认；取消不改计划版本、未来 schedule、会话与历史；确认后才归档旧事实并建立新未来。
- EA16 旧状态兼容：`loadState` 按包补齐旧 services 的 `priceInput/priceUnit/scope/goals/response` 等字段，价格输入由有效 `priceYuan` 转换；不删本地存储/不换 key/不重置；补冷启动回归。
- VR01 触达：记录范围、查看服务、预览入口、状态 chip、开关、快捷操作等实际命中区补到 ≥44；用真实 390 DOM bounds 审计记录。
- VR02 对象入口：今天「现用安排」进入现用教练计划详情，新增独立「我的内容」入口进入自有编辑器；两者版本/标题/落地同源。
- VR03 消息主位：移除页面规则说明、底部渠道自述与重复段；服务/授权收进可展开轻量上下文，对话为主；sticky 输入保留。
- VR04 记录口径：动作按业务名称归一，消除重复「平板支撑/死虫式」；图表横轴按真实日期间距，纵轴有 kg/秒刻度与单位；明细未练动作改为「本次未记录这个动作」。
- VR05 咨询包：消息上下文区分「当前服务」与「已有约定」，价格/周期/确认状态同源；查看服务带回同一 packageId；旧月度约定 699/月单独保留。
- VR06 文案：移除「价格与服务内容相邻」等布局理由、重复样例/认证免责与排除项自述；保留资质未核验、评价不足、范围与报价影响。
- VR07 服务详情：预览显示计价单位+周期，实际详情同步新字段；保存时间用既有本地格式；switch 稳定名称「学员可见」与 checked 同义；移除布局解释。

- 内容拆天：`daysFromContents` 不再按动作数量推断；所有内容保留一个完整训练单元，另为「一周两练 · 力量基础」明确书写两个训练日。
- 交付字段：删除 `sentDelivery` 重复字段，统一以 `coach.deliveries` 为交付事实源，`student.incoming` 从同一不可变快照生成；发布同版本幂等。

## 下一步（交接）

1. 文档恢复轮已完成 `D01-03-report.md/json`；源码保持在 6dba 冻结，不再改动。
2. 4187 服务继续保留，不重启、不停止；Lead 独立跑固定版本代码/身份/流程/视觉与动效检查。
3. 不把候选、中间日志、模块替身或执行者自检当作 Lead accepted；完整目标仍 active。

## 错误与处理（收束）

- 上一轮 336 步后 TRANSPORT Connection error，内部 5/5 重试失败；因此报告由本轮恢复补写，不声称正常完成。
- 本轮无源码改动；只写 worker 证据文档。历史 fix01/fix02 与所有失败/候选记录保留。
- 最新日志早于 6dba 冻结数分钟，但冻结提交由 Lead 从同一工作树原样取得；执行者未在冻结提交后重跑测试。
