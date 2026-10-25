# 同轮V04补查 · 服务包状态文字

不停止/不重开原fix05，只补原07接受标准的一个普通文字对比问题。Lead已走全部16族和双方消息列表/会话，发现两个未选中服务包的“学员可见”状态字需要修复；不重做配色或字号系统。

固定559实际390/100 DOM：`.package-tab-state` 本身opacity=.8，父button及package-tabs opacity=1；未选中前景rgb(94,107,112)，实际不透明canvas rgb(243,242,238)。合成后约rgb(123.8,134,137.2)，对比**3.3318:1**，12px普通功能文字要求4.5。不是禁用按钮/装饰分隔点。选中项白字深底可读。

证据：`docs/evidence/D01/round-03/lead/expansion/frozen-04/contrast/s16-package-label-opacity.json`、`s16-package-label-composite.json`及同名截图。源码CoachServicesView.vue `.package-tab-state { opacity:.8 }`。请最小修复（例如消除这层文字淡化，最终以实际计算/截图为准），同时保留选中态、隐藏态、200字号、三服务包与旧约定。

写入同份fix05报告，针对性computedStyle/实际底色4.5检查即可；不再扩展功能/重跑全量。Lead继续最终同版本验收。审阅页两个装饰“·”的低对比已明确排除，不要求修改。
