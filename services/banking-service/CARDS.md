# 模拟银行卡管理

本模块增加独立的银行卡实体、持久化查询、绑定/解绑、借记卡/信用卡类型、发卡行、四位模拟尾号、默认卡和模拟信用额度展示。页面入口：安全中心 `/security` → **我的银行卡**。原“模拟账户管理”保留，用于管理卡片关联的资金账户。

卡片不是账户、信用额度不是余额。绑定不增加资金；解绑不关闭账户、不改变交易或账本；默认卡只是管理偏好，不产生自动扣款授权。本阶段未开发信用卡消费、账单、还款、真实银行绑定、真实商户扣款或短信发卡验证。

## 持久化与权限

- `internal/cards` 包提供Card实体、严格输入规则、状态转换、独立Repository。最多20条/用户，包含解绑历史。
- 迁移014在 `banking_users` 增加可空 `card_portfolio TEXT`。卡片为随机UUID的结构化JSON记录，旧用户NULL读为空集合。选择有界用户聚合，以复用现有用户行锁、46表快照迁移和数据库写围栏；没有第二个余额源、没有在内存模拟持久化。大量卡片/跨卡查询未来应迁出独立关系表。
- JSON只存本人的模拟账户ID、名称、发行机构、四位尾号、类型、状态、默认标记、整数分信用额度和时间。没有完整卡号、有效期、CVV或真实持卡人证件字段；不接受PAN/CVV/自定义user_id请求。
- 初始已用信用额度为持久化的0；可用信用额度由Go根据已存额度减已用额度计算，不让LLM或浏览器计算。尚无刷卡/还款入口，因此不能把它展示成已连接真实信用账单。
- 读接口绑定已验证的本人用户身份，拒绝Agent。绑定与设默认卡时验证关联账户归属、模拟标记、CNY及active状态。
- 三种变更均使用 `card_manage` 意向，Agent/Tool不能创建。prepare只记录意向，不改卡片；authorize绑定摘要/nonce与本人；execute复用用户行锁、数据库事务、风险校验、savepoint、幂等和审计。
- 集合摘要在执行时重新校验。确认后其他卡片发生变化，旧意向以STATE_CONFLICT失败，用户必须重新准备。每用户至多一张默认卡；解绑默认卡后不擅自指定别的卡。

## API 与 curl

OpenAPI主契约升级至 **0.17.0**。Coze Plugin不增加卡片工具。

```shell
# 当前同源网页代理；Bearer取真实登录响应，不能把签名密钥放进前端。
curl http://127.0.0.1:8088/api/v1/cards -H 'Authorization: Bearer <access_token>'
curl http://127.0.0.1:8088/api/v1/cards/<card_uuid> -H 'Authorization: Bearer <access_token>'

# 绑定借记卡：先创建待确认意向。account_id是本人已有模拟账户ID。
curl -X POST http://127.0.0.1:8088/api/v1/execution/operations \
 -H 'Content-Type: application/json' -H 'Authorization: Bearer <access_token>' \
 -H 'Idempotency-Key: <本次准备的稳定UUID>' \
 -d '{"kind":"card_manage","parameters":{"action":"bind","account_id":"<account_id>","issuer":"模拟银行","name":"日常卡","last_four":"1234","card_type":"debit","credit_limit_minor":0}}'

# 信用卡使用card_type=credit，并提交整数分credit_limit_minor。不得把额度存入账户余额。
# 设置默认或解绑，同样只准备意向；不要给这两类参数附上bind字段。
curl -X POST http://127.0.0.1:8088/api/v1/execution/operations \
 -H 'Content-Type: application/json' -H 'Authorization: Bearer <access_token>' \
 -H 'Idempotency-Key: <新的准备UUID>' \
 -d '{"kind":"card_manage","parameters":{"action":"set_default","card_id":"<card_uuid>"}}'
# 解绑用action=unbind。
```

收到意向后展示 action、卡片名称/发行机构、尾号、关联账户及资金影响，用户明确同意后才调用：

```shell
curl -X POST http://127.0.0.1:8088/api/v1/execution/operations/<operation_id>/authorize \
 -H 'Content-Type: application/json' -H 'Authorization: Bearer <access_token>' \
 -d '{"decision":"approve","intent_digest":"<服务端摘要>","confirmation_nonce":"<服务端nonce>"}'
curl -X POST http://127.0.0.1:8088/api/v1/execution/operations/<operation_id>/execute \
 -H 'Content-Type: application/json' -H 'Authorization: Bearer <access_token>' \
 -H 'Idempotency-Key: <执行阶段稳定UUID>' \
 -d '{"authorization_id":"<真实授权ID>","intent_digest":"<服务端摘要>","expected_version":<最新版本整数>}'
```

上述尖括号均须用真实响应替换，不是可直接提交的JSON。普通响应为现有Envelope；成功执行仍需 `operation.status=succeeded`。卡片UUID不是账户数值ID。已用/可用信用额度为整数分，Unix毫秒时间，status为active/unbound。绑定字段字节长度最多96；前端中文名称/机构限制32字符。错误沿用INVALID_ARGUMENT、FORBIDDEN、NOT_FOUND、ACCOUNT_FROZEN、STATE_CONFLICT、IDEMPOTENCY_CONFLICT及网关授权错误。

## 迁移与部署

先在明确的隔离 `*_test` 库执行测试，再在维护窗口迁移当前PG主库。不要对冻结历史MySQL主库运行迁移；新014快照的接收端也需要014列契约，不能直接导入旧011/013目标。

```powershell
# 仓库根目录，已有私有配置/网络/基础镜像的本地环境。
powershell -NoProfile -File deploy/banking/Build-Image.ps1 -LocalBinaries -ImageTag qingyingjin-banking-service:card-management -RuntimeBaseImage qingyingjin-banking-service:sms-auth
powershell -NoProfile -File deploy/banking-web/Build-Image.ps1 -LocalDist -ImageTag qingrongjin-banking-web:card-management -NginxRuntimeImage qingrongjin-banking-web:sms-auth

# 仅在已确认主库和维护窗口时操作。
docker stop qingyingjin-banking-service-1
Push-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking-postgres/finance.migrate.env.local -expected-database qingrongjin_finance
Pop-Location
docker compose --env-file deploy/banking-postgres/finance.compose.env.local -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml -f deploy/banking-postgres/compose.yaml -f deploy/banking/compose.phone-auth.yaml -f deploy/banking/compose.sms-development.yaml -f deploy/banking/compose.cards.yaml up -d --no-deps --no-build banking-service
docker compose -f deploy/banking-web/compose.yaml -f deploy/banking-web/compose.cards.yaml up -d --no-deps --no-build banking-web
```

测试脚本：Go `internal/cards/cards_test.go`、`internal/execution/cards_test.go`、`cards_integration_test.go`、`internal/httpapi/cards_integration_test.go`；前端 `tests/cards-browser.mjs` 明确使用拦截fixture验证UI契约，不冒充真实执行。实际运行结果统一记录在仓库 `output/card-management`，最终验收文档会区分真实数据库、fixture页面和在线模拟业务。
