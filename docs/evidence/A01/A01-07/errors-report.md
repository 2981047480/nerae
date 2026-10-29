# A01-07 · 会话无效与服务故障分类

状态：partial，未接受。基线 `a950d6c`，Lead 实现，现有 reviewer 只读复核未发现当前存储层新增阻断；不代表 HTTP 或客户端已经使用分类。

认证校验将无效签名、已过期或缺失的会话、撤销的身份/设备/会话、禁用用户及令牌版本变化区分为 `ErrInvalidSession`；数据库查询错误、上下文取消、已结束事务、记录或归属损坏和未知内部状态为 `ErrSessionUnavailable`。校验前明确检查签名器配置并固定一次有效时钟值，锁等待后重新读取时钟和验证，避免异常签名器或零时钟被误判为用户退出。现有事务和加锁顺序不变，`Principal.ForTransaction` 仍保守折叠错误，调用方不可据此武断退出用户。

验证：锁定 Go `go test -race -count=1 ./internal/modules/auth/session` 通过；`python3 -B docs/evidence/A01/A01-07/mysql_smoke.py` 在仅私有 Unix socket 的隔离 MySQL 中通过全包测试（含年龄声明、邮箱会话、并发撤权、签名/时间及错误分类），成功目录由脚本清理；无真实邮箱或设备。首次重跑发现年龄测试先写入合成数据后，旧会话测试仍统计全库数量而失败；已把三个计数限制为本用例主体并通过复验，失败目录保留于 `/private/tmp/nerae-auth-session-mysql-md5wesfx`。测试脚本首次编译也因一个旧调用漏传新增测试参数失败，修复后通过。

未完成：`middleware/auth.go` 和 workout application 仍把这些错误合并，不能据此映射真实 HTTP 401/503 或自动清除登录；还需分别在相应任务写域接线并复测。缺少真实验证码发送、完整登录 HTTP、幂等/限流和设备验证，不能将此记录视作可安装 App 或登录已完成。

后续集成：`e305bd2` 通过私有 MySQL 会话复验；`894a078` 的未挂载 middleware 和 `68c9a19` 的训练事务已分别接收并复验此分类。事务内 `ForTransaction` 仍保守折叠为不可用，真实路由与客户端仍未接线。
