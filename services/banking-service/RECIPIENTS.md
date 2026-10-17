# 按姓名或手机号找人转账

入口：网页「可信执行与安全」`/security` 顶部的 **找人转账**。只操作本项目模拟人民币账户，没有接真实银行、微信或支付宝。

## 使用流程

1. 收款人在「我的收款设置」选择本人的可用模拟CNY普通账户，明确开启「允许通过姓名或手机号查找我并向我转账」。默认关闭；开启要求已完成项目内手机号验证。当前 development 验证不代表真实短信或实名核验。
2. 付款人按完整姓名／账户显示名（至少两个字符）或完整手机号查找，手机号接受11位或+86格式。姓名是注册显示名，不是经银行核验的真实姓名。
3. 同名返回多个脱敏候选，必须手动选择，不自动选中。同名无法区分时，改用完整手机号。结果只包含显示名、脱敏手机号、收款账户名称与掩码、收款人ID、设置版本和验证方式，不返回余额、完整手机号或其他账户。
4. 付款人选择自己的付款账户，输入金额和备注，创建待确认意向。核对收款人、脱敏手机号、验证性质、双方账户、金额和备注后，明确授权执行。
5. 网关重新检查身份、目录状态及版本、收款人公开身份快照、账户归属、冻结、余额、金额上限和必要支出预留；成功后双方交易、各自双分录及审计同事务提交。

备注持久化到双方交易。创建意向、查找、开启收款不直接改资金。关闭目录、切换收款账户或改变已确认的收款身份信息会使旧意向失效，必须重新查找和确认。关闭收款不自动退款、不改变已有余额。

## 工程实现与一致性

- 新 recipients 模块保存版本化收款设置并提供精确查询、脱敏和执行时解析；手机号规范化和掩码从原 userauth 提取至 phoneid，共享同一规则，原认证入口保留兼容包装。
- 迁移017给 banking_users 增加可空 transfer_directory：PostgreSQL TEXT、MySQL MEDIUMTEXT，NULL读为 enabled=false/account_id="0"/version=1。保留原46业务表快照及写围栏，不修改历史迁移。
- 查询为POST，手机号不放URL；SQL使用参数绑定，不做前缀、子串、通配符或全目录查询。只查询已开启且手机号已验证、身份active、收款账户可用的其他用户；每次最多20项，超限 limited=true。
- Redis原子限频：每用户每分钟20次、每IP每分钟60次。查询内容和完整手机号不作为Redis键或日志字段；Redis不可用时拒绝查询。当前代理保留既有不信任任意转发头的配置，多个代理后用户可能共享IP额度。
- recipient_transfer 为人类用户专用，Agent不能查询目录或准备此类操作。没有新增Coze插件工具、自动转账权限或自然语言转账编译。
- 参数含 recipient_id、recipient_version、source_account_id、amount_minor、description，不接受任意 target_account_id。收款账户由服务端解析。确认摘要绑定完整意向和公开收款快照，不能用 confirmed=true 代替授权。
- 沿用现有网关 authorize/execute、nonce、15分钟以内有效期、幂等和审计。事务外发现不可变收款用户ID，事务内先按ID排序锁定双方用户，再读取最新设置与账户，避免MySQL旧快照。
- 复用AA已经验证的跨用户账本路径：Repository.Post仍是唯一余额写路径；双方各一笔交易和一个两分录journal，通过 operation_id 关联，在同一个数据库事务提交。单端写入失败整体回滚。
- 同一幂等键重复准备返回原意向；同一意向重复或并发执行只入账一次。不同意向代表不同转账，必须各自授权，不以金额相同推断为同一笔。AA份额去重规则继续保留。
- 结算流水分类 peer_transfer、kind=internal_transfer，避免把资金划转当消费；备注保存原文并由页面转义显示，不交给LLM作为指令。

## API（Go OpenAPI0.20.0）

| 方法 | 路径 | 请求与返回 |
| --- | --- | --- |
| GET | /api/v1/recipients/settings | 返回本人enabled、account_id、version |
| PUT | /api/v1/recipients/settings | enabled、account_id、expected_version |
| POST | /api/v1/recipients/search | kind=name或phone、query；返回items、limited |
| POST | /api/v1/execution/operations | kind=recipient_transfer，parameters见下 |

均需本人用户Bearer，拒绝Agent。响应沿用Envelope；ID为字符串、金额为整数分。RecipientView包含id、display_name、phone_masked、account_name、account_masked_number、version、phone_verification_method。没有保存联系人备注名或模糊手机号匹配功能。

常见错误：INVALID_ARGUMENT=400、UNAUTHENTICATED=401、FORBIDDEN=403、NOT_FOUND=404，设置/幂等冲突=409，RECIPIENT_RATE_LIMITED=429，DEPENDENCY_UNAVAILABLE=503。执行结果必须检查 operation.status/error_code；HTTP200不代表转账成功。收款关闭、版本变化、身份变化、冻结、余额不足、必要支出保护等都可能使操作failed而不扣款。

curl示例（用户令牌取自真实登录，ID及版本取自响应；不要把手机号、令牌或密码放到URL或聊天）：

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8088/api/v1/recipients/settings
curl -X PUT -H "Authorization: Bearer $RECIPIENT_TOKEN" -H 'Content-Type: application/json' \
  --data-binary @receiving.json http://127.0.0.1:8088/api/v1/recipients/settings
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  --data-binary @search.json http://127.0.0.1:8088/api/v1/recipients/search
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $KEY" \
  --data-binary @transfer.json http://127.0.0.1:8088/api/v1/execution/operations
```

receiving.json：`{"enabled":true,"account_id":"本人收款账户ID","expected_version":1}`；关闭时enabled=false、account_id="0"，版本取最新。

search.json：`{"kind":"name","query":"完整显示名"}`；或kind=phone、query为完整手机号。不要把请求文件中的实际手机号纳入源码或日志。

transfer.json：`{"kind":"recipient_transfer","parameters":{"recipient_id":"已选收款人ID","recipient_version":2,"source_account_id":"本人付款账户ID","amount_minor":2000,"description":"聚餐费用"}}`。接着由付款人核对并提交 intent_digest/confirmation_nonce 到原 `/execution/operations/{id}/authorize`，再以返回的 authorization_id/intent_digest/expected_version 调用 `/execute`。

## 部署与限制

本地权威金融库保持PostgreSQL，已016→017；旧历史MySQL继续冻结。Go/Web镜像均使用recipient-transfers标签，原链末尾分别追加 deploy/banking/compose.recipients.yaml、deploy/banking-web/compose.recipients.yaml，无新增密钥或环境变量。

当前手机号验证仍为本地开发模拟，姓名仍为显示名；没有真实实名金融收款、通讯录、自然语言转账或真实外部支付接入。目录查询使用现有数据库JSON配置与关联查询，尚未做大规模目录、生产时效或容量SLA验收。测试、变更清单及实际启动命令见 docs/hand-off/recipient-transfers-validation.md。
