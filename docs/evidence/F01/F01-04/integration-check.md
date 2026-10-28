# F01-04 本地集成复测（未接受）

- 复测代码提交：`c3ed9db3ea7b4a9b01f230f951e1d5f43d5fb80f`；本文件仅记录该候选版本的事实，不是接受证据。
- `/Users/zephyr/.local/bin/python3.11 -m unittest discover -s scripts/dev/tests`：13 项通过，退出 0。
- Go 1.27.3，使用 API 模块锁定依赖，`go test -race -count=1 ../../scripts/dev/migrate.go ../../scripts/dev/migrate_test.go`：退出 0。
- `python3.11 scripts/plan.py check`：退出 0，10 个 accepted；F01-04 仍 active，ready 只有 P02-02、P02-03。
- `scripts/dev/tests/mysql_native_smoke.py`：临时原生 MySQL 8.4.13 与 Redis 7.4.12 各两进程同时运行，退出 0；验证两个临时 Git worktree 的端口、测试库与 Redis 前缀，交叉读写，空库迁移、重跑和合成迁移部分失败后 dirty 阻断。脚本校验 MySQL 端口属于自启进程并在结束时停止子进程。源包与固定镜像详情见 [report.md](report.md)。
- 未验证：Docker Compose 配置及镜像启动、实际卷清理与另一工作区保留、迁移失败后的完整恢复、对象存储隔离和真实业务迁移。此测试使用合成测试表，没有真实用户数据。Docker 未安装；F01-04 不能接受，后续依赖不得提前放行。
