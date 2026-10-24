# fix02 连接失败恢复 · 只接续未完成部分

原生UI已核实第4轮579步以TRANSPORT / Connection error / 内部5of5重试失败结束，**不是正常交回**。保持同一会话、OpenCode Go / opencode-go/deepseek-v4.1-flash / Medium、原树与4187，不切模型、不新开会话、不reset、不改Git HEAD。

Lead已把本轮已落盘源码原样冻结为 **aaf1fe0a8e9506b72c56157139eeda100d71a709**（未接受恢复点，不是6a基线回滚）。60源前后散列稳定，独立typecheck exit0、原83测试exit0；未新增fix02回归。证据主仓库 `docs/evidence/D01/round-03/lead/expansion/fix02-interrupted/`。旧报告全部保留。当前prototype clean，worker持续dev日志不算不可变证据。

大部分EA22/23/24/25/26/27、时间/文案/字号/缺图已有代码，不要重新从头梳理全部55项或重做风格。当前最重要未完成是EA21补查，直接接续当前实现：

- `src/state/store.ts`约764行goToIncomingPlan、2175行incomingView、2805行openMessageRef；目前history只查当前活coach.deliveries、找不到便回latest，plan引用一律转student。林澈发8后最后教练切周岚再打开林澈旧7会又开8；教练周岚消息点计划会切林澈语境；缺失引用也不能默默换成最新版。
- 按引用所属对象、读取方与不可变快照解析；学员/教练身份不能因点引用混淆。教练看当前学员交付可只读，不能变成该学员采用动作。版本号不是跨学员全局ID。
- 训练引用要真正定位被引用记录，RecordsView expandedRecord当前仍默认record-20261022；不能只改range/exercise而展开错记录；不存在/不可读取明确不可用并可返回。服务引用查具体包的现有改善保留。
- 详细静态反例在主仓库dispatch/D01-03-fix02-reference-context-followup.md，原EA21需求就在fix02-packet；无需重新读全部历史日志或全部截图。保留EA25非4动作数量、VR12缺图叠字等已收到补查。

恢复后先保存一份简短worker进度检查点，写明TRANSPORT失败和恢复点/待办，不冒充成功；再修EA21剩余、补有意义正反例与原有回归。核对原fix02各项已改结果即可，禁止扩新功能或无休止打磨。

一次最终typecheck/build/test与针对性真实浏览器检查后，立即保存新的 **D01-03-fix02-report.md/json**、source/evidence/report SHA清单（持续dev日志排除），记录实际退出码/未解决项/未测/PID/URL。MD约400–700中文字，明细放JSON；已有55映射引用即可，不重写55项。保留原D01-03与fix01报告。

随后正常结束并停止源码变动供Lead冻结。所有16族、五宽100/200、55语义与M01–M12最终检查由Lead统一做，不需要你再跑全矩阵。完整目标active，候选未accepted；不要等待人类额外批准。
