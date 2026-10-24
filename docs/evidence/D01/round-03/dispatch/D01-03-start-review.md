同一D01-03继续，保持当前Harness会话、模型与工作树。Lead用真实store模块独立跑了开始/替换诊断，替身window/storage，不是浏览器或服务端验证。三份源码在诊断前后散列一致，完整证据：
/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/expansion/monitor/start-candidate-diagnostic-01.log
诊断脚本同目录上级check_candidate_start.mjs，可在全量树design/prototype中用既有Node运行。

EA07 所选训练日与开始内容不一致：goToPlanDetail('plan-weekly-base')→selectPlanDay('plan-weekly-base-day-b')，activePlanDay为「训练日B·拉与下肢」，requestStartPlan并确认替换后，实际session却是「训练日A·推与核心」。startPlanNow取item.days[0]，忽略选择；请让所见训练日、替换/开始摘要、创建会话同一对象，刷新不串，重复开始同一请求仍不重建。

EA08 再次开始与归档ID冲突，第二次事实丢失：开始哑铃→真实completeCurrentSet记录12kg×8→切核心→再次开始哑铃→记录17kg×9→切核心。两次session ID都是session-plan-dumbbell-20261024，组ID也相同；第二次重新变pending，但archiveSessionBeforeReplace发现同archive ID直接return，最终只保留第一次12×8，第二次17×9消失。需要区分「重复点击同一次开始」和「已明确结束/替换后再次训练」的会话身份；同一次重复幂等，确实新的一次不能覆盖/丢掉前后已确认事实。旧事实保留、当前未确认输入/休息规则也按合同说明。不能通过修改诊断或跳过归档来遮蔽。补覆盖正反例，最终Lead会在真实UI重复路径复验。

这两项属F01/F02/历史与恢复合同，请并入当前全量交回；不要求重做品牌。最终报告/55项实现映射与全量验证仍要完成，尚未accepted。
