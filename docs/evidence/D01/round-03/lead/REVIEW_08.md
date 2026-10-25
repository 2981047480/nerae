# fix03 冻结复验 · 引用返回与旧缓存仍未通过

第7轮正常45分7秒/752步交回；Lead核对60源码/21证据及两报告散列，原样冻结 `55994603461ee5710a5be4f70b1180d445dba5c5`。独立类型、构建、97测试 exit0。新增引用生命周期检查首次有一处测试夹具错误（把没有id的delivery对象当MessageRef）；已改从实际发布消息取ref，保留原错误日志，不当产品失败。

修正后独立21项检查 **17过、4败**，见 `expansion/frozen-04/code-reference-followup/summary.json` 与 `identity-seams.json`。检查是实际store模块加隔离的假window/storage，不能冒充浏览器或服务端证据。当前新格式下，教练周岚引用冷恢复/返回、学员旧7在新8后冷恢复、缺失引用冷恢复均已通过。

仍失败：

1. 非默认9/29训练引用冷恢复保留正确recordId，但 `goBack()` 返回today，而非原messages；引用打开复用了会清导航栈的普通记录根入口。
2. 有效旧coach/messages缓存仅缺新增selection.actor，parsed.actor仍coach，但冷加载变student，conv仍周岚。
3. 旧coach/incomingPlan引用缺未曾持久化的owner/version/readOnly字段，冷加载变student，展示林澈latest7且available=true；应保留可信读者并明确引用不可用，不能猜最新版。
4. 旧records引用缓存缺新增focus字段，冷加载explicit=false，界面会落普通默认10/22展开模式；应明确未知引用而不假定原记录。

静态根因：loadState先merge base.selection，之后从merged.selection.actor取值，base默认student使对parsed.actor的fallback永远不用。记录引用入口仍清stack。新增字段不能只补类型默认，还要校验共享页/引用来源的不变量；已被默认填过的旧缓存也不能因字段存在就认定引用可信。

结论：**本候选未接受，集中fix04**。不重做视觉或业务边界。多条流程在a289固定构建的真实操作已落盘（F03林澈新旧采用尚未完整走完）：完整12组、取消/替换保留3组、双向重试/未读/私稿、撤权四读面、自己的饮食/公开审阅、AI仅私稿、新759与旧699分离。它们是旧源证据，不能代替最终同版本复验。M02/M04/M05的新渲染仍待Lead检查。完整目标active。
