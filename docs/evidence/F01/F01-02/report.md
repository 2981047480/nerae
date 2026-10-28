# F01-02 · Go 进程与健康检查

状态：success；服务进程骨架与本卡范围的测试已完成，任务接受状态和版本以 `docs/tasks/tasks.json` 为准。真实健康数据库、实际后台任务和业务仍待后续验证。Lead：`gpt-6.1-terra` / `max`；只读源码复核由 `gpt-6-luna` / `medium` 完成。基线 `c885515b58961e8738df2d63d5d6387d88e4b8eb`；任务状态由 Lead 在集成工作树登记。

交付：Go/Gin API 进程、显式环境配置及错误脱敏；liveness 不依赖外部服务，readiness 实际 ping MySQL 与 Redis，失败/超时返回 503；SIGTERM 下关闭 HTTP 和客户端。worker 能校验配置并等待信号，但尚无 job handler、不会消费队列，不能当成真实后台任务能力。新增配置、健康检查单元测试和本机进程 smoke 脚本。

## 实测

- Go 1.27.3：`go mod tidy` 在官方校验和开启的情况下完成，固定 Gin 1.10.0、MySQL driver 1.10.1、go-redis 9.23.0、Viper 1.21.0 及传递依赖；网络曾因 EOF 失败，复用已有校验缓存并补齐缺失依赖后成功。依赖尚未做安全审计。
- `go build -mod=readonly ./...`、`go test -mod=readonly -count=1 ./...`、`go test -mod=readonly -race -count=1 ./...`、`go vet -mod=readonly ./...` 均退出 0；测试使用确定性探针函数覆盖 readiness 分支，并不是健康数据库集成测试。
- `python3 docs/evidence/F01/F01-02/smoke.py --api <临时构建的API二进制> --worker <临时构建的worker二进制>` 在本机回环保留的关闭端口执行：API 两次启动均 liveness 200、readiness 503、SIGTERM 退出 0；worker 明示未注册任务并在 SIGTERM 后退出 0；缺 MySQL 配置非零退出且测试凭据未出现在日志。
- `python3 docs/evidence/F01/F01-02/drain_smoke.py --api <临时构建的API二进制>` 退出 0：用本机故障注入连接阻塞依赖，在退出超时内等待请求完成；另使请求超过关闭期限，验证连接被关闭且进程明确报错。该故障注入不模拟健康数据库。
- 本机回环绑定在默认沙箱被拒绝，获得受控审批后执行。smoke 首次以固定 0.3 秒等待 worker，因进程尚未报告启动就被 SIGTERM 导致退出 -15；改为等待明确启动日志后重跑成功。该失败属于测试时序，未伪称首次通过。

## 未完成

本机没有可用的真实 MySQL/Redis 环境，尚未验证健康依赖时 readiness 200、数据库权限、迁移或实际队列；后续 F01-04 等卡必须使用隔离环境执行。API 没有业务接口或登录，worker 未处理任何任务；这不是正式应用、HTTP 业务联调、真机或上线验收。此报告的工作树证据仍须在集成提交上复跑并由 Lead 审阅。

集成版本 `ffa7398485281b68b8b544600549cef5b5db888b` 的干净归档已重跑 Go build、race 测试、vet、API/worker 构建和本机进程 smoke；随后追加的请求进行中退出测试也已通过。正式接受提交须包含此记录与关闭测试，并在同版复跑。
