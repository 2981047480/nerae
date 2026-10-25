# M01–M12 · 实际触发与等价结果

Lead通过正常UI触发full/reduced，DOM读取计算样式和终态，不修改动画时钟/应用状态。708的实际动效组件在898均未改；记录页脚本窄改后M09在898复验。源差异见[source-change-scope](source-change-scope.json)，原始观察在[708 motion](../frozen-09/motion/motion-probes.jsonl)及[898 M09](motion/motion-probes.jsonl)。

| ID | 实际触发与观察 | 减少动态效果 |
|---|---|---|
| M01 | 学员导航换发现，退出140ms/进入220ms，标题和焦点接续 | 立即同一任务与标题 |
| M02 | 所选计划→同标题详情；实际ghost fixed/z150/pointer-events none/cover，进入有opacity与位置接续；终态清理 | 无ghost，同封面与内容 |
| M03 | 今天继续当前组；实际focus-enter260ms与session ghost，保留动作/组号/输入 | 立即同一合法会话 |
| M04 | 真正完成第4组；可见rest-recorded--just，set-confirm160ms，真实4/12 | 动画约0.001ms，文字与数值相同 |
| M05 | 真正完成后休息，暂停→刷新仍同秒数，继续后1:30→1:29；无数字闪烁放大 | 同一真实计时、暂停与恢复 |
| M06 | 登录/替换弹层；实际打开与Tab循环/Escape返回，sheet约220ms、scrim200ms | 相同关闭与焦点规则 |
| M07 | 修改实际备注、保存、审阅；相关plan-row闪底300ms，完整差异文字保留 | 约0.001ms，改动文字仍在 |
| M08 | 实际写消息并Control+Enter发送；message-enter160ms/4px实际被捕获；双方发送/失败/重试另外走通 | 约0.001ms，立即保留同样状态与正文 |
| M09 | 898真实选择8周；chart-fade180ms/明细160ms，日期/来源/单位一起更新 | 约0.001ms，范围与数据相同 |
| M10 | 实际Space主操作打开影响确认，按钮计算样式transform90ms；源码active scale0.985 | 约0.001ms，操作仍生效 |
| M11 | 队列实际周岚→返回→林澈，student-context-in180ms，选中姓名和各自内容明确 | 同一对象与标题立即显示 |
| M12 | 正常UI加载3组失败夹具→重试→失败标志解除→刷新仍3组，full/reduced均过；真正存储异常另由模块QuotaExceededError正反例覆盖 | 同样的状态文字、重试与事实 |

接受范围：任务因果和终态、实际计算时长与减少效果等价。未测60fps、硬件触觉/声音、物理设备软键盘或操作系统设置切换；M10没有捕获按键保持期间的形变，不伪称已捕获。UI没有播放假系统声或震动提示。

方法校正：708 motion中m12-full-recovered-same-facts不是合格保留4组证据——切换训练预览会加载3组夹具；m12-full-corrected-confirm-four-under-failure也不能当存储异常证据，真实完成动作会清除该预览状态。采用m12-final-full/reduced-*的正确3组夹具路径；[training-motion-final-results](../frozen-09/motion/training-motion-final-results.json)里的clockMeasurementMillis含后续操作，只用于过程记录，不当精确计时间隔。真实浏览器存储故障未注入，模块故障见最终116测试与源测试，不混为服务端恢复。
