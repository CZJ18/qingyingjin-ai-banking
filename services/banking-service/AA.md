# 模拟 AA 分摊转账

新增微信线下结算见 [WECHAT-AA.md](WECHAT-AA.md)：逐项消费明细、付款人自报和发起人确认收款，与本文模拟资金通道隔离，不代表微信平台核验。

网页入口：「可信执行与安全」`/security` 页面中的 **AA分摊转账**。需要多个独立登录用户参与；本人不能以不同账户代替其他参与人确认。

## 已实现流程

1. 发起人声明已垫付，选择本人收款账户、账单名称、总金额及其他参与人称呼。支持包含本人在内的均分或自定义整数分金额。
2. 服务端计算分摊并持久化，发起人私下分享邀请码。创建只记录应收，不导入消费、不扣款、不增加余额。
3. 参与人登录，粘贴邀请码预览，认领自己的位置；每用户每账单只能认领一个位置，已认领位置不可被其他用户占用。邀请码只用于加入，不是付款授权或真实身份核验。
4. 参与人选择本人的模拟账户，点击「确认本人的付款」，核对服务器意向的账单、本人位置、金额及双方账户，勾选确认后授权执行。
5. Go 网关重新检查身份、归属、账单状态、未付份额、必要生活预留、余额、账户状态和金额限制。成功后同时入账双方交易及双分录、更新付款进度。
6. 发起人可终止剩余未付部分，已经付款保留，不自动退款。终止后即使旧付款意向已授权，也不能再付款。

例如 360 元四人均分，每人90元，发起人应收270元。实际参与人付款前，可用余额不增加。100分三人均分为34、33、33分，余数按发起人及输入顺序逐分分配。自定义分摊必须合计等于总金额，其他参与人金额必须大于0。

## 金额、安全和一致性

- 全部金额是整数分，支持 CNY；账单2–20人，总金额2–1,000,000,000,000分，执行还受已有金额风控上限约束。前端仅做元输入转分，不用 LLM 计算分摊或金额。
- 收款账户和分摊创建后不可修改。`aa_payment` 参数只有 owner_id、bill_id、participant_id、source_account_id；金额、收款账户来自持久化账单，不接受调用方指定 amount 或 target。
- AA 接口与付款准备拒绝 Agent；授权、执行复用现有用户 JWT、摘要、一次性 nonce、15分钟内意向有效期及审计。用户不能代其他参与人付款，邀请码不能直接扣款。
- 参与人仅能查看被邀请账单信息；其他人的用户ID、账户、余额和操作意向ID不会出现在共享账单视图。邀请码仅返回发起人，不进入 URL、请求日志或验收截图。邀请内容应仅分享给预期参与人。
- 付款人和发起人的用户行按ID排序加锁，再锁定双方账户。MySQL REPEATABLE READ 下，在事务外读取不可变意向以确定锁集合；财务事务内先获取用户锁，再读取账单，避免旧快照导致重复付款或进度覆盖。
- 资金只通过现有 Repository.Post 更新余额。每个用户分别使用独立UUID的两分录清算账本，同一事务提交双方交易、四条分录、份额已付状态、授权消费及审计。两个journal通过同一个 operation_id、bill_id、participant_id关联。任何一端SQL失败整体回滚，不保留半笔扣款。
- 同一操作执行重试返回原结果；不同意向同时支付同一份额也只能成功一次，其他意向失败且不扣款。取消、冻结、余额不足和必要预留不满足均阻止执行。
- AA结算交易记为 internal_transfer/aa_settlement，避免与已垫付消费重复计入消费分析；创建AA账单本身不代表原始消费流水已导入。

## 持久化和迁移

迁移016在 banking_users 增加 `aa_portfolio`，MySQL MEDIUMTEXT、PostgreSQL TEXT，NULL兼容旧用户。JSON聚合保存本人账单、参与位置和加入引用；每用户最多50个发起账单、100个加入引用，每单最多20人，达到上限明确返回 STATE_CONFLICT。通过单一用户列复用现有46业务表存储写围栏、快照和用户行锁，不增加第二个余额源。

