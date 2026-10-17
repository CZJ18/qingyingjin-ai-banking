# 模块2：Banking Core

目录与真实验证结果见 `../../docs/banking-module2-delivery.md`。模块1文档保留为历史交付记录；当前业务和运行方式以本文为准。

## 结构与复用

```text
cmd/server          Gin 服务与依赖生命周期
cmd/migrate         版本化 SQL 迁移
cmd/seed            调用真实应用服务初始化模拟数据
cmd/token           操作员签发用户 JWT
internal/entity     核心实体、整数金额规则、操作摘要
internal/repository MySQL 事务、锁、幂等、只允许账本路径写余额
internal/service    账户/交易/转账/预算/目标金业务及查询
internal/risk       模块8可替换 Gateway 和模拟安全策略
internal/httpapi    用户路由、身份、严格 JSON、错误/响应/日志
internal/migration  001_core.sql、checksum、迁移锁
api                 可下载 OpenAPI3；插件文档不暴露资金 API
tests/integration   真实 MySQL/Redis、HTTP、故障注入和并发测试
```

依赖方向：Handler → Service → Risk / Repository → MySQL。Handler 不持有 GORM 或 Repository。复用 module1 config、identity、platform、日志、健康检查；复用 `backend/domain/banking/entity` 的 Money、币种规范化及分页，账户详情通过现有公共租户仓储查询。旧更新 API 会直接写余额，因此不暴露或调用；新账户和交易表沿用其兼容字段，新资金事务在独立服务实现。没有修改 Coze 或 backend 原代码。

## 数据与资金规则

- 唯一余额投影存于 banking_bank_accounts；AccountBalance 是只读 DTO，实时对账。初始0资金无分录，正初始资金有模拟清算双分录。
- 所有金额 int64/BIGINT 整数分，首版 CNY，单笔/账户上限10^12分。公开 ID 为字符串，时间为 UTC Unix 毫秒。
- ledger delta 从账户资产角度表示；debit 正、credit 负。同 journal 两条分录同币种、和为0；NULL account_id 是模拟清算对手方，并非用户账户。
- Transaction.external_transaction_id 在用户/账户范围唯一；Idempotency-Key 在用户/动作范围唯一。相同规范化请求返回原持久响应，不同请求409。事务唯一约束是权威，Redis不承担资金锁。
- record_only 持久记录分析事实，不改余额、不产生资金分录；apply_simulated_effect 需要用户显式确认，进入安全网关并写账本。预算包括两种模式的 external 支出事实；初始化和内部转账不计消费。
- 意向只有同一用户的两账户可转账，参数不可修改，授权hash绑定ID、用户、源/目标、金额、币种、目的/目标、15分钟期限。显式 approve 在本模块同步执行，reject 不扣款。锁意向→按ID锁账户→检查授权、状态、余额→双边资金/交易/储蓄流水/审计一起提交。重复确认返回原终态；不同决定409。
- 业务拒绝保存 failed 和 failure_code，返回422；数据库故障回滚全部效果及确认，意向保持 pending_approval，可重试。冻结/非active状态拒绝资金效果。
- 月预算同用户/月/CNY唯一；类别总分配≤月额度，预留≤类别分配。预留是规划约束，不是账户资金冻结。usage 即时汇总真实交易，remaining 可为负；enforce_reserve 从 remaining 扣除尚未使用的必要预留。
- 目标金绑定专用零余额 savings 账户及 manual 月计划。普通转账、外部导入和状态修改不可绕过目标金专用路径。存入/取出先生成意向，审批后双分录及 SavingsLedger 同事务生效。进度来自专用账户余额，basis points=余额×10000/目标，不使用浮点或体重/节食奖惩。
- User ID 只来自有效JWT；先注册自己的资料。跨用户资源返回404。用户JWT可调用模拟业务；工具key不能替代JWT。操作员CLI不是对外登录API，未来身份/OAuth模块需替换。

## API 与错误

完整契约为 `api/openapi.yaml`，运行时 GET /openapi.yaml。28个路径、31个方法，Foundation和Core全部纳入路由匹配测试；集成用例校验成功请求及所有测试响应。Coze插件仍仅有集成探测端点，AI工具扩展留给后续模块。

| 类型 | API |
|---|---|
| 用户 | POST/GET /api/v1/users/me |
| 账户 | POST /api/v1/simulation/accounts；GET /accounts、/accounts/{id}、/balance、/ledger；PATCH /accounts/{id}/status |
| 交易 | POST /transactions/import；GET /transactions、/transactions/{id} |
| 转账 | POST /transfers；GET /transfers/{id}；POST /transfers/{id}/approvals |
| 预算 | POST/GET /budgets；GET /budgets/{id}、/usage |
| 目标金 | POST/GET /savings/goals；GET /savings/goals/{id}、/ledger；POST /deposits、/withdrawals |
| 审计 | GET /api/v1/audits |

表中省略的前缀均为 /api/v1，余额/账本/usage/存取路径位于相应实体ID之后。业务响应沿用 Envelope，附 request_id、trace_id、可选 task_id，实体返回 audit_id / journal_id。所有持久创建带 Idempotency-Key，注册除外；确认由意向行锁和唯一 approval 实现幂等，无须第二个任意确认幂等键。

400 INVALID_ARGUMENT；401 UNAUTHENTICATED；403 FORBIDDEN；404 NOT_FOUND；409 IDEMPOTENCY_CONFLICT/VERSION_CONFLICT；422 INSUFFICIENT_FUNDS/ACCOUNT_FROZEN/ACCOUNT_CLOSED/AMOUNT_LIMIT/APPROVAL_REQUIRED/APPROVAL_EXPIRED/RISK_REJECTED；503 DEPENDENCY_UNAVAILABLE。SQL、密钥和JWT不会作为错误详情返回。

