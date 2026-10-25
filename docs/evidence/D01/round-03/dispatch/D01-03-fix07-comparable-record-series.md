# fix07：记录只比较相同测量语义

原 D01-03 / C03–C04 单位一致性缺陷，不新增界面或重做视觉。执行者仍是同一 Harness / OpenCode Go / deepseek-v4.1-flash / Medium，原树 / 4187。基线为 Lead 正常冻结 `54b74c86b93e80967b84d9fff4cba95fd0be9ba1`，不改 HEAD。4186/4188/4189/4190/**4191**固定预览均保留，Lead继续其他独立检查。

## 已通过与真实失败

fix06第13轮正常1h2m50/924步交回，60源/16证据/2报告核对一致。Lead独立类型/构建/109测试/原27接缝全过；真实编辑/保存/发布8、最后切周岚、旧7冷恢复、新8消息取消/确认/刷新/再次打开已采用、今日3组/历史保留均过。自重/秒每侧真实8组、0值阻断、暂停冷恢复也过，不重做这些。

固定4191完整核心8组后选择“鸟狗式”，**真正终态**（无enter/leave类、SVG opacity=1）为：

- 10/21旧记录是kind=perSide、weightKg=0、reps=10；10/24新记录是bodyweightPerSide、reps=10、weightKg=null。
- `recordChart`按同名拼成点0 kg与10 次；RecordsView取首点单位显示**kg轴 / 最高kg变化 / 最高重量来源**，delta却是**+10 次**。不同量被减出来，误导成长。
- 证据：`lead/expansion/frozen-07/units/core-bird-chart-mixed-unit-terminal-bug.txt`、对应截图和`mixed-unit-terminal-dom.json`。初次截中chart离场是单独QA方法问题，已排除；本条不是动画假象。
- 追加独立第28项已实际执行：28项27过1败，源码前后60文件稳定。`frozen-07/code-followup/identity-seams.json`中保留两点和delta10。原27绿色快照另存，不混称28通过。

## 窄修复

`store.ts:recordChart / recordSummary / lastReferenceFor`与`RecordsView.vue:chartUnit / source text / recordExerciseValue`保持同一测量语义：负重、次数、自重次数、呼吸次数、秒及是否每侧不能按名字直接混比。

- 默认图采用可解释的同一记录方式；不把kg与次、秒与次数、每侧与非每侧混进同一series/差值。无需新增复杂切换控件，可在明细保留不能直接比较的旧方式，说明用户实际需要理解的比较范围。
- **旧已确认数据不可改写、删除或猜测迁移**。不能为了图表变绿，把旧0kg偷偷改成新10次，或改fixtures绕过。无法证明同一测量语义时从可比较趋势排除，旧事实仍可读。有且仅有一个可比较点时不造增量/趋势。
- 轴、aria、数值、摘要、来源与明细同单位。次数/呼吸次数不能继续叫最高重量；真实kg/秒分支保留。
- `lastReferenceFor`当前按名字找到旧fact后直接套新kind的perSide。也须按相同语义选参考；不足时明确暂无可比较参考，不替旧记录添加每侧/呼吸含义。不改原记录。
- 保留最新引用采用、旧引用只读/撤权/缺失、M02/M12/V04和所有现有回归。不改正式API/ER/依赖/设计token。

## 最低验证及交回

1. 正常UI完成Core后，鸟狗/侧桥图和明细不混单位；旧0kg/旧秒记录保留且不被重写。一个可比较点明确不足，不显示假+10。
2. 新自重/呼吸/秒每侧的本次与再次记录/上次参考、记录/图表单位一致；负重仍kg，真实历史不变。覆盖有效与不兼容/单点反例。
3. 同版类型/构建/全部既有测试与必要新回归，针对性390真实UI，报告区分浏览器/模块/未测。
4. 保存fix07报告md/json、source/evidence/report三SHA清单（排除dev日志），自然正常交回并停止源码，Lead再冻结独立复验。候选不自标accepted。

补充实际对照（同固定54，未改源码）：Mobility6组含呼吸冲突远端选择只确认一次、0呼吸阻断、全部完成过；猫牛图单点8/次呼吸且不造趋势，但来源仍错误写“该动作最高重量”。证据frozen-07/units/mobility-breath-record-chart-terminal.txt。fix07范围已含此来源文案，不新增设计。
