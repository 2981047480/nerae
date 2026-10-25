# 单位走查方法修正

`core-bird-dog-record-chart`在selectOption后捕捉到图表离场中间态：页面已选择鸟狗式，但旧SVG仍是哑铃卧推/kg。该捕获不作图表终态接受或产品失败证据。终态等待已从view/focus扩为所有Vue enter/leave active（包含chart），另存settled重拍，不覆盖原捕获。动效过程另行触发和检查。

执行者截图`fix06-390-core-bird-dog.png`实际描绘侧桥输入，保留原字节但不由文件名宣称鸟狗式视觉已验。Lead独立`core-bird-dog-unweighted-input`才是该动作的实际输入截图。

CUA模块载入器不支持在相对静态import附查询串（Module not found），未发生UI操作。已恢复普通相对import，通过ctx.qa显式传入本轮新载入的终态检查函数，避免复用REPL中的旧依赖。

随后`core-bird-chart-mixed-unit-terminal-bug`为**真正终态产品失败**：无enter/leave class、SVG opacity=1；所选鸟狗式，SVG轴/aria仍kg，点0与10，差值却“+10 次”，来源仍“最高重量”。这是旧perSide/0kg与新bodyweightPerSide/reps10同名记录混进同一series，与最初动画中间帧是两件事。另存DOM JSON与截图，不能因修正截图方法而忽略真实缺陷。
