# A04-06 本人训练列表：本轮证据

状态：内部自由记录列表已实现，任务仍为 active；这不是 HTTP、完整计划记录或真实跨设备验收。基线 `db7ba5c`。Lead 实现并复核；独立审查角色 `/root/foundation_review` 只读检查安全和测试边界，未自行重跑数据库测试。

- 列表从同一活跃会话及年龄声明事务派生主体，限定本人 `source=free` 记录。日期范围含端点，顺序为实际训练日及记录 ID 倒序；页面超出范围返回空数组，乘法前检查超大页码。
- 结果保持内部脱敏，未来 HTTP presenter 必须显式映射授权字段。列表查询与总数处于同一事务；用户行锁与当前训练写入约定一致。
- `python3 -B docs/evidence/A04/A04-06/mysql_smoke.py` 退出 0：私有、仅 Unix socket 的隔离 MySQL 中 `TestWorkoutListIsolationAndPagination` 通过，覆盖同主体两个会话、不同主体、实际完成与空记录、日期筛选、分页和超大页、无效输入、撤回年龄声明、无效会话及禁用主体；成功后仅删除本次数据目录。两个会话使用同一设备密钥，并不等于新设备批准与兑换的真实流程。

被测文件 SHA-256：

- `repository/list.go`：`97fb63cecfec83679be2e0f404a0c55e2f5e9aff25500c8af2b93938aef3394d`
- `application/list.go`：`8bac0efaf6715b8cf2c7e15461e343ce1b0fd1af309d2027e405009bfc29b5ef`
- `application/list_test.go`：`e49ef5576663be1fae6e0d9db12718eba86482a687f83202a9a7286f9ddd68dc`
- `mysql_smoke.py`：`8d11e4bfb3646f45cf2fb729bc26338876e6801c271440538fd38096fc7e4177`

独立审查未发现本实现 P0/P1 问题；现有 OpenAPI 列表不限定来源，而本实现只支持自由记录，接入完整列表端点前须处理计划记录。按页读取期间若其他事务新增记录，后续页可能移动；真实新设备登录、HTTP 接线、客户端与服务端端到端同步仍未验证。
