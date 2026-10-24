# fix02 收束补查 · 原EA23/EA27两条状态一致性

同一恢复轮/模型/写域，仍是原清单，不扩业务、不重做全矩阵。当前源码静态审阅发现：

1. EA23/C04的信息不足状态仍只在RecordsView把points置空，而recordViewRows/summary/feedback仍是4条完整卧推记录；页面却说“所选动作没有已确认的完整记录”。旧frozen-02/states-s05-03.txt已有实际界面证据。请让信息不足的样例投影、图表/摘要/明细/原因彼此一致；不能仅靠清图或一句假原因。保留正常历史与已修空/单条；给有效的信息不足反例。
2. EA27的再次编辑目前只在updatePlanField/updateDayTitle调用syncPlanDraftWork；addExercise/removeExercise/undoRemoveExercise没有同步。周岚发布后新增或移除动作再回列表/待办，不能仍显示已交付等待采用且草稿todo.done=true。请覆盖全部实际编辑入口；撤销回原内容时按真实dirty状态决定，失败/取消不假完成，专业回复事项仍保留。

这两项是原“同源状态/再次编辑恢复待办”的漏口，主要是局部逻辑与正反例，不添加新界面。与EA21及现有全部修复集中一次最终类型/构建/测试、报告md/json和三清单后正常交回；未解决如实写，不要循环扩大打磨。
