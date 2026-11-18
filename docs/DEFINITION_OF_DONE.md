# 统一完成定义与交付记录

版本 1.0 · 2026-10-23。task/integration 是可派发执行卡，work_package 是路线图/最终验收节点。执行卡不超过3有效人日；超过就拆卡并明确先后或互斥写域。工作包未细拆不进入ready，也不能直接派给agent。

## 1. 开始前

依赖accepted且review证据有效；任务包列基线、必读章节、白名单、交付物、实际验证与失败协议。F01-01核对已有Git基线、未提交差异及工具链；其余开始时记录真实owner/model/reasoning/base_revision。外部缺资料允许诚实盘点，但不能通过核定卡。用户已授权 Lead 直接完成 R04/R05 样机，当前 D02 负责第五轮三端实施规格；历史 D01 接受不等于 R05、正式业务界面或发布接受。

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

每个pass引用至少一个登记的证据path；证据同时含SHA-256和revision，revision必须与accepted_revision一致，表示被验证的集成版本。不适用写reason。检查器从Git中核验对应版本的普通文件blob，而不是将历史证据误绑定到以后会继续变动的工作树文件；没有可核验的Git历史时检查失败。少数报告在验证后才归档：须另登记实际`source_revision`，证明归档提交位于接受提交之后且属于当前集成历史；此字段不改变报告所验证的版本，也不代替复核。原第三轮D01报告的真实归档提交是`e4610a7e69fb926782200a1c89373b3ba427fc93`，历史接受版本保持原值。摘要与提交关系只能证明文件来源和字节，不能证明内容或验收结论正确；Lead仍须审查，新一轮集成仍须在其实际版本复跑。仅编译通过、空测试集、只生成接口或未执行命令不能报功能完成。低影响文案/规划变更只跑相应检查，不强行补业务测试。

涉及自有 UI 的设计和实现必须按[白灰黑色彩系统](design/MONOCHROME_COLOR_SYSTEM.md)验收：白底、灰黑主次层次及浅装饰线符合已确认基准；关键操作和状态不能仅依赖浅线或灰度，相关文字与必要图形对比、可访问状态及本任务适用平台的实际截图须有证据。首页「最近记录」的动作图与名称之间不得恢复竖线。相关检查未通过或缺少证据时，不得将该 UI 范围或三端视觉验收标为通过；已有旧色样机不自动通过。

## 3. 报告格式

保存到执行卡的evidence_dir，通常为 docs/evidence/<工作包>/<卡ID>/report.md。记录 status=success/partial/blocked/failed、角色与实际模型/推理、输入/接受提交、交付与改动、命令/退出码、证据、用户/App责任是否保持、用到/排除的上下文、风险/取舍/需决策项。先完成worker报告，再review，最终Lead接受；作者success不会自动变成accepted。

任务JSON接受后需填写的结构示意（这里只是模板，不是已完成记录）：

```json
{
  "accepted_revision": "实际集成提交",
  "review_mode": "independent",
  "reviewed_by": "实际复核者",
  "evidence": [{"path": "任务白名单内证据路径", "sha256": "实际摘要", "revision": "实际集成提交", "source_revision": "如后续归档则填实际归档提交，否则可省略"}],
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

Q01-01 独立验证 F02-01 任务包。真实陌生执行者可用时，在隔离临时环境复跑；只有已获并行授权时才使用原生子代理。不预载全聊天或全部历史文档，也不在本轮虚构试验结果。Q01-01 的接受结果不作为 F02-01 或业务开发的前置。

接受时提供onboarding_result：cold_start=true、extra_context_requests、rework_cycles、boundary_violations、verification_reproducible、evidence。记录每次额外上下文请求及原因，说明任务包如何修正。边界违规必须解决到0，验证可复跑；返工和补上下文次数先如实测量，不套用“最多一次”成功阈值。原始数量不因后续修正文档而改写；未解决项保留并阻止接受。

## 6. 工作包汇总

工作包只有breakdown=complete、全部子卡及外部接受前置通过、最终整体验收有证据才可accepted；不自动累计打绿。I01/I02当前只拆了早期API集成部分，最终客户端接缝仍需后续设计完成后再拆。partial/pending工作包不派发；每次只细化接下来可开工的少量卡。子卡估工仅是有效人日，不含审核/外部等待，不承诺日历。
