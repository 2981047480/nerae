# F01-01 · 工具链探针报告

- 状态：partial；本轮尚无接受提交及正式复核，不能将任务标记 accepted。
- 基线：`e4610a7e69fb926782200a1c89373b3ba427fc93`；开始时存在 152 个非忽略的修改或未跟踪文件，逐文件哈希见同目录 `preflight.json`。哈希用于核对，不是备份或提交。
- 执行：上轮由 Lead 执行，实际模型及推理配置未单独登记；本轮由 Lead `gpt-6.1-terra`、`max` 继续，`gpt-6-luna`、`medium` 仅作只读复核。
- 变更：根 Node/Go 版本、pnpm 工作区及依赖锁，工具链说明，以及此目录的探针与证据。本任务没有进一步修改 `.gitignore`；预检时该文件已有未提交变化：相较 HEAD，`node_modules/` 已改为更宽的 `node_modules`，因此同名目录及其他同名路径均会被忽略。该差异属于预存工作，不声称由本任务引入或还原。

## 已执行

| 检查 | 结果 |
|---|---|
| `git check-ignore -v node_modules/ .review/ api/tests/.venv-py311/ .env.local` | 退出 0，四类路径有忽略规则 |
| Go 官方下载包 SHA-256 | 与 Go 发行信息一致；临时解压的 `go version` 为 1.27.3 |
| 临时 Go 探针 `go test ./...` | 退出 0；只验证编译工具链 |
| 临时 uni-app 探针 `pnpm install --ignore-scripts` | 退出 0；修正 Vue 与类型包的 peer 版本后继续 |
| 临时探针 `pnpm peers check` | 退出 0，无 peer 依赖冲突 |
| 临时探针 `pnpm run build:h5`、`build:mp-weixin`、`build:app` | 三项各退出 0；产生对应资源，无真实 AppID |
| 根目录 `pnpm install --offline --frozen-lockfile --store-dir /private/tmp/nerae-pnpm-store --ignore-scripts` | 退出 0，复用已填充 store，未重新解析 |
| 根目录 `pnpm peers check` | 退出 0，无 peer 依赖冲突 |
| `python3.11 scripts/plan.py check` | 退出 0；没有变更任何任务接受状态 |
| `python3.11 -m unittest discover -s scripts/tests -q` | 退出 0，35 项规划测试通过 |
| `api/tests/run_contract_tests.py --summary` | 复用原项目 Python 3.11 虚拟环境执行；退出 0，72 项静态契约测试通过 |
| 上轮预检哈希复核 | 当时原有 152 个修改或未跟踪文件均与预检时相同 |

## 2026-10-28 续作复核

- 从上轮临时探针找回原 Go 测试源码并保存至 `go-probe/`；复制本目录的两个探针到独立临时目录，使用独立 Go 编译缓存重跑 `go test ./...`，退出 0。
- uni-app 探针源码和锁文件在独立临时目录用现有 pnpm store 执行 `pnpm install --offline --frozen-lockfile --ignore-scripts`，退出 0；`pnpm peers check` 与 H5、微信小程序、App 资源三项构建各退出 0。该过程不是从 Git 接受提交的干净检出，不能替代 F01-I。
- 从 `git archive HEAD` 建立独立候选源码副本，只覆盖 F01 本轮的 19 个候选文件，再离线冻结安装根依赖、检查 peer 依赖并对其中 Go 探针执行 `go test ./...`，均退出 0；候选文件内容已逐项与工作树核对。该副本是基于 HEAD 加候选文件的源码归档，不是 Git 接受提交或真正干净检出；pnpm store 和 Go 工具依然来自本机临时目录。
- 本轮任务登记和规格/上下文维护后，预检 152 项中有 7 项由 Lead 按用户最新授权主动更新：`AGENTS.md`、`docs/CURRENT_STATE.md`、`docs/IMPLEMENTATION_PLAN.md`、`docs/UI_SPEC.md`、`docs/design/round-04/INTEGRATION.md`、`docs/tasks/README.md`、`docs/tasks/tasks.json`；其余 145 项哈希未变。复核时 `/Users/zephyr/Documents/项目/nerae` 中的 152 项仍与预检哈希一致；该目录是另一个可核对的现有副本，不是不可变备份。若将来需要恢复，应先根据 `preflight.json` 找到具体相对路径并重新核对源文件 SHA-256，再单独检查目标是否包含之后的新工作；不得直接覆盖、重置或批量恢复。

最初 npm 查询在沙箱内因连接本机代理 `EPERM` 失败，获准在沙箱外查询后成功；误查询 `@dcloudio/create-uni` 返回 404，随后改用 DCloud 官方 `uni-preset-vue` 的 Vue 3 模板。安装提示 `vue-i18n@9.14.5`、`phin@2.9.3` 和 `phin@3.7.1` 已弃用；本轮没有依赖安全审计。

## 尚未完成

Go 仅在临时目录，尚无持久安装或干净环境复现；原生安装包、真机及实际业务不在探针结果内。现有 152 个未提交文件必须保留；需要有与真实集成提交一致的证据和复核后才能接受 F01-01。临时探针安装禁用依赖生命周期脚本；后续应用如需脚本须单独评估并验证。
