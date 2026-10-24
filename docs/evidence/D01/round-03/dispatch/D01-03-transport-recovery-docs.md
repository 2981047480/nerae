原D01-03会话的连接失败恢复；不换模型/工作区，不开新会话。Lead已在原生UI核实上一轮TRANSPORT Connection error、内部5/5重试后失败（第1轮336步），不是正常完成。此前源码/75测试日志/55映射已保存，报告md/json仍缺。

Lead现已将已有文件原样冻结到本树Git提交6dba9b6085c9a7f0b2ac84cf0fa8f290d4a7871d（ui/full-round-three，/private/tmp/nerae-ui-complete）。该提交仅为候选冻结，尚未accepted。初始输入基线b3949bf仍是历史，不要reset回它。Lead正在独立跑固定版本的代码/身份检查。

本次恢复只完成交回材料，禁止再改design/prototype/**（含README/ASSETS）或重置任何浏览器数据，禁止改Git HEAD；4187服务继续留着，不重启/停止。模型仍OpenCode Go / opencode-go/deepseek-v4.1-flash / Medium。

请现在在docs/evidence/D01/round-03/worker/补D01-03-report.md/json：如实标明上一轮传输失败、当前candidate待独立验收；从已保存日志引用最新75测试/类型/构建的实际结果，明确哪些仅是执行者检查、哪些未测。引用现有implementation-map.json（55项/16族/7流/12动效），概括EA01–16/VR01–08处理/未解决项，给实际文件、源码散列、端口/PID和限制。源码散列以冻结6dba提交为准；持续写入的dev日志不要冒充不可变证据，可引用冻结快照或明确排除。不要为报告重跑截图或继续自我打磨。

更新worker进度的过时模板文字即可，保留历史fix01/fix02和所有失败/候选记录。保存两份报告后自然结束这次文档恢复交回；Lead收到独立检查结果后，再在同一会话给下一轮修复。无需等人类重新批准，不代表缩减完整16族/55项目标，也不代表UI已经接受。
