# F02-00 · transport 边界提前开工的任务图修正

状态：规划调整，非 F02-00 或 F02-02 最终接受。Lead 在集成基线 `a71a7840e0c390eaca66906ea6c5bc35c8e21eef` 上串行修改任务图；独立 reviewer `/root/contract_tests` 只读核验依赖、写域与新增测试。F02-02 使用该基线中已验证的生成合同快照，开始依赖为已接受的 F01-01/02/03、K01-01、Q02-02；F02-01 保留为最终接受前置。F02-01 最终合同或生成物变化后，F02-02 必须在同一最终版本重新验证，早期工作不能替代正式接受。

`scripts/tests/test_plan.py` 此次由 F02-00 的 Lead 单 owner 按其既有写域串行维护，不计入 F02-02 任务产物。新增开工条件、最终验收前置和 acceptance-only 边不排序写者的正反例；F02-03 仍等待 F02-02。`scripts/plan.py check` 通过，`python -m unittest discover -s scripts/tests -v` 53 项通过；独立 reviewer 另单独运行开工/验收测试通过。该调整不改变业务合同，不代表认证、幂等、业务 HTTP 或可安装 App 已完成。
