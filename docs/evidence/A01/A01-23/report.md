# A01-23 本机 TLS 集成证据

状态：**success（隔离合成联测）**。复核提交基线 `01e2a7098b619b1a4d51de21d396abdffa12b6f3`；本次仅新增本报告、HTTP 集成测试和私有服务 harness。

## 实际验证

从全新、私有目录启动 MySQL 8.4.13 与 Redis。MySQL 使用 `--skip-networking`，Redis 使用 `--port 0` 和 Unix socket；只创建 `nerae_a01_23_test`，应用本卡 harness 内列出的迁移。测试经 `httptest.NewTLSServer` 调用真实 `EmailLoginCoordinator`、登录 handler、`/me` handler、session store 与登录回执存储；邮箱和验证码均为本地合成值，没有 SMTP 或公网请求。

命令：

```sh
python3 -B docs/evidence/A01/A01-23/mysql_redis_http_smoke.py
```

退出码：`0`。脚本运行的定向 race 命令：

```sh
/private/tmp/nerae-go-toolchain-1.27.3/go/bin/go test -mod=readonly -race -count=1 -v \
  -run '^TestEmailLoginHTTPRealCoordinatorOverLocalTLS$' ./internal/modules/auth/transport
```

退出码：`0`。成功输出标记：`PASS: A01-23 real coordinator, login handler, /me handler and receipt replays over local TLS`。覆盖首次认证响应与 no-store、同键相同请求返回同一 token 且不增加剩余有效期、改变请求的同键冲突、错误验证码同键只记一次并留下拒绝回执、已认证 `/me`、禁用账号后 `/me` 与回执重试拒绝且不返回身份资料/令牌、新设备只获得待批准结果并可重放同一申请、未知请求字段返回 422，以及入口 IP 额度超限在数据库访问前返回 429。

成功 harness 在关闭本次创建的 MySQL/Redis 后移除了自身目录；未连接现有服务。保留的失败现场：

- `/private/tmp/nerae-a01-23-mysql-gbemp59u`：默认沙箱里的 MySQL 初始化发生 SIGSEGV；未启动 Redis。
- `/private/tmp/nerae-a01-23-mysql-7x3bczko`、`/private/tmp/nerae-a01-23-mysql-von4mcw4`：正常本机隔离服务已启动，最初测试错误要求新账号 `roles` 非空。OpenAPI 要求 `roles` 数组存在但不要求非空，邮箱身份创建也不自动授予角色；修正为验证数组字段存在后，最终完整 race harness 通过。以上失败目录均保留。

## 边界

这是本机合成数据、loopback TLS 的 handler/coordinator/数据库集成证据。它不验证真实邮件投递、外部 SMTP、不经公开路由的生产装配、真实账号/代理来源、手机或正式环境部署；不表示邮箱登录已对公网开放。
