继续同一D01-03、同一会话/模型/树与全部A–D。Lead已用真实store模块、替身window/storage做独立诊断（不是浏览器/服务端证据），检查期间store SHA始终67e7b3d67dbf3edd6ed32555509dc2f1194ea2b2fe67dc488d1c778286b03a3e。证据绝对路径：
/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/expansion/monitor/store-candidate-diagnostic-02.log

请把下面两项实质问题并入当前实现，不等最终页面后再修：

EA03 刷新丢当前对象：goToPlanDetail('plan-dumbbell')后正常持久化，冷重载真实store时screen仍planDetail，但selectedPlanItem变plan-upper-pull。ui.selectedPlanId等选择只存在临时ui，persistNow只写state。需要详情、当前教练/学员/会话、已选范围和返回上下文按其恢复合同保留；重开不应显示另一对象。可以新增/调整持久化选择数据，但不把打开的确认弹层、旧确认hash或焦点当作必须重开的业务状态。补真正“选非默认对象→保存/刷新→同对象”的正例。

EA04 替换计划丢已确认记录：从初始session-20261024-strength（3组已确认）requestStartPlan('plan-dumbbell')返回replace，confirmReplaceAndStart后旧s-a1-1/2/3既不在新session也不在records，3条事实全部丢失。替换安排只能影响合法未来，已有记录和进行中会话的快照/恢复位置必须保留；不能把不完整的旧会话伪称已完成。默认能继续旧会话；若允许另开新会话，旧会话与已确认组必须仍有明确可达记录。按真实source-of-truth修正并补正反例，取消不改原状态。

早前两项内容拆天/交付字段反馈在源码已见改善，后续仍需实际UI验证。本条不增加界面范围、不重做视觉；继续原计划落盘并完成全量后报告。Lead没有改实现代码。