## 启动（PowerShell，仓库根目录）

已存在 .env/.env.local 时不要重复生成。

```powershell
./deploy/banking/Initialize-Environment.ps1 -ToolUserId 42
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d banking-mysql banking-redis
./deploy/banking/Initialize-TestData.ps1
./deploy/banking/Build-Image.ps1 -LocalBinaries
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d --no-build banking-service
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz
```

Build-Image 不带 -LocalBinaries 使用完整多阶段源码构建，并用临时英文盘符处理 BuildKit中文路径；-LocalBinaries 用本机固定 module 依赖编译Linux静态二进制再构建 distroless nonroot镜像。本轮前者因 Docker Hub token 连接超时失败，后者成功。临时输出路径打印供复核；脚本不自动删文件。Compose migration 容器先执行SQL，成功后服务启动，数据库root凭证仅传给MySQL。未采用 AutoMigrate。

本机调试无需镜像：在 services/banking-service 执行 `go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking/.env.local`，再 `go run -buildvcs=false ./cmd/server -env-file ../../deploy/banking/.env.local`。本机端口13306/16379只在 compose.test.yaml 开启并绑定loopback。

SQL DDL在MySQL自动提交：迁移采用named lock、checksum、CREATE IF NOT EXISTS、中断重试；不会声称DDL整体可回滚。不兼容历史账户表会被拒绝，不自动重置数据或补造账本。正式余额数据导入需独立审计迁移。

## curl（POSIX shell，可在Git Bash/WSL执行）

JWT在本机可信操作员CLI签发，不传给Coze或模型。PowerShell等价写法是 `$token = go run -buildvcs=false ./cmd/token -env-file ../../deploy/banking/.env.local -user-id 42`。

```bash
cd services/banking-service
export BANKING_URL=http://127.0.0.1:8090
USER_TOKEN=$(go run -buildvcs=false ./cmd/token -env-file ../../deploy/banking/.env.local -user-id 42)
curl "$BANKING_URL/api/v1/users/me" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -d '{"display_name":"模拟用户"}'
curl "$BANKING_URL/api/v1/simulation/accounts" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: curl-account-v1' \
  -d '{"display_name":"日常账户","currency":"CNY","initial_balance_minor":100000,"confirm_simulated_funds":true}'
curl "$BANKING_URL/api/v1/accounts" -H "Authorization: Bearer $USER_TOKEN"

# 从实际响应填写自己的源/目标账户ID（均是字符串）
curl "$BANKING_URL/api/v1/transfers" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: curl-transfer-v1' \
  -d '{"source_account_id":"SOURCE_ID","target_account_id":"TARGET_ID","amount_minor":1000,"currency":"CNY"}'
# 阅读意向详情后，填写该意向ID和原响应中的 approval_hash；以下调用执行模拟资金变化
curl "$BANKING_URL/api/v1/transfers/TRANSFER_ID/approvals" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -d '{"decision":"approve","approval_hash":"EXACT_APPROVAL_HASH"}'

curl "$BANKING_URL/api/v1/transactions/import" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: curl-transaction-v1' \
  -d '{"bank_account_id":"SOURCE_ID","external_transaction_id":"receipt-demo-v1","direction":"expense","amount_minor":2500,"category":"food","merchant_name":"模拟餐食","occurred_at":1790812800000,"effect_mode":"record_only"}'
curl "$BANKING_URL/api/v1/transactions?category=food&direction=expense&limit=20&offset=0" -H "Authorization: Bearer $USER_TOKEN"
curl "$BANKING_URL/api/v1/budgets" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: curl-budget-v1' \
  -d '{"month":"2026-11","total_minor":100000,"categories":[{"category":"food","allocated_minor":60000,"essential_reserve_minor":40000}],"enforce_reserve":true}'
curl "$BANKING_URL/api/v1/budgets/BUDGET_ID/usage" -H "Authorization: Bearer $USER_TOKEN"
curl "$BANKING_URL/api/v1/savings/goals" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: curl-goal-v1' \
  -d '{"name":"规律饮食和合理运动","target_minor":120000,"starts_at":1790812800000,"ends_at":1822348800000,"monthly_minor":10000}'
curl "$BANKING_URL/api/v1/savings/goals/GOAL_ID/deposits" -H "Authorization: Bearer $USER_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: curl-goal-deposit-v1' \
  -d '{"bank_account_id":"SOURCE_ID","amount_minor":10000}'
# deposits/withdrawals 都只创建意向，随后按同样的 transfers/{id}/approvals 显式确认
curl "$BANKING_URL/api/v1/savings/goals/GOAL_ID/ledger" -H "Authorization: Bearer $USER_TOKEN"
```

示例ID占位符必须替换为真实响应，不是硬编码演示结果。初始化脚本调用真实Service，固定 fixture 日期/金额均标记为模拟输入；余额、预算和进度查询都来自MySQL。

## 测试与后续对接

```powershell
cd services/banking-service
go test ./... -count=1
$env:BANKING_TEST_ENV_FILE = 'D:\智慧农业\deploy\banking\.env.local'
go test -tags integration ./tests/integration -count=1 -v -timeout 120s
go vet ./...
```

模块8实现 risk.Gateway 的 Check（读取持久TransferIntent和Approval）与 CheckFunding（读取持久Transaction和授权审计），检查时在本模块资金事务内，不可另发外部扣款、另开不受约束的余额写入路径。当前策略只处理模拟显式确认，不包含正式多因子审批、限额分级、撤销、Outbox/事件任务或真实银行连接。

AI、多Agent、订阅取消、自动规划、定时划拨、前端、Coze插件金融工具、用户登录/OAuth/密钥轮换等属于后续模块；本模块不把健康指标作为资金奖惩条件。
