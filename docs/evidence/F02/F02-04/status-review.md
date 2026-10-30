# F02-04 · 状态与结果授权增强

状态：partial。基线 `2c8c36a`。独立复核指出训练创建的无资源失败引用可能被误用于成功结果，导致重放时跳过记录归属检查；同时，若无资源失败正文含隐私，也不能仅靠引用确认其安全。

`Authorize` 在变更前接收空引用与状态 0，在首次提交前和重放时接收真实响应状态及引用；调用方可据此对成功结果强制资源引用。固定策略支持可信 `ValidateResult` 回调，在首次提交前及重放解密后、返回前校验正文；回调只收到正文副本，不得记录或保留。训练侧针对无资源失败的固定正文校验另在 A04-08 实现。数据库账号最小权限、真实 HTTP 接线与非敏感错误格式仍须最终业务复核。

`python3 -B docs/evidence/F02/F02-04/mysql_smoke.py`：首次编译失败，因为一个匿名测试回调仍为旧签名，修正后编译通过；其后新状态测试影响旧清理用例的模拟时钟，移动测试顺序后通过。以上两次失败的目录分别保留在 `/private/tmp/nerae-idempotency-mysql-5spsqwbg` 与 `/private/tmp/nerae-idempotency-mysql-n5one7z6`。最终加入结果策略后再次运行同命令退出 0，覆盖成功/失败状态重放、策略拒绝时业务回滚与重放不返回正文，以及原有隔离 MySQL 并发/撤权/清理测试。

- `store.go`：`e7fdd5b5fb098839455460f1ad8c2b5840810b1f3b1ed093fe273573bdc0a9f6`
- `store_test.go`：`9b0f9e763adb715fa288576e6a4e94f69aed6ef81302d7ca158789a2a167a17c`
- `transaction.go`：`16da03b31d8a6c74a1980514b6ae4c07b1b19edba7f2df5227ab51bf279413b5`
