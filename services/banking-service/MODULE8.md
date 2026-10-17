# 模块8：执行权限、用户授权、风控与审计

Banking Service 提供统一、持久化的模拟金融执行网关。模型仅可读取和准备操作；签名人类用户 JWT 才可授权及执行。金融结果仍来自 Go 领域服务和账本，Coze 输出仅是建议或解释。

## 接口

用户 JWT API：

- `POST /api/v1/execution/operations`，201，必须提供 Idempotency-Key。
- `GET /api/v1/execution/operations?kind=transfer&resource_id=7`，最多返回100条。
- `GET /api/v1/execution/operations/{operation_uuid}`。
- `POST /api/v1/execution/operations/{operation_uuid}/authorize`，仅人类用户。
- `POST /api/v1/execution/operations/{operation_uuid}/execute`，仅人类用户且必须提供 Idempotency-Key。
- `GET /api/v1/execution/operations/{operation_uuid}/audits`。
- `GET /api/v1/execution/risk-rules`。

原生 Coze 插件仅新增 `POST /tools/v1/execution/prepare_operation` 和 `POST /tools/v1/execution/get_operation`，成功HTTP200。插件共有21条路径，无用户授权/执行接口。工具身份绑定服务端指定用户，模型不得提供用户ID、密钥、JWT或授权令牌。

Prepare 请求为 `{ "kind": "transfer", "parameters": { ... }, "expires_in_seconds": 900 }`；parameters 使用对应模拟金融操作的严格领域输入。金额为整数分，资源ID为十进制字符串。支持 transfer、savings_deposit、savings_withdrawal、cancel_subscription、budget_rule_update、account_status、open_account、import_transaction、create_budget、create_savings_goal、subscription_manage。

授权请求为 `{ "decision": "approve", "intent_digest": "准备结果中的摘要", "confirmation_nonce": "准备结果中的一次性确认值" }`。执行请求为 `{ "authorization_id": "授权UUID", "intent_digest": "同一摘要", "expected_version": 2 }`。expected_version 必须取最新 operation.version；不可硬编码示例版本。明确用户确认操作和金融执行分属两次请求。Task/Trace 请求头仅用于关联，不能提升权限。

下面的curl例子在可信人类用户端执行；先设置由操作者签发的 `BANKING_USER_JWT`。实际账户ID、金额和授权选择必须由用户核对。确认值仅在用户端处理，不传给Coze。

```powershell
$base = 'http://localhost:8090'
$headers = @('-H', "Authorization: Bearer $env:BANKING_USER_JWT", '-H', 'Content-Type: application/json')
@{ kind='transfer'; parameters=@{ source_account_id='123'; target_account_id='456'; amount_minor=1000; currency='CNY' } } |
  ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8NoBOM prepare.json
$prepared = curl.exe -sS @headers -H "Idempotency-Key: $([guid]::NewGuid())" --data-binary '@prepare.json' "$base/api/v1/execution/operations" | ConvertFrom-Json
$op = $prepared.data.operation
# 用户在此核对 $op.parameters、$op.snapshot、金额、目标、风险和过期时间后，才选择 approve。
@{ decision='approve'; intent_digest=$op.intent_digest; confirmation_nonce=$op.confirmation_nonce } |
  ConvertTo-Json | Set-Content -Encoding utf8NoBOM authorize.json
$authorized = curl.exe -sS @headers --data-binary '@authorize.json' "$base/api/v1/execution/operations/$($op.id)/authorize" | ConvertFrom-Json
$current = $authorized.data.operation
@{ authorization_id=$current.authorization_id; intent_digest=$current.intent_digest; expected_version=$current.version } |
  ConvertTo-Json | Set-Content -Encoding utf8NoBOM execute.json
curl.exe -sS @headers -H "Idempotency-Key: $([guid]::NewGuid())" --data-binary '@execute.json' "$base/api/v1/execution/operations/$($op.id)/execute"
```

11种 parameters 的精确命名契约见OpenAPI：`ExecutionTransferParameters`、`ExecutionSavingsDepositParameters`、`ExecutionSavingsWithdrawalParameters`、`ExecutionCancelSubscriptionParameters`、`ExecutionBudgetRuleParameters`、`ExecutionAccountStatusParameters`、`ExecutionOpenAccountParameters`、`ExecutionImportTransactionParameters`、`ExecutionCreateBudgetParameters`、`ExecutionCreateSavingsGoalParameters`、`ExecutionSubscriptionManageParameters`。储蓄入金/出金为 `{goal_id,input}`，订阅管理为 `{subscription_id,input}`；账户状态包含 expected_version，预算规则包含 budget_id、rule_id、expected_digest、enabled。

## 兼容与拒绝

生产配置网关后，旧开户、导入交易、转账及审批、储蓄目标创建和入金/出金、预算创建、账户状态更改、订阅确认/管理接口返回 GATEWAY_REQUIRED。读接口和计划接口继续可用。既有 create_cancel_intent 经网关 PrepareSubscription 准备，并保持原订阅intent响应；不可把旧确认接口作为执行入口。

Agent JWT 只可读取、准备和明确列入白名单的专业分析/计划及任务提交操作；编排 confirm、inputs、pause/resume/cancel 不授予 Agent。身份取签名claims而非模型参数或自定义HTTP头。拒绝、输入失败与无身份敏感请求进入持久化审计；无法写审计时失败关闭。所有响应采用既有Envelope；日志不记录请求体、密钥、JWT或用户确认值。

## 验证与集成

```powershell
go test ./internal/execution ./internal/httpapi ./api
python scripts/generate_core_openapi.py
```

OpenAPI生成包含模块2至8，并递归更新 coze/banking 下专门的 plugin.openapi.yaml / coze-plugin.openapi.yaml，不覆写 agent-config.yaml。原生Coze导入、实际模型选择和发布参考仓库 `coze/banking/execution-gateway/README.md`。本模块未发布真实Coze模型、Agent或Workflow，不声称已接通外部银行；只有本地模拟账本执行。


## 审查后的执行边界

正常必要消费在数据库事务中的业务保存点内记账，再按真实流水重新计算必要预留；校验失败会回滚交易、账本、余额及业务审计，网关保留失败状态和风险拒绝记录。非必要消费及上月流水不能释放本月必要类别的预算预留。FinancialPlan中明确设置的剩余生活义务不会因消费分类而自动降低，用户仍需通过既有计划设置接口更新实际剩余义务。

Agent只允许列明的分析/规划路由，不能调用 financial-plans/{id}/decisions；需要用户确认的规划选择保留给签名用户。错误输入和越权请求在可核实所有权时记录具体操作意向ID；他人意向不会进入调用者的审计历史。

创建意向会先检查预算年份、类别长度、目标期限/名称及交易必填字段。REST路径、金额整数分、错误码和数据库结构不变，未新增迁移。最新验收见 docs/banking-module8-review.md，原始记录在 output/module8-review/。
