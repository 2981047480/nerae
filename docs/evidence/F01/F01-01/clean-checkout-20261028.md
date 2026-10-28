# F01-01 · 干净检出复跑与独立复核

范围：只验证 F01-01 的 Git 保护、固定工具链和可复现编译探针；不验证业务服务、原生安装包、数据库、微信开发者工具、签名或真机。原工作区开始时的 152 项预存差异及其哈希以 `preflight.json` 为准；对应文件未由本卡删除。真实任务初始基线为 `e4610a7e69fb926782200a1c89373b3ba427fc93` 加当时既有未提交工作。

本地快照 `e3de84312ceabb11202dbd723972d82d4c25bbda` 保存了当时全部非忽略工作文件，不是 F01 已接受提交。新工作树从该快照检出，起始 `git status --short` 为空；快照的 182 个文件与 Git 对象逐项核对 SHA-256 和大小，详见 `workspace-snapshot-provenance.json`：145 项预存文件内容未变、7 项预存文件由 Lead 按当前工作更新、19 项是 F01-01 新文件、11 项属于 K01/D02/L01 等其他已授权工作。初始 152 项均在快照中；其他工作不作为本卡完成。

复核角色：`/root/foundation_review`，`gpt-6-luna`、`medium`，仅作独立、只读的确定性复跑与范围检查；Lead `gpt-6.1-terra`、`max` 复核结论并负责接受。复核前后候选工作树干净，HEAD 均为上述快照。

| 检查 | 实际结果 |
|---|---|
| `node --version`、`pnpm --version`、临时 Go 的 `go version` | Node 26.0.0、pnpm 11.5.3、Go 1.27.3 darwin/arm64 |
| 从候选工作树复制 `probe/` 到隔离临时目录，执行 `pnpm install --offline --frozen-lockfile --ignore-scripts --store-dir /private/tmp/nerae-pnpm-store` | 退出 0；从已填充 store 复用依赖，不证明外网可达 |
| 同一探针 `pnpm peers check` | 退出 0，无 peer 冲突 |
| 同一探针 `pnpm run build:h5`、`build:mp-weixin`、`build:app` | 各退出 0；资源编译不等于原生安装或真机验收 |
| 从候选工作树复制 `go-probe/` 到隔离临时目录，以独立 GOCACHE 和 Go 1.27.3 执行 `go test ./...` | 退出 0，`ok example.invalid/nerae-probe`；只验证编译工具链 |
| 临时副本中将 Vue 版本改为 3.4.20、保留原 lock，再执行冻结离线安装 | 依赖安装按预期退出 1，并报告 `ERR_PNPM_OUTDATED_LOCKFILE`；验证锁文件漂移被拒绝，原工作树未修改 |
| `git check-ignore -v node_modules/ .review/ api/tests/.venv-py311/ .env.local` | 原 F01 探针报告记录退出 0，四项被忽略；现有 `.gitignore` 的更宽 `node_modules` 规则是预存变化，不能当作本卡新改动 |

依赖锁和实际安装来自本机版本及临时缓存。uni-app 探针使用 `--ignore-scripts`，提示 Vue i18n 和 phin 依赖弃用，尚未完成依赖安全审计；Go 发行包当前仅安装在临时目录。F01-02 至 F01-05、F01-I 及后续三端真机/上线门槛仍须独立完成，不能由本报告放行。
