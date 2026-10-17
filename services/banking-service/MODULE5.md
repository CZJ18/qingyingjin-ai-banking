# 模块5：Subscription Agent

已实现独立Go订阅服务、5表迁移、规则识别与置信度、6个工具、用户取消授权、模拟状态机、持久化报告/历史、OpenAPI/JSON Schema、原生Coze Agent配置与工具Workflow生成器。没有前端或Coze核心修改。

## 目录与依赖

| 层 | 文件/目录 | 依赖 |
| --- | --- | --- |
| Entity | internal/entity/subscription.go | Base、Audit、Coze公开订阅状态枚举 |
| Repository | internal/repository/subscriptions.go | 现有Core/Tx、Atomic、GORM/MySQL、已有交易事实 |
| Service | internal/subscription | Repository、整数金额、JSON Schema、安全网关 |
| Risk Policy | internal/risk/subscriptions.go | 持久订阅、意向、授权；模块8替换接口 |
| Handler | internal/httpapi/subscriptions.go | 既有身份、错误、关联ID中间件 |
| Migration | internal/migration/004_subscriptions.sql | 001–003；不修改其校验和 |
| Agent/Workflow | ../../coze/banking/subscription-agent | 当前源码的原生Agent、Plugin节点、发布后的实际工具ID |

无新Go依赖。MySQL/Redis配置继续复用模块1；Redis检查可用性，订阅事实、授权和状态均在MySQL，不用缓存冒充持久化。

## 启动与初始化

在仓库根目录PowerShell执行：

```powershell
./deploy/banking/Build-Image.ps1 -LocalBinaries
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d --no-build
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz
./deploy/banking/Initialize-Subscriptions.ps1 -UserID 42
./deploy/banking/Verify-Subscriptions.ps1
```

迁移容器自动执行004；readiness检查版本4。初始化脚本通过真实API写入零余额账户和4笔record_only事实，重复执行不重复记账；停在candidate，不纳入管理、不确认、不取消。单独执行迁移：

```powershell
cd services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking/.env.local
```

## 工具及用户API

六个POST工具路径均为 `/tools/v1/subscriptions/<tool>`；用户镜像路径为 `/api/v1/subscriptions/<tool>`。

| tool | body | 结果 |
| --- | --- | --- |
| detect_subscriptions | 可选from/to/account_id | 持久报告、候选、证据、重复/价格变化 |
| get_subscription_details | subscription_id | 订阅、付款、意向历史、预测 |
| analyze_subscription_cost | 空body或{} | 已关联实际费用、条件预测、已取消避免支出 |
| suggest_subscription_actions | 空body或{} | 核实使用/重复及本地管理建议；无自动取消 |
| create_cancel_intent | subscription_id、expected_version；Idempotency-Key必填 | 绑定版本/摘要/有效期的pending意向 |
| get_subscription_status | subscription_id | 当前状态、付款及意向历史 |

工具凭据为服务端固定用户，不接受user_id；插件没有管理、确认、执行、转账或目标金写接口。Agent配置只安装上述6个工具。仅用户JWT接口：

- GET `/api/v1/subscriptions`：所有当前订阅分析。
- GET `/api/v1/subscriptions/{id}`、`/{id}/history`：详情及历史。
- POST `/api/v1/subscriptions/{id}/manage`：expected_version、confirm_local_simulation=true，可选用户提供last_used_at；需Idempotency-Key。
- GET `/api/v1/subscriptions/analyses/{id}`：已持久化不可变报告。
- GET `/api/v1/subscription-intents/{id}`：当前意向。
- POST `/api/v1/subscription-intents/{id}/confirm`：decision=approve/reject、confirmation_hash；与模拟取消同一事务。

用户创建意向返回201；工具创建返回200，适配当前Coze只识别HTTP200成功的实现。所有响应使用既有Envelope和X-Trace-ID/X-Task-ID，公开规范在 `/openapi.yaml`、`/coze-plugin.openapi.yaml`。幂等重放返回原始意向响应，最新状态需查询；过期按expires_at校验并在确认时持久化expired。

## curl 示例

以下是bash风格，Windows可用curl.exe配合实际环境变量/JSON文件，避免在历史中写入凭据。BANKING_URL、USER_TOKEN、TOOL_KEY、SUBSCRIPTION_ID来自实际环境/返回值，禁止使用虚构ID。

