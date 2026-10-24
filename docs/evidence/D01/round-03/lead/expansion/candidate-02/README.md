# 第二批变化中源码检查

这些是D01-03原实现轮次仍在修改时的真实4187预览观察，不能当冻结接受证据；sourceRevision明确标为unfrozen-live-full-worker-round1。Lead未改UI。

- 教练详情、待办实际390×900截图用于过程视觉检查。
- 周岚的草稿待办打开林澈计划，见zhou-draft-from-todo.txt。
- 周岚未授权健康内容时，实际头部/证据/sidebar仍显示训练组数，见zhou-no-health-detail.txt与重试截图；已交回EA09/EA10。
- zhou-no-health-detail-390第一次截图scrollY从0.5变0，helper因此排除，保留原图与捕获日志，不作为稳定截图；同状态重试before/after均390×900、DPR1、scroll0，尺寸核实通过。这里没有手动裁剪、DPR假定或伪造几何。

后续冻结后需逐项重跑，不能从这些候选截图自动升级为accepted。

新增过程观察：ai-390是HMR异常导致的空页，不能判AI设计；显式冷重载又复现S16旧合法services缺priceInput导致trim错误（main0），已交EA16。normalizeServices之后，实际冷重载保留759/8周/12小时回复/版本4，原699/版本2约定不变，新控制台warn/error0；相关DOM与日志另存。AI-ready、饮食、收到计划正常页另有截图。S09采用条在无主导航时仍bottom72，scroll0几何与incoming-action-overlap截图对应；另一份scroll64几何单独留存，不与该图混用。仍未冻结、未accepted。
