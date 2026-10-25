# D01-03 fix03 · 引用完整生命周期与两处布局/入口

保持原会话 `session-15de3174-0b22-47b7-9574-29dd3b032a8c`、OpenCode Go / `opencode-go/deepseek-v4.1-flash` / Medium，树 `/private/tmp/nerae-ui-complete`、`ui/full-round-three`、4187。第5轮已正常交回；Lead把原样源码/worker冻结为 **a289b090b5b172cf027b37dd0d5535659dd68364**。不要回滚aaf/6a、改Git HEAD、重设计风格、换依赖或扩业务。Lead没有写UI。

先读主仓库 `docs/evidence/D01/round-03/lead/REVIEW_07.md`；证据都在同仓 `lead/expansion/frozen-03/`。独立类型/构建/91测试/14接缝过，220尺寸捕获（10错角色排除）、96逐态样例已落盘，但下面实际错误阻止接受。只集中修这4项：

## EA28 / EA29 · 引用打开、刷新、返回必须是同一个上下文

- **实际返回错误**：教练→学员周岚→继续草稿→保存/发布→回复→点周岚旧交付6（最新7，林澈也有7）→标题“教练查看交付版本”、周岚内容正确；点页头返回后变成学员林澈消息列表。截图/DOM `ui/flows/f03-coach-reference-back-wrong-student-actor.*`。
- **实际刷新错误**：同一周岚旧6只读交付刷新后，头部仍顾言·教练，主体却变成林澈力量基础7/现用6，还出现“采用这份安排”。见 `f03-coach-old-zhou-ref-reload-wrong-lin-content.*`。未点击错误采用。
- 当前 `actorOfScreen` 无法表示共享messages/incomingPlan的读取方；`goBack`从screen重推actor；`createInitialUi`又把 `incomingHistoryVersion/StudentId/ReadOnly/RefMissing` 还原默认，`syncSelection`未保存引用。这是当前静态线索，按可靠的角色/对象/不可变引用上下文修完整生命周期，避免每入口补一行actor而遗漏返回与冷恢复。
- 已有EA03/EA21边界不扩张：学员林澈旧7在最新8、最后教练周岚时，打开→刷新→返回仍是林澈旧7；教练周岚旧交付的对象/版本/只读保持；缺失引用刷新仍不可用，不能默认latest；训练消息引用原记录的定位在刷新/返回后也保持，不回默认10/22。共享版本号不是全局对象ID。只读教练入口在动作层也不能采用学生安排。
- 新/旧本地存储兼容：新增选择字段需有合法默认/迁移；无法恢复可信上下文时明确不可用并可返回，不能猜成林澈当前计划。不要持久化弹层/瞬时确认/动画本身。
- 补有意义的返回/冷启动正反例，不只测首次openMessageRef。记录真实对象、actor、版本、adoption gate和记录ID。

## VR13 · 教练消息预览入口

先选教练，再点预览中第二个“消息”会切回学员；实际教练主导航→消息是正确的。修预览入口角色语义，保留双方可分别进入；不靠重置整份样例或扩大权限。Lead错误角色10图已明确排除，不是教练通过证据。

## VR14 · 审阅标题320/200

`CoachReviewView` 的 `.change-summary-head` 把“本次改动 · 4 处”压成51px宽/225px高，实际逐字竖排。见 `ui/s15-squeezed-heading.json` 和 `ui/screenshots/s15-review-320-text200-below-fold-final.jpg`。让标题与“对照现用版本”在窄屏/大字时叠排或合理换行，保留正常中文行长；不缩字号、截字或隐藏信息，桌面审阅布局保留。

## 交回

写域仍仅 `design/prototype/` 与 `docs/evidence/D01/round-03/worker/`；Lead报告/任务/契约不可写。4188是Lead的固定a289构建，只读验收服务，**不要停止/重启/占用**；4186首批同样保留。你用原4187。

先修四项与针对性正反例；做一次最终类型/构建/全部测试、针对性真实浏览器打开→刷新→返回、320/200检查即可。全部矩阵/7流/12动效/55语义由Lead验收，不重复整套、不写长篇思考。

立即保存新 `D01-03-fix03-report.md/json`、source/evidence/report 三SHA清单、真实退出码/未解决/未测/PID。原报告保留，持续dev日志排除。然后**自然交回并停止源码变动**，供Lead新冻结；不要改Git HEAD、等待额外人类批准或扩大任务。
