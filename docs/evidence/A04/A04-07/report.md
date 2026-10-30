# A04-07 本人训练 HTTP 读取：本轮证据

状态：自由记录列表与详情处理器已在隔离测试路由运行，任务仍为 active；未挂接现有生产进程，不能据此宣称真实登录、线上服务或跨设备同步完成。基线 `8ea6abd`，后同步任务范围提交 `1edf881`。Lead 实现并复核；独立审查角色 `/root/foundation_review` 只读审查两次，没有自行重跑测试。

- 处理器复用严格 Bearer 解析及同事务会话、主体、年龄检查；训练详情中不存在与非本人记录均为 404，实际数据库故障为 503。服务端时间不推断训练完成时间，动作条目顺序、稀疏组序号和十进制原值由授权读取结果映射，并按 OpenAPI 校验后返回；响应禁止缓存。
- `python3 -B docs/evidence/A04/A04-07/mysql_smoke.py`：退出 0。使用新建的私有 Unix socket MySQL、合成用户及本机 `httptest.NewTLSServer`，通过 `TestWorkoutHTTPRead` 和 `TestWorkoutHTTPConstructorRejectsMissingDependencies`；本次临时数据库在成功后删除。
- 同一测试启动 Node 真实 `RecordingApi` 与 `restoreConfirmedSession`，明确把临时服务器证书加入信任并开启 TLS 验证，使用真实测试 JWT 读取本人列表和详情并恢复动作、组和原事件标识；另一主体读到空列表且详情返回 404。客户端认证适配器只供应合成测试会话，不是正式登录模块，也未运行原生 App 或小程序。
- 正反例包括空列表、同主体不同会话、其他主体与不存在记录的统一 404、数据库关闭后的 503、未登录、年龄声明撤回、禁用账号、重复分页参数、非法日期与 ID；第二个同主体会话使用同一测试设备密钥，并非真实新设备批准及兑换。

被测文件 SHA-256：

- `application/visible_read.go`：`dd5c81643c0fe343c77da0b9a5a9498e3a67c98c83a1fc7490522babb96e153b`
- `transport/read.go`：`263a657100b1160fdfc5450ae9c7e2e3d84e9f67d2d51509cee99e69f1c47bb1`
- `transport/read_test.go`：`8ad0c82ce470597081844a44452698a475fe749b00d74c741900cc097728bb50`
- `mysql_smoke.py`：`033fb42c5b0fb3e53ca6efa112f4ceaaed068a663448021a458ec972f77bf414`
- `client_interop.mjs`：`cbaaa132f8ab6244dc3cc9fdb2e15fdbfcba06ac2a287ce0823a4c932ee209bb`

首次审查发现不可见记录被误报为 503；现已在同一鉴权事务中区分不可见与数据库故障，并补充测试。定向复审未发现新的阻断项。计划训练来源、生产服务装配、真实邮箱发信和原生密钥、限流、写入幂等、实际设备批准及服务端 HTTPS 地址仍未完成或验证。
