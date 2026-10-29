# A04-04 · 训练事务认证错误分类

状态：partial，未接受。基线 `894a078`，Lead 实现，现有 reviewer 只读复核未见新阻断。初次认证仅在会话层明确返回无效或撤销时给出 `ErrAuthentication`；数据库故障、未知认证错误和缺失主体返回 `ErrUnavailable`。事务中 `Principal.ForTransaction` 暂不能区分真实撤权与校验故障，因此前后复核失败均保守返回不可用。这样可能延迟提示已撤销或过期，但不把不确定故障当作要求退出；下一次初始认证可重新判断。年龄不满足仍独立拒绝，所有失败均先回滚。

锁定 Go `go test -race -count=1 ./internal/modules/workout/application` 通过（无 DSN 时 DB 用例跳过）。本卡 `mysql_smoke.py` 在私有 Unix socket MySQL 通过两个完整训练事务测试：包含在途授权撤销、组更新和完成确认的全部回滚，以及取消上下文后的回滚和不可用分类；不连接真实服务。首次数据库全包运行发现 A04-05 的完成测试仍期望旧错误类别，配套测试提交 `a0da684` 改为保守分类并重跑通过；失败目录保留 `/private/tmp/nerae-workout-integration-mysql-s1aa4_l2`。

客户端、HTTP 路由、幂等与真实双设备尚未接线；`Principal.ForTransaction` 的细分错误仍待设计，不能称故障提示已向用户交付或任务已接受。
