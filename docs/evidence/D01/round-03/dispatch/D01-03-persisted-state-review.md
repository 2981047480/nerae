同一D01-03继续，保持原会话/模型/树。Lead看到了S16新价格校验、字段传播的源码改进；但当前真实浏览器已经被新增字段的持久化兼容问题阻断，请先恢复这一处再交回。

EA16 旧的合法本地保存状态不能冷重载：Lead先在S16正常保存759元/8周、范围/目标/响应，再遇到你的热更新。原priceYuan状态没有新增的priceInput。实际先HMR报错，Lead没有将它直接认作产品失败；随后显式reload冷启动仍只剩skip link，main与预览控制均不存在。新控制台错误06:31:26Z：
TypeError: Cannot read properties of undefined (reading 'trim')，servicePriceIssues；CoachServicesView render失败。
源码loadState仍services: parsed.services ?? base.services，servicePriceIssues直接draft.priceInput.trim()，未按每个包补新增字段。

证据绝对目录：
/Users/zephyr/Documents/项目/nerae/docs/evidence/D01/round-03/lead/expansion/candidate-02/
after-live-hmr-cold-reload.txt、cold-reload-service-error-logs.json、screenshots/cold-reload-service-render-error-390.jpg、viewport-captures.jsonl。实际390×900/DPR1稳定，mainCount0、skip目标不存在。ai-390是发生HMR崩溃后的空页面，不作为AI美观判断。

请在加载/迁移中为已有合法services草稿与saved快照按包补新增字段，价格输入从已有有效priceYuan转换，单位/新增配置用可信同包默认；保留原来的已保存/未保存内容、记录、当前对象、版本和已确认约定。不要删localStorage、换key、强制reset或退回seed掩盖。恢复入口也不能随某页渲染异常一起消失。用缺少新增字段但包含旧合法修改的状态补回归，冷启动应能进S16/其他页，浏览器实际保留759/8周等旧修改。只恢复开发预览，不需改生产schema/API。

Lead暂时停止在崩溃页面操作以免混入无效观察；仍在独立整理验收。修完全部EA01–16/VR01–07并保存最终报告/55映射后交回，未accepted。