状态：账单 open → settled（全部其他参与人已付）或 cancelled（停止剩余未付）；参与位置 self / unclaimed → joined → paid。已付款只追加操作ID和时间，不回退、不因终止自动退款。列表每页最多50条，offset至150。当前没有归档清理功能，历史占用容量；大规模使用需要独立关系表和迁移围栏升级，本阶段不擅自改变既有快照协议。

## API（Go OpenAPI 0.19.0）

全部使用本人 UserBearer，返回现有 Envelope。ID字段为字符串，金额为整数分，时间为Unix毫秒。没有新增Coze工具或AI执行权限。

| 方法 | 路径 | 功能 |
| --- | --- | --- |
| GET | /api/v1/aa/bills?limit=50&offset=0 | 本人发起及加入的账单 |
| POST | /api/v1/aa/bills | 创建账单，必需Idempotency-Key |
| GET | /api/v1/aa/bills/{owner}/{id} | 只有发起人/成员可读 |
| POST | /api/v1/aa/invitations/preview | 邀请码预览；不认领、不授权 |
| POST | /api/v1/aa/join | 邀请码＋participant_id认领位置 |
| POST | /api/v1/aa/bills/{id}/cancel | 发起人终止未付，expected_version校验 |
| POST | /api/v1/execution/operations | kind=aa_payment，创建本人付款意向 |

后续 authorize 和 execute 沿用原网关接口。常见错误 INVALID_ARGUMENT=400、UNAUTHENTICATED=401、FORBIDDEN=403、NOT_FOUND=404、STATE_CONFLICT/VERSION_CONFLICT/IDEMPOTENCY_CONFLICT=409；余额不足、冻结、必要预留等在执行记录体现为failed及error_code，不能只看HTTP 200判断成功。

curl 示例（TOKEN 为临时用户令牌；参与人与发起人使用各自令牌，变量值取自实际响应；不向聊天发送令牌或邀请码）：

```bash
curl -X POST http://127.0.0.1:8088/api/v1/aa/bills \
  -H "Authorization: Bearer $OWNER_TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $CREATE_KEY" \
  -d '{"title":"聚餐","total_minor":36000,"mode":"equal","account_id":"1","already_paid":true,"owner_share_minor":0,"participants":[{"name":"小林","amount_minor":0},{"name":"小陈","amount_minor":0},{"name":"小周","amount_minor":0}]}'
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8088/api/v1/aa/bills
curl -X POST http://127.0.0.1:8088/api/v1/aa/invitations/preview \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data-binary @invitation.json
curl -X POST http://127.0.0.1:8088/api/v1/aa/join \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data-binary @join.json
curl -X POST http://127.0.0.1:8088/api/v1/execution/operations \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $PAY_KEY" --data-binary @payment.json
```

invitation.json结构为 `{"invitation":"实际私密邀请码"}`，join.json再增加实际participant_id。payment.json为 `{"kind":"aa_payment","parameters":{"owner_id":"发起人ID","bill_id":"实际UUID","participant_id":"本人位置UUID","source_account_id":"本人账户ID"}}`。付款人接着核对意向，提交其 intent_digest/confirmation_nonce 到 authorize，再用返回的 authorization_id/intent_digest/expected_version 调用 execute。

## 部署及限制

当前本机主金融库PostgreSQL已015→016，旧MySQL历史库继续冻结。新Go/Web镜像标签为 aa-bills，原Compose链末尾分别追加 deploy/banking/compose.aa.yaml 和 deploy/banking-web/compose.aa.yaml。复用原环境变量，不需要新的模型、短信或支付密钥。

没有接入真实银行、微信、支付宝，没有开发自然语言AA编译、群聊催款、实际垫付交易核验或自动退款。已垫付是用户声明；邀请码是本地协作邀请，不能当作实名身份认证。实际验收和修改文件清单见 docs/hand-off/aa-bills-validation.md。
