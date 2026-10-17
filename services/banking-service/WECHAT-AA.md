# 微信 AA：明细核对与人工收款记录（API 0.22.0）

复用已有 AA 创建、邀请、认领、列表和版本接口，保留模拟通道。新增 `payment_method=wechat_manual`，不调用微信商户 API，不自动发起真实转账、不接收官方支付回执。付款人在微信自行付款后自报，发起人核对微信记录后确认收到；全程 `platform_verified=false`。

## 单人管理（新建默认）

新建微信账单不传 management_mode 时默认 single_manager。只需发起人一个账号，参与人姓名只是本单称呼，不创建用户、不需要注册或认领。发起人分享分摊明细，朋友在微信付款；发起人用 purpose=confirm_received 执行 review→核对逐项明细和实际到账→confirm，直接 awaiting_receipt→receipt_confirmed。记录明确属于发起人核对，不产生付款人自报，reported_at 保持0、位置user_id保持0。实际资金仍在微信，platform_verified=false。

单人单没有邀请码，preview/join拒绝；收款确认只允许账单发起人。取消后未收款位置不再接受确认，历史确认保留，不代表退款。管理方式创建后不可切换，避免改变归属和确认语义。

需要多人独立登录时显式传 management_mode=collaborative，沿用下述加入、自报和独立收款确认。旧持久化账单缺省字段仍为collaborative，不会被自动转成单人。模拟账户AA仅允许collaborative。新建默认变化不意味着可将旧账单重放成另一种模式，幂等键仍绑定原始请求。

首次更新前尚未提交的核对，建议关闭重开：新摘要纳入管理模式，旧短期核对可能需要重新申请。API字段及enum以最新OpenAPI为准。

## 明细与状态

创建微信账单不需要模拟账户，`account_id` 必须省略或为字符串 `0`；提供 `recipient_hint`、`items` 和可选 `memo`。明细最多30项，名称非空、金额为正整数分，合计必须严格等于总金额；总额与分摊限制沿用 AA。创建后收款提示、明细、份额、渠道不可修改，改动需要新建账单。

多人协作参与状态：`unclaimed → joined → payment_reported → receipt_confirmed`。付款自报不计入 collected_minor；只有发起人确认后计入“人工确认已收”，不代表微信验证，也不写入模拟账户、交易或账本。全部其他份额均确认后账单 settled。原模拟通道仍使用 joined→paid 与独立网关授权，不能用 aa_payment 支付微信账单。

终止账单阻止新的付款自报；对终止前已有的 payment_reported，仍可核对收款。不撤销、退回或阻止微信里的实际交易。当前未实现付款争议、退款、部分到账或撤回自报，不能据此自动认定实际债务已清偿。

## 新增 API

全部要求本人 UserBearer；Agent/Tool 不得调用。owner 与 id 取自已加入的账单，不能由客户端改成另一个收款对象。

| 方法与路径 | 请求 | 响应 |
| --- | --- | --- |
| POST `/api/v1/aa/wechat/{owner}/{id}/review` | participant_id、purpose、expected_version | AAManualReview，含完整账单、intent_id、digest、nonce、expires_at |
| POST `/api/v1/aa/wechat/{owner}/{id}/confirm` | 上述字段及 intent_id、digest、nonce | 最新 AABillView |

多人协作中，purpose=report_paid 仅允许位置绑定的付款人；purpose=confirm_received 仅允许发起人，且必须先有付款自报；单人管理则仅发起人可对 awaiting_receipt 确认收款，无需付款自报。review 返回的 bill.version 是 confirm 使用的 expected_version，不用旧列表版本。15分钟有效，确认绑定身份、用途、账单、本人份额、金额、收款提示、备注和消费明细；简单 confirmed=true 会被拒绝。版本冲突、过期和重复确认均不执行状态变更，重新查询账单后核对；HTTP 超时先 GET 查询，不重复微信转账。

核对过程也是持久化写入，会增加账单版本。并发确认按既有用户 ID 顺序锁定，最多成功一次；版本变化要求重新核对。nonce 只返回给本次合法核对用户，不放在共享列表、URL、通用日志或复制给微信的内容中；消费后清除。审计和 portfolio 写入处于同一事务，任一失败均回滚。

## curl 示例

以下变量必须来自实际登录和 API 响应。请求文件不纳入版本管理，不共享令牌、私密邀请码或确认 nonce。

```bash
curl -X POST "$BASE/api/v1/aa/bills" \
 -H "Authorization: Bearer $OWNER_TOKEN" -H 'Content-Type: application/json' \
 -H "Idempotency-Key: $CREATE_KEY" \
 -d '{"title":"聚餐","payment_method":"wechat_manual","recipient_hint":"小林，请在微信核实","memo":"周末聚餐AA","items":[{"name":"餐费","amount_minor":8000},{"name":"交通","amount_minor":2000}],"total_minor":10000,"mode":"equal","already_paid":true,"owner_share_minor":0,"participants":[{"name":"小陈","amount_minor":0}]}'
```

参与人先通过原有 preview/join 加入，再把实际 participant_id、purpose=report_paid、最新 expected_version 保存到 review.json：

```bash
curl -X POST "$BASE/api/v1/aa/wechat/$OWNER_ID/$BILL_ID/review" \
 -H "Authorization: Bearer $PAYER_TOKEN" -H 'Content-Type: application/json' --data-binary @review.json
```

用户逐项核对后在微信手动付款。使用该响应的 participant_id、purpose、intent_id、digest、nonce，以及 bill.version 作为 expected_version，保存 confirm.json：

```bash
curl -X POST "$BASE/api/v1/aa/wechat/$OWNER_ID/$BILL_ID/confirm" \
 -H "Authorization: Bearer $PAYER_TOKEN" -H 'Content-Type: application/json' --data-binary @confirm.json
```

发起人读取最新账单，以本人 OWNER_TOKEN、purpose=confirm_received 重复 review→逐项核对微信到账→confirm。付款人不能代收款人确认。

## 数据、部署与安卓

复用 migration 016 的 `banking_users.aa_portfolio`，兼容空/旧 simulation 字段；不新增表、余额字段或 SQL 迁移，既有迁移仍为017。新 JSON 状态写入后不能直接回退不认识这些状态的旧服务镜像，应保留数据并做向前修复。

在当前完整 Compose 链末尾添加 `deploy/banking/compose.wechat-aa.yaml`，网页链末尾添加 `deploy/banking-web/compose.wechat-aa.yaml`。镜像为 `qingyingjin-banking-service:wechat-aa`、`qingrongjin-banking-web:wechat-aa`。

安卓 1.2.1 保留原生离线核心，AA 页新增联网工作区，复用已有 WebView 外壳、服务器白名单策略及同源登录；默认不嵌入服务器地址。跨手机 AA 需要同一可访问服务、发起人的服务器账号（单人管理），或各自账号（多人协作）；本机档案不会上传。HTTPS 用于远程服务，私有IPv4 HTTP仅供显式确认的局域网演示。网页和安卓都需要用户自行打开微信选人、填金额、确认付款。

本机实际结果与限制见 [验收记录](../../docs/hand-off/wechat-aa-validation.md)。

单人管理本轮验收见 [单人管理交付](../../docs/hand-off/single-manager-aa-validation.md)。
