同一D01-03接续。Lead新增一项真实模块接缝缺陷，并提供中间验证结果供你定位；不重启、不换模型、不删合法校验。

EA15 今天完成后更换内容仍须说明未来安排：Lead用真实completeCurrentSet、每组明确填有效kg/次数或秒、真实skipRest完成哑铃12组；原student.schedule仍有未来项。然后goToPlanDetail('plan-core')→requestStartPlan直接返回started、sheet=null，planVersion和未来schedule在任何确认前已改变。原合同F01/F02要求已有安排更换时明确替换影响；不能只在当前session.in_progress时拦截。今天已完成的事实保留，同一次完成不能重放计数；切内容需要明确取消旧未来/建立新未来的确认，取消不改内容/安排/历史。今天完全无安排等场景按其真实状态处理，不无故加确认。

证据绝对路径：
/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/expansion/monitor/start-candidate-diagnostic-03.log
三源前后散列一致；这是模块+替身window/storage，非浏览器。脚本check_candidate_start.mjs里完整输入循环可参考；与UI实现分离。

Lead interim-check-01也已实跑：typecheck exit2（当时Today两个未用导入，之后已见源码改动，不当成最新错误）；vitest 66/67、exit1；直接vite build exit0（不是含类型检查的package build）。检查期间60源文件未变，日志在同monitor/interim-check-01/。不要把这个中间结果当最终通过。

该测试失败处expansion.test.ts循环到「哑铃推举」时weightKg/reps为空，completeCurrentSet正确拒绝；测试目前只给首组填了输入，却假设没有历史的新动作也能自动完成。应明确为每个当前组填写合法测试输入，保持无数据不猜重量的产品行为；并按EA15检查完成后更换的确认。不要为了绿勾删除空输入校验或造上次参考。

继续合并EA01–15、VR01–07后更新进度、最终报告与55映射，运行最终同版验证再交Lead。变化中候选均未accepted。
