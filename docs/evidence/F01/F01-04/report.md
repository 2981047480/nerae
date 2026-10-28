# F01-04 进度证据（未接受）

- 状态：partial；本地测试环境脚本和迁移执行器正在实现，真实数据库验收未完成。
- 实际执行者：Codex Lead，`gpt-6.1-terra`，max；基线 `fbb9f91f431842f032e99b9a25c7ce46d466af4c`。
- 本地隔离：按 Git 工作树真实路径派生测试项目、数据库、Redis 和对象前缀；端口显式配置；随机密码保存在 Git 忽略的本地私有文件；不自动重置数据。
- 清理：默认 dry-run，执行须再次提供当前工作树的完整测试命名空间；限制本机 Docker context。此轮没有执行真实清理。
- 迁移：按清单扫描模块目录、校验序号和 SHA-256，记录 dirty 并拒绝不一致的历史；当前业务迁移清单为空，没有业务 DDL。DDL 隐式提交时需人工诊断与恢复，不承诺事务回滚。
- 测试镜像：Redis 7.4.12 摘要沿用 F01-01；MySQL 8.4.13 使用 `sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7`。2026-10-28 直接从 Docker Registry 拉取 `8.4` 与 `8.4.13` 的 manifest，校验正文 SHA-256 与响应摘要一致、两个标签一致，且包含 amd64/arm64 的 8.4.13 条目；见 [镜像清单](image-lock.json)。这替换了先前公开页面搜索结果中已过时的摘要；镜像未拉取或运行，不能视为供应链/漏洞审计或生产版本确认。F01-01 历史镜像记录不覆盖。MySQL 8.0 系列已结束维护，见 [官方发布说明](https://dev.mysql.com/doc/relnotes/mysql/8.0/en/)。
- 局限：本机未发现 Docker 服务，未验证 Compose 镜像启动、清理隔离及迁移失败后的完整恢复。测试版 Redis 无密码，仅绑定 loopback，禁止真实数据；对象前缀尚无对象存储支持。F01-04 保持 active，不能 accepted，后续依赖不能据此放行。
- 已执行（临时 stage）：`python3 -m unittest discover -s scripts/dev/tests -v`，13 项通过；其中两个真实临时 Git worktree 验证端口登记冲突和 Git 环境变量覆盖。其余测试对 Docker 与端口绑定使用替身，只验证控制逻辑。
- 已执行（临时 stage，API Go 模块依赖）：`go test -race -count=1 migrate.go migrate_test.go` 与 `go vet migrate.go migrate_test.go` 均退出 0；包含清单、路径、校验和、历史 dirty 与状态绑定的正反例。未连接数据库。
- 静态 YAML 可由 PyYAML 解析并检查两服务 host_ip；不是 `docker compose config` 或运行验证。此前对该工作树内隔离解释器的调用因该忽略目录不存在退出 127，后用系统 Python 已安装 PyYAML 完成静态解析。
- 同仓库 worktree 的端口登记使用共享 Git common dir 对应的本机锁；独立 clone 不共享登记，端口实际冲突由系统/Compose 报错。同仓库登记不等于 Compose 的端到端隔离已验证。
- 独立只读复核指出 Git 环境变量重定向和预登记端口冲突；修复后复核确认两个问题在同一 Git 仓库 worktree 范围内关闭。Compose 配置与清理范围仍待实测。

- 已在隔离工作树复跑 13 项 Python 测试并通过；`go run ../../scripts/dev/migrate.go check` 对当前空清单退出 0。

- 原生运行测试：临时下载 MySQL Community Server 8.4.13 macOS ARM 包，MD5 `73926da59774274b5cdfe78cd2150840` 与 [官方页面](https://dev.mysql.com/downloads/mysql/8.4.html?os=33) 一致；另外计算 SHA-256 `f324f8950e24d4147470bbc6a6ef0b90ef93b91d3fc74154b3d2ba085b44912a`（未验证 GPG 签名）。Redis 7.4.12 官方源码 SHA-256 `56588ee1648539556f2b6cf4e873e3734afbbe40fa952fec2fcd6b6d7dae2924` 与 [官方源码哈希](https://github.com/redis/redis-hashes) 一致，临时编译。两者都没有安装为系统服务。
- `scripts/dev/tests/mysql_native_smoke.py` 在两个临时 Git worktree 和私有 datadir 上同时启动两套 MySQL、Redis，验证各自端口/库/前缀、跨实例读写、空库初始化、同迁移重复执行，以及合成迁移部分执行失败后留下 dirty 并阻止重试；全部进程结束后已只读核对无遗留。该测试不验证 Compose 或其清理命令，也未声称业务数据迁移和失败恢复完成。
- 单独 `scripts/dev/tests/redis_native_smoke.py` 对两个原生 Redis 进程执行同名键和跨实例键隔离检查，退出 0。测试脚本在私有临时目录运行，数据为专用合成探针，不包含真实用户数据。

- 复核提醒：原生测试在 TCP 连接前用 `lsof` 校验监听进程属于本测试，检查与连接之间仍存在极短竞态；环境仅含随机测试凭据，不能等同连接原子绑定证明。单元测试与原生测试未覆盖 Docker 卷清理。

- 2026-10-29 新增仅限临时测试实例的冷备份恢复冒烟：两套真实 MySQL 8.4.13 与 Redis 7.4.12 同时运行，A 实例干净停机并备份后重启，合成 DDL 部分失败并被 dirty 防护阻止重试；再次停机、恢复测试备份后验证原测试行仍在、失败表消失、修复后的迁移成功，B 的迁移与表未受影响。`scripts/dev/tests/mysql_native_smoke.py` 最终退出 0；成功后脚本等待其子进程结束并清理本次测试目录。失败现场和工具链均未删除。
- 路径保护与初始化回归测试连同既有测试共 19 项通过。前两次真实运行在初始化阶段分别因 `initial_defaults` 与 `socket_path` 未定义退出 1，未进入恢复验证；已完整检查并修复该段，增加初始化回归测试并检查未定义全局名。两次私有失败现场分别保留于 `/private/tmp/nerae-mysql-native-smoke-bwb2d89k` 与 `/private/tmp/nerae-mysql-native-smoke-c5pdmnht`。
- 本次只证明合成测试数据的冷恢复路径，不是迁移器自带自动恢复，也不证明生产备份/恢复、Docker Compose 卷清理或对象存储隔离。端口归属检查与迁移 TCP 建连间仍有短暂竞态；脚本使用私有随机测试凭据和独立进程，不将这项检测写成绝对连接证明。F01-04 继续 active。
- 独立 reviewer `/root/contract_tests` 对最新脚本作只读复核并复跑 6 项恢复路径单测：未发现 P1；仍提示上述端口检查到 TCP 连接之间的 P2 竞态。真实冒烟由 Lead 执行，reviewer 未重复启动数据库。

- 2026-10-29 集成提交 `8fd2863035ea6273045b0da1acb3b0ba6233598a`：同一版本再次实际运行原生 MySQL/Redis 冒烟，退出 0，包含两实例隔离、迁移失败 dirty 阻断、冷备份恢复与修正后再次迁移；`scripts/dev/tests` 19 项通过，`scripts/plan.py check` 退出 0。冒烟脚本 SHA-256 `c8d4efa8d9cf7890447bf5ba01f03262ed2fcacc1ff96536e16cefd108836ce8`。独立 reviewer 的代码与 6 项恢复路径单测复核见上；没有把这次结果当作 Compose、对象存储或生产恢复验收。
