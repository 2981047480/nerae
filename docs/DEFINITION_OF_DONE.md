# 统一完成定义与交付记录

版本 1.0 · 2026-10-23。task/integration 是可派发执行卡，work_package 是路线图/最终验收节点。执行卡不超过3有效人日；超过就拆卡并明确先后或互斥写域。工作包未细拆不进入ready，也不能直接派给agent。

## 1. 开始前

依赖accepted且review证据有效；任务包列基线、必读章节、白名单、交付物、实际验证与失败协议。F01-01由Lead先建立Git基线；其余开始时记录真实owner/model/reasoning/base_revision。外部缺资料允许诚实盘点，但不能通过核定卡。D19已明确授权D01逐项设计文稿、指定Harness执行的独立UI样机与Lead验收；正式U01–U07仍待工程及已接受设计基线。样机接受不等于业务应用或发布接受。

## 2. 七项完成清单

执行前标明适用范围，接受时把结果登记在任务JSON的completion对象。所有项均须回答；不适用只允许用于contract/privacy/recovery，并写明具体理由，经review确认。不能把缺验证写成不适用。

| key | 必须回答 | 结果 |
|---|---|---|
| scope | 实际改动仅白名单；共享变更有owner；基线与接受提交明确 | pass |
| verification | 与影响相称的正常/失败/边界/重复/回归检查已执行，测试能识别缺陷；记录命令/退出码/版本/验证提交 | pass |
| contract | 契约、生成物、消费者、枚举/错误映射同步；适用时有有效正反例 | pass或有理由的not_applicable |
| privacy | 对象权限/撤权/日志/导出等负例有效，敏感资料不进入通用记录 | pass或有理由的not_applicable |
| recovery | CAS/幂等/事务/队列/迁移/备份等适用失败恢复有实证 | pass或有理由的not_applicable |
| docs | 必要README/配置名称/错误码/限制/变更与恢复说明更新 | pass |
| review | reviewer检查diff和证据；剩余风险/取舍明确；接受理由真实 | pass |

每个pass引用至少一个登记的证据path；证据同时含SHA-256和revision，revision必须与accepted_revision一致。不适用写reason。文件存在/hash一致只证明对应文件未变，不证明内容正确；Lead仍须审查。仅编译通过、空测试集、只生成接口或未执行命令不能报功能完成。低影响文案/规划变更只跑相应检查，不强行补业务测试。

## 3. 报告格式

保存到执行卡的evidence_dir，通常为 docs/evidence/<工作包>/<卡ID>/report.md。记录 status=success/partial/blocked/failed、角色与实际模型/推理、输入/接受提交、交付与改动、命令/退出码、证据、用户/App责任是否保持、用到/排除的上下文、风险/取舍/需决策项。先完成worker报告，再review，最终Lead接受；作者success不会自动变成accepted。

任务JSON接受后需填写的结构示意（这里只是模板，不是已完成记录）：

```json
{
  "accepted_revision": "实际集成提交",
  "review_mode": "independent",
  "reviewed_by": "实际复核者",
  "evidence": [{"path": "任务白名单内证据路径", "sha256": "实际摘要", "revision": "实际集成提交"}],
  "completion": {"scope": {"status": "pass", "evidence": ["已登记路径"]}}
}
```

实际completion须填写全部七项。independent模式复核者不得与owner相同；工具无独立复核者时可用sequential_role_review并写review_reason，清楚标为同一代理顺序角色复核，不冒充独立意见。所有新证据均指向集成后提交，不用旧分支的绿灯代替。

## 4. 小批集成卡

F01-I/F02-I/I-A01/I-A02/I-A03/I-B01/I-B02为专门接线/复跑卡。至少登记integration_result：integrated_revision、covered_tasks、evidence。接受时：

1. covered_tasks包含该卡全部直接依赖，且依赖已accepted；记录它们的接受提交与本次集成提交。
2. 复跑使用同一集成版本；证据revision与accepted_revision一致，不仅引用worker各自日志。
3. 共享路由/命令/迁移顺序由本卡单owner串行更新；发现新业务/合同缺口另开卡并阻塞，不扩大本卡。
4. 对应HTTP/DB/恢复证据符合ENGINEERING_STANDARDS。小批集成不能代替I01/I02完整闭环、V01/V03真机和外部上线门槛。

## 5. 陌生agent上手试验

F02-01是首个指定试验卡。将任务包交给未参与方案的执行者；只有已获并行授权时才使用原生子代理，否则安排后续陌生执行者，不在本轮虚构试验结果。不预载全聊天或全部历史文档。

接受时提供onboarding_result：cold_start=true、extra_context_requests、rework_cycles、boundary_violations、verification_reproducible、evidence。记录每次额外上下文请求及原因，说明任务包如何修正。边界违规必须解决到0，验证可复跑；返工和补上下文次数先如实测量，不套用“最多一次”成功阈值。原始数量不因后续修正文档而改写；未解决项保留并阻止接受。

## 6. 工作包汇总

工作包只有breakdown=complete、全部子卡及外部接受前置通过、最终整体验收有证据才可accepted；不自动累计打绿。I01/I02当前只拆了早期API集成部分，最终客户端接缝仍需后续设计完成后再拆。partial/pending工作包不派发；每次只细化接下来可开工的少量卡。子卡估工仅是有效人日，不含审核/外部等待，不承诺日历。
