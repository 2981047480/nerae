# 过程诊断记录

第一条启动命令因工作目录为prototype而使用相对Lead脚本路径，脚本未创建，Node报MODULE_NOT_FOUND；没有执行store或修改UI。原错误保留store-candidate-diagnostic-setup-error.log。已用主仓库绝对路径创建脚本，第二次实际诊断exit0。

第二次执行真实候选store，window/storage是替身。store前后SHA一致，复现非默认详情冷重载对象改变和替换后3条已确认记录丢失；结果是缺陷发现，不是通过。对应反馈data-review-02，仍待指定执行者修复与真实浏览器/最终冻结复验。
