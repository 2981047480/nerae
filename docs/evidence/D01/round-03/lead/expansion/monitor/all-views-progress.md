# D01-03 全量扩展 · 进度日志

- 开始：2026-10-24 13:04 +08:00
- 会话：DeepSeek Harness `session-15de3174-0b22-47b7-9574-29dd3b032a8c`（新工作区新会话）
- 执行：opencode-go / `opencode-go/deepseek-v4.1-flash`；reasoning Medium（Lead 实施调整，非人类指定等级）
- 工作树：`/private/tmp/nerae-ui-complete`；分支 `ui/full-round-three`；Git 基线 `b3949bfbd0ec5e5b6d88594c3c3948b47681b8ed`
- 端口：`127.0.0.1:4187`（旧 4186 为首批已接受预览，不复用、不停止）
- 写域：`design/prototype/**`、`docs/evidence/D01/round-03/worker/**`
- 工具链：`/opt/homebrew/bin/node` v26.0.0；`/opt/homebrew/bin/pnpm`；依赖按既有 `pnpm-lock.yaml`

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
- 交付/采用分开：`coach.sentDelivery`（不可变 v7 快照）与 `student.incoming` 同源；`student.plan` 现用 v6 不自动改，采用才改为 v7，历史 session 不改；`ownPlan` 是学员自己的内容，与教练计划分离。
- 消息以稳定 message id 发送/失败重试；撤权时删除健康原文并置占位，不能从 DOM/title/摘要回读。服务确认与健康授权分开；价格修改不改 `relation.agreedPrice`。
- 预览控制分“角色 / 情境 / 当前屏状态 / 减少动态 / 文字大小 / 重置”，设计解释不进入产品。
- 共享样式新增 `styles/screens.css`（页面、工具条、分段、字段、状态、空态、列表、消息、图表等通用类），各屏保留自己的特有构图，避免一屏一套皮肤。
- 验证：逐阶段 `pnpm typecheck`；最后 `pnpm build`、`pnpm test`；服务 4187 仅本机。

## 错误与限制（当前）

- 会话开始时 Git 无改动属正常，旧报告不代表本任务；已建立本文件；用户复核时本文件刚创建，存在时序差，现已按纠偏补齐。
- 首次 `pnpm typecheck` 因依赖未安装触发自动安装，且 pnpm 11 的 ignored-builds 报错；已用 `pnpm install --ignore-scripts` 按既有锁安装，后续直接调用本地 `vue-tsc`/`vite`/`vitest`。
- 修过类型错误：未使用导入、`Screen` 预览状态缺键、会话冲突字段 `id`、导航选择恢复、可选参数点击处理器；当前 `vue-tsc` exit 0。
- 未做浏览器截图/真机/真实服务/真实 AI；最终报告逐项明示。
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

## Lead 反馈处理

- EA03 刷新丢当前对象：新增持久化 `state.selection`（计划/训练日/教练/服务包/学员/会话/记录范围与动作/自己的训练日/AI 模板/教练训练日与模式），`persistNow` 同步，冷重载恢复；打开弹层、确认 hash、焦点不持久化。已补 store 冷重载正例。
- EA04 替换丢失确认记录：替换前把进行中/已完成会话快照归档进 `records`，进行中会话标记未完成与已记录组，不伪称完成；取消替换不改状态。已补正反例。
- EA05 消息标题写死：改为按当前会话 `coachName`/学员渲染；进入江遥、何岸会话时标题、服务入口、草稿与发送对象同源。
- EA06 产品正文混入实现/验收说法：逐段替换教练待办、教练详情、学员详情、学员列表、探索空态、饮食、记录、AI 的“假指标/不调用模型/样例集合”等表述，保留来源、版本、限制和健康范围。
- 内容拆天：`daysFromContents` 不再按动作数量推断；所有内容保留一个完整训练单元，另为「一周两练 · 力量基础」明确书写两个训练日。
- 交付字段：删除 `sentDelivery` 重复字段，统一以 `coach.deliveries` 为交付事实源，`student.incoming` 从同一不可变快照生成；发布同版本幂等。

## 下一步（即时）

1. 运行 `vue-tsc`、`vite build`、`vitest`，逐项修复回归；更新旧 fixture 断言以反映统一交付事实。
2. 4187 启动并保留服务；记录 PID/日志/散列。
3. 写 `D01-03-report.md/json`、`implementation-map.json`、更新 README/ASSETS，交 Lead 实际路径验收。

## 错误与处理

- 无（尚未开始实现）。
