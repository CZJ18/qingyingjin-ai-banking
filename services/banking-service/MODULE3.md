# 模块3：Goal-to-Banking 健康目标编译器

本模块只保存目标、解析事实和任务计划，不访问资金写接口。Coze未配置时仍可创建/更新/查询原始目标，parse返回503 COZE_UNAVAILABLE，不会生成假解析结果。业务数据存储在模块2同一独立Banking MySQL schema。

## 代码与依赖

| 层 | 文件 | 职责 |
|---|---|---|
| Entity/DTO | internal/goal/model.go | Goal、GoalConstraint、GoalVersion、TaskPlan、TaskDependency、Parsed、Plan |
| Repository | internal/goal/repository.go | 复用Core.Atomic创建幂等；目标行锁/乐观版本；解析claim；原子保存计划和审计 |
| Service | internal/goal/service.go | 创建/更新、只读预算快照、Coze解析、编译、失败持久化 |
| Model adapter | internal/goal/parser.go、internal/coze/client.go | 已核实同步workflow协议，无自动POST重试，无解析兜底假结果 |
| Schema | internal/goal/schema.go、schemas/*.json | Draft2020-12校验、原文金额/周期/语义证据 |
| Compiler/Risk policy | internal/goal/compiler.go | 固定任务允许清单、必要支出保护、冲突、缺失信息、权限/依赖/参数校验 |
| Handler | internal/httpapi/goals.go | 用户JWT REST，金额整数、严格请求体、关联ID |
| Migration | internal/migration/002_goals.sql | 5张业务表、用户复合外键、唯一目标版本和唯一版本计划 |
| Workflow/Prompt | ../../coze/banking/goal-compiler | 原生节点参考配置、Prompt和画布生成工具 |
| Tests | internal/goal/*_test.go、tests/integration/goals_test.go | 单元和真实MySQL API测试 |

新增依赖 `github.com/santhosh-tekuri/jsonschema/v6 v6.0.3`。Gin/GORM/MySQL/JWT/审计/HTTP关联/日志沿用模块1/2。BudgetReader接口只暴露BudgetUsage，不提供资金执行方法。模型不决定任务ID、权限、执行模式、依赖或API路径。

## 启动

从仓库根目录PowerShell执行。沿用已有本地.env，不输出密钥。

```powershell
# 若数据库尚未启动
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d banking-mysql banking-redis

# 实际增量迁移；001保持原checksum，002可重复验证
Push-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking/.env.local
Pop-Location

# 当前机器可用的本地交叉编译+运行镜像路径
./deploy/banking/Build-Image.ps1 -LocalBinaries
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d --no-build banking-service
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz

# 初始化持久化示例目标；仅原始目标，尚未调用模型
./deploy/banking/Initialize-Goal.ps1
```

镜像tag为 `qingyingjin-banking-service:module3`。`compose.test.yaml` 仅供本机数据库集成测试发布127.0.0.1:13306/16379端口；部署不需要该覆盖文件。没有删除/重置数据卷。完整Dockerfile源码构建仍可尝试；本机模块2曾遇到Docker Hub网络限制，本模块运行镜像采用 -LocalBinaries。

Coze启用需在本机`.env`（Compose）与`.env.local`（本机CLI）配置：

```dotenv
BANKING_COZE_BASE_URL=http://实际可达的Coze主机:端口
BANKING_COZE_PAT=填写实际PAT
BANKING_GOAL_WORKFLOW_ID=填写已发布工作流的正整数ID
BANKING_COZE_TIMEOUT=30s
```

不要使用这里的说明文字当配置值。Compose环境已传递该workflow_id。服务器响应超时调整为CozeTimeout+10s。`/readyz`检查MySQL/Redis/迁移002，代表服务存储就绪，不代表模型已配置；parse以自身配置和调用结果明确报告模型不可用。

## Coze原生配置

当前源码依据：`backend/application/workflow/workflow.go` 的OpenAPIRun同步分支；画布依据 `backend/domain/workflow/internal/canvas/examples/llm_node_with_skills/llm_node_with_workflow_tool.json`。无需修改Coze源码。

在Coze原生工作流管理界面创建三节点 Start→LLM→End。Start必填字符串input_string；LLM input引用Start.input_string，user prompt为`{{input}}`，system prompt为goal-compiler/system-prompt.md加完整goal-parse.schema.json，选本机实际安装的模型，关闭工具和聊天历史，temperature=0，JSON responseFormat=2。End用returnVariables返回字符串output，引用LLM.output。工作流发布后配置其真实workflow_id和可运行该工作流的PAT。

`workflow-config.yaml`是逐节点配置参考。生成器复用真实样例字段（包括原源码的`modleName`拼写），要求真实model ID/name，移除样例已有的其他workflow工具。画布JSON是工程参考，**没有声称Coze支持将该文件直接作为公开导入接口的请求体**。用原生界面配置和发布。

```powershell
python coze/banking/goal-compiler/generate_canvas.py --model-id $实际模型ID --model-name $实际模型名称 --out output/goal-parser.canvas.json
```

Banking调用已核实的POST `/v1/workflow/run`，PAT Bearer，`parameters`是包含input_string的JSON字符串，`is_async=false`。响应需要code=0、execute_id、data字符串，data内只有End的output字符串。UUID parse_id、Coze execute_id、版本、trace_id与audit_id持久化关联。

## REST API

全部以下接口要求用户Bearer JWT；工具密钥不能调用。JSON用户ID不能覆盖JWT身份，版本和goal_id均按用户过滤。

| 方法与路径 | 功能 |
|---|---|
| POST /api/v1/goals | Idempotency-Key创建原始目标，201，初始unparsed |
| GET /api/v1/goals/{id}?version=N | 当前或指定历史版本与其计划 |
| PUT /api/v1/goals/{id} | expected_version乐观追加新版本，保留旧版本 |
| POST /api/v1/goals/{id}/parse | expected_version指定当前版本，调用Coze、校验并编译；成功版本重复请求不重复调用模型 |
| GET /api/v1/goals/{id}/plan?version=N | 查询当前/历史计划；未编译404 |
| GET /api/v1/goals/{id}/versions?limit=20&offset=0 | 版本历史，limit最大100 |
| GET /api/v1/goals/{id}/compare?from=1&to=2 | 原文、确认上下文、解析结果差异 |

完整OpenAPI见api/openapi.yaml及运行中的/openapi.yaml。重新生成：`python services/banking-service/scripts/generate_core_openapi.py`，自动追加模块3契约。

请求例子如下。financial_context是用户明确提供的模拟月度信息，金额全部为分；缺失值保留null。可选budget_id是字符串，只能关联自己的预算。消费预算表示全部月消费，已经包含必要生活支出，不是额外自由消费金额。

```json
{
  "text": "未来三个月，我想保持规律饮食和运动，每月储蓄200元，同时不影响正常生活。",
  "financial_context": {
    "monthly_income_minor": 500000,
    "spending_budget_minor": 400000,
    "essential_expenses_minor": 200000
  }
}
```

把该JSON保存为本地goal.json。只在本机捕获token，不打印或发到聊天：

```powershell
Push-Location services/banking-service
$userToken = go run -buildvcs=false ./cmd/token -env-file ../../deploy/banking/.env.local -user-id 42
Pop-Location
curl.exe -X POST http://127.0.0.1:8090/api/v1/goals -H "Authorization: Bearer $userToken" -H "Idempotency-Key: user-goal-001" -H "Content-Type: application/json" -H "X-Trace-ID: goal-demo" --data-binary '@goal.json'

# 用实际返回的goal.id替换$id；parse.json内容为{"expected_version":1}
curl.exe -X POST "http://127.0.0.1:8090/api/v1/goals/$id/parse" -H "Authorization: Bearer $userToken" -H "Content-Type: application/json" --data-binary '@parse.json'
curl.exe "http://127.0.0.1:8090/api/v1/goals/$id/plan" -H "Authorization: Bearer $userToken"
curl.exe "http://127.0.0.1:8090/api/v1/goals/$id/versions" -H "Authorization: Bearer $userToken"
curl.exe "http://127.0.0.1:8090/api/v1/goals/$id/compare?from=1&to=2" -H "Authorization: Bearer $userToken"
# update.json为完整新目标JSON并加expected_version:1；更新不自动解析
curl.exe -X PUT "http://127.0.0.1:8090/api/v1/goals/$id" -H "Authorization: Bearer $userToken" -H "Content-Type: application/json" --data-binary '@update.json'
```

## 状态与错误

| 状态/错误 | 行为 |
|---|---|
| unparsed / parsing / failed / parsed | 版本解析状态，不是资金执行状态 |
| awaiting_clarification | 缺预算/可靠月收入/必要支出/目标周期等，后续规划任务blocked |
| conflict | 目标与预算、必要预留、明确总目标或安全政策冲突；返回理由和requires_confirmation=true替代方案 |
| pending_confirmation | 仅编译可供确认的计划，financial_execution_enabled=false |
| 400 INVALID_ARGUMENT | 非整数金额、未知字段、无效ID、未提供expected_version等 |
| 404 NOT_FOUND | 不存在或非本用户资源；未生成计划 |
| 409 VERSION_CONFLICT | 更新/解析目标版本过期 |
| 409 IDEMPOTENCY_CONFLICT | 相同创建key绑定不同请求 |
| 409 PARSE_IN_PROGRESS | 同一版本已有解析，不重复调用模型 |
| 409 PARSE_OUTCOME_UNKNOWN | claim租约超时但结果未知；显式创建新版本再解析 |
| 422 MODEL_OUTPUT_INVALID | Schema失败、数值/原文证据不符、模型金融任务、异常End输出 |
| 422 INVALID_TASK_PLAN | 非允许任务、权限、参数、依赖或执行模式 |
| 503 COZE_UNAVAILABLE | 未配置、网络、超时或工作流不可用 |
| 503 DEPENDENCY_UNAVAILABLE | 数据库/事务/Schema等不可用；编译落库事务整体回滚 |

冲突和缺信息是成功解析的业务结果，parse HTTP200，需检查task_plan.plan.status。建议不会覆盖GoalVersion原文/解析值。总目标/周期可生成向上取整到分的月度建议，但须另行确认；明确月额、周期、总额不一致会产生冲突。

每个任务包含task_id、task_type、dependencies、parameters、required_permissions、execution_mode、status。固定任务链为query_accounts→analyze_bills→check_subscriptions→calculate_budget→generate_savings_plan→propose_financial_operation。用户明确禁止转账或一切金融操作时不生成最后的操作建议任务。subscriptions分析未在本模块执行。propose_financial_operation只表示计划节点，**不会调用Core.CreateTransfer**。

## 后续模块对接与限制

后续Supervisor/Billing/Subscription/Savings Agent只能读取受保护API并实现计划中的能力；required_permissions是逻辑能力契约，不是当前JWT已拥有的资金授权。执行器必须读取服务器保存的计划/版本并重新校验当前数据，不能信任客户端计划或模型返回的task。模块8独立控制明确授权和风控，计划确认不是转账授权。目标、版本、计划、task_id、trace_id、audit_id和Coze execute_id应贯穿执行审计。

本模块没有后台自动执行、AI直连数据库、订阅取消、自动扣款、真实银行、体重奖惩或前端。当前没有运行中的Coze实例与已发布解析workflow_id，因此真实模型端到端联调尚未完成；测试中的HTTP Coze服务是显式fixture，用于验证协议、异常处理与真实MySQL/API链路，不是实际AI结果。

运行验证：

```powershell
Push-Location services/banking-service
go test ./... -count=1 -timeout 90s
$env:BANKING_TEST_ENV_FILE='D:\智慧农业\deploy\banking\.env.local'
go test -tags integration ./tests/integration -count=1 -v -timeout 120s
go vet ./...
go mod verify
Pop-Location
python -m unittest discover -s coze/banking/goal-compiler -p test_generate_canvas.py -v
```

实际执行结果、健康检查及修改清单见 ../../docs/banking-module3-delivery.md。
