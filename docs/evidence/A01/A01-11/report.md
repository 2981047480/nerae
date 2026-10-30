# A01-11 邮箱登录事务协调：本轮证据

状态：内部事务接缝已实现并通过独立审查；任务仍为 active，不代表真实邮箱登录、跨设备同步或安装包已完成。基线 `cfe9c26`。Lead 实现并审查；独立审查角色 `/root/foundation_review` 只读核对事务、安全及测试，未自行重跑数据库测试。

- 对已发出的邮箱挑战，错误码计数提交后再统一拒绝。通过的挑战、身份解析和可信设备会话在同一事务内处理；仅提交并重新验证后释放令牌。
- 新设备仅获得与挑战绑定的待本人批准申请，不创建会话。申请存储错误时事务回滚，挑战仍可在安全条件下使用；并发消费同一挑战只产生一个会话。
- 隔离库覆盖首次主体、可信设备再次登录、新设备待批准、错误码上限、不同主体、审批密钥错误回滚、重复/并发消费和敏感结果的序列化与日志防泄漏。

验证：

- `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMODCACHE=/private/tmp/nerae-go-api-mod GOCACHE=/private/tmp/nerae-go-api-cache /private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -race -count=1 -v ./internal/modules/auth/application`，退出 0；因未配置隔离数据库，数据库事务测试按预期跳过，此命令只验证编译和无依赖负例。
- `python3 -B docs/evidence/A01/A01-11/mysql_smoke.py`，退出 0；使用新建私有 Unix socket MySQL、合成数据和 10 项既有迁移，`TestEmailLoginTransaction` 与 `TestEmailLoginRejectsMissingDependencies` 均通过，成功后只删除本次数据目录。
- 测试程序未发送邮件、未连接用户服务器或任何现有数据库。对临时脚本运行 `python3 -m py_compile` 时，系统 Python 尝试写沙箱外缓存而失败；随后以 `python3 -B` 的 AST 解析通过，且实际 MySQL harness 运行通过。

被测文件 SHA-256：

- `email_login.go`：`8b26e14f0cb78369c7edab02dbaef2fc028d0aa46a50f02661c9fd6d0eb45ffa`
- `email_login_test.go`：`5674d97adf5f540878f082bb53f1446f6454f3377b1b494ecc313d3bfd936a30`
- `mysql_smoke.py`：`40ea702ae263760e130775fffb139171f5cdcd03176fef404b5fe099718c3b14`

独立审查未发现本协调器 P0/P1 问题，提出两个上线阻塞：本协调器尚无请求幂等和提交结果未知恢复；既有新设备批准兑换实现拒绝重复兑换，而当前 OpenAPI 承诺幂等重放。这两处须在公共 HTTP 接线前收敛。此外，跨挑战限流、SMTP 真实送达、原生设备证明、HTTP、恢复码、受控服务端部署及真实设备验证均未完成。不能将数据库测试当成真实登录或跨设备验收。
