# fix03 同轮补充 · 原 M02/M04/M05 合同

Lead在逐文件相同的a289固定构建4188继续真实流程；只补原有动效合同，不改风格、范围、模型或会话，合并到当前fix03一次交回。

1. **M05 已实际复现**：真实计时01:15→01:14，暂停、刷新保持01:14；再真实暂停5.5秒后点继续，**即时显示01:20**，1.3秒后变01:13。未模拟时钟。证据主仓 `docs/evidence/D01/round-03/lead/expansion/frozen-03/immutable-flows/rest-resume-clock-display-probe.json`。a289 `resumeRest`改deadline后ensureTicker，但`clock.now`在暂停时停止，首帧使用陈旧now。恢复首个可见值应连续，不先增大后跳回；保存的remaining和真实deadline语义保留。补正常/暂停/冷重载恢复正反例。
2. **M04 可见分支**：真实完成42.5kg×11后马上进入休息，1/12与事实正确，但即时DOM没有动画元素。a289 TrainingView的`set-row--just`/160ms `set-confirm`仅在普通当前动作分支，而completeCurrentSet同一轮立即startRest；900ms的justCompleted在75秒休息后已清除。确认标记需在实际可见的休息/完成反馈分支发挥作用，120–160ms短反馈跟真实记录，不用虚假等待、延迟落盘或阻挡下一组。若已有其他可见反馈能满足合同，请给实际证据。
3. **M02 挂载时序待核实**：真实发现→完整计划，只观测到220ms页面进入，未捕获封面bridge。采样文件 `m02-plan-bridge-samples.json` **首次读到292ms，不能仅凭采样断言从未运行**。但a289 App.vue在screen watch的nextTick+rAF就查`[data-plan-hero]`，非训练main使用out-in，旧页面140ms退出后才挂新详情；runPlanBridge找不到目标直接return。请核实并使所选封面/标题接续或240–300ms局部交叉淡入实际发生，目标挂载后触发；快速返回/Reduce Motion不遗留ghost。已正确实现则提供可复核过程，不为未捕获而盲改。

Lead已实际观测M03的260ms focus与标题ghost、M06 220ms弹层/200ms遮罩、M07 300ms差异；这些保留。UI可见读数与终态不靠动画才成立。M10及其余触发Lead继续检查，不声称fps。最终fix03报告包含本补充的真实处理、未解决与证据；只针对性检查，无需重跑全矩阵。