```bash
curl "$BANKING_URL/tools/v1/subscriptions/detect_subscriptions" \
  -H "X-Banking-Tool-Key: $TOOL_KEY" -H 'Content-Type: application/json' \
  -H 'X-Trace-ID: subscription-review' -H 'X-Task-ID: subscription-task' \
  -d '{"from":"2026-01-01T00:00:00Z","to":"2026-04-01T00:00:00Z"}'

curl "$BANKING_URL/tools/v1/subscriptions/analyze_subscription_cost" \
  -X POST -H "X-Banking-Tool-Key: $TOOL_KEY"

curl "$BANKING_URL/api/v1/subscriptions/$SUBSCRIPTION_ID/manage" \
  -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $MANAGEMENT_KEY" \
  -d "{\"expected_version\":$VERSION,\"confirm_local_simulation\":true}"

curl "$BANKING_URL/tools/v1/subscriptions/create_cancel_intent" \
  -H "X-Banking-Tool-Key: $TOOL_KEY" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $CANCEL_PROPOSAL_KEY" \
  -d "{\"subscription_id\":\"$SUBSCRIPTION_ID\",\"expected_version\":$CURRENT_VERSION}"

# 用户实际审阅意向后选择approve/reject，Agent不能调用这个接口：
curl "$BANKING_URL/api/v1/subscription-intents/$INTENT_ID/confirm" \
  -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"decision\":\"$USER_DECISION\",\"confirmation_hash\":\"$CONFIRMATION_HASH\"}"
```

## 规则与安全边界

至少3个非重复扣费事件且全部相邻间隔符合周/月/季/年规律才生成candidate；3次评分7500，4次以上9000，是规则分数非概率。五分钟同账户/商户/金额为可能重复，仍计入记录费用；不同账户同商户为可能重复订阅，不证明服务相同。会员、运动、生活采用原账单分类，不推断健康。

next_billing_at只推算最后记录的下一期，过期时明确提示证据过时，不虚构付款或推断停用。历史费用仅指已关联证据，不是全部银行消费。无使用数据为unknown；用户明确提供last_used_at超过90天仅提示核实。检测不修改active/cancelled的用户确认快照，新观察价格差异可查看payments/price_changed。

candidate -> 用户纳入本地管理 -> active -> 用户确认+网关 -> cancelled。意向pending_approval -> executed/rejected/failed/expired；幂等、摘要、版本、15分钟有效期和所有权均校验。相同确认返回原终态；不同决策冲突。每个用户变更加锁，并发确认只有一个持久授权和执行审计。业务取消失败不改订阅；SQL/审计失败回滚所有变更。拒绝/越权审计尽力持久化，数据库不可用时保留HTTP状态日志。

每月/三月/年度按52周、12月、4季或1年估算，整数分逐周期向下取整，明确价格/周期延续假设。available_balance_increase_minor始终0；不写余额、账本或目标金。检测最多1096天/10000交易；报告最多1000订阅/10000付款/1000重复组合，每个详情最多1000意向历史，超限返回ANALYSIS_LIMIT。

错误码：INVALID_ARGUMENT(400)、UNAUTHENTICATED(401)、FORBIDDEN(403)、NOT_FOUND含未授权资源(404)、IDEMPOTENCY_CONFLICT/VERSION_CONFLICT/STATE_CONFLICT(409)、APPROVAL_REQUIRED/RISK_REJECTED/ANALYSIS_LIMIT/INVALID_TRANSACTION_DATA/MODEL_OUTPUT_INVALID(422)、DEPENDENCY_UNAVAILABLE(503)。已确认后的取消业务失败或过期以200返回failed/expired及failure_code=CANCEL_FAILED/APPROVAL_EXPIRED/VERSION_CONFLICT，客户端不能只判断HTTP成功。

## Coze 原生接入

在现有Coze管理界面创建API Plugin并导入规范，用原生密钥配置service API token/header X-Banking-Tool-Key；每个插件凭据限一个银行用户，不可跨用户共享Agent。选择真实已安装模型，使用system-prompt.md并安装六个工具。无需更改原生界面。

生成原生工具Workflow参考，参数必须取当前实例真实发布值：

```powershell
python coze/banking/subscription-agent/generate_tool_canvas.py --tool detect_subscriptions --plugin-id $ActualPluginID --api-id $ActualToolID --plugin-name $ActualPluginName --plugin-version $ActualPublishedVersion --out subscription-detect.canvas.json
```

六个工具均可生成Start->Plugin->End包装；不含LLM资金计算、不含授权节点、不自动重试。Start传入业务参数、trace_id/task_id，取消提案另需idempotency_key；检测包装要求from/to，账户过滤可在原生UI配置。生成器验证画布结构，不是假定存在的公开导入API，也不自动发布。Supervisor从原生Agent/Workflow取得权威JSON，同用户Savings Agent消费预测字段而不是余额。缺少实际Coze实例/模型/工具ID，因此真实发布和模型端到端联调尚未完成。

## 测试

```powershell
cd services/banking-service
go test ./...
go vet ./...
go mod verify
$env:BANKING_TEST_ENV_FILE='D:\智慧农业\deploy\banking\.env.local'
go test -tags integration ./tests/integration -count=1
cd ../..
python -m unittest discover -s coze/banking/subscription-agent -p 'test_*.py' -v
```

执行结果、镜像/健康检查证据及修改文件清单见docs/banking-module5-delivery.md与其JSON附件。集成测试使用真实MySQL持久化；失败模拟器只用于取消失败测试，GORM回调只用于注入真正SQL错误，不进入生产配置。


Coze当前整数响应转换器不处理null，未设置的last_used_at/cancelled_at/executed_at在工具响应中省略，Schema将其设为可选；没有伪造零日期。已有持久历史JSON中的null仍可由用户API读取。
