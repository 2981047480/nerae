# D01-03 fix05 · M02 可见接续

继续原会话、OpenCode Go / opencode-go/deepseek-v4.1-flash / Medium，原树 `/private/tmp/nerae-ui-complete` 与4187。第8轮fix04已正常交回，Lead原样冻结 `ae141ae639fde8234b7782bf79775f78cc5e04c8`，不是accepted。只修原M02/VR15，不重做风格、依赖、日期、业务范围，不改Git HEAD，不碰4186/4188/4189。

## 真实缺陷与根因

Lead在散列匹配5599460的固定构建4189，390×900、Full Motion，实际从发现卡片按Enter到计划详情。`.plan-focus-ghost`确实出现，但DOM几何：left0/top1997/bottom2308，position=static，zIndex=auto，pointerEvents=auto，backgroundSize=auto；目标封面left20/top64.56/350×320。ghostInViewport=false。证据：

`docs/evidence/D01/round-03/lead/expansion/frozen-04/motion/m02-ghost-visible-geometry.json`

源码App.vue确有规则，但放在 `<style scoped>`；动态 `document.createElement('div')` 后直接append到body，元素没有组件scope属性，因此规则未命中。请核实此根因并做最小修复。不要只检查元素存在/280ms，要证明实际可见、定位与装饰层行为生效。

## 接受条件

- Full Motion真实卡片→详情有可见封面接续：起点/目标几何正确、fixed或等效可见覆盖层、背景cover/center、pointer-events none、合理z层；完成移除、不改变布局。
- 快速进入后返回，再次进入，不遗留ghost或影响新目标。
- Reduce Motion等效终态，不创建大位移/缩放ghost；键盘焦点/返回照常。
- 保留fix04引用/迁移、M04可见确认、M05恢复时钟、原四情境。新增检查应覆盖实际computedStyle/视口相交，不写只镜像类名的测试。

只跑针对性浏览器与现有类型/构建/测试；全量矩阵由Lead进行。完成写新的fix05 md/json、源码/证据/报告三散列清单，日志排除持续变化dev日志。报告实际测过与未测过，自然交回后停止源码变动。Lead只验收，不补UI实现。
