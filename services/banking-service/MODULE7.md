# 模块7：Multi-Agent Orchestrator

Supervisor协调Billing、Subscription、Savings。Coze执行原生推理Workflow及Plugin工具；独立Banking Service保存任务DAG、调度租约、重试和审计。专业Agent通过用户授权的Banking HTTP API读取确定性结果，不直接访问数据库或修改资金。Go调度器负责应用层任务生命周期，复用Coze既有Workflow执行而不重写其底层引擎。

## 目录和数据契约

| 层 | 位置 | 用途 |
| --- | --- | --- |
| 模型、策略、调度 | internal/orchestrator | AgentTask、AgentExecution、AgentDependency、ToolInvocation、ExecutionTrace及Job；固定工具权限和依赖图 |
| HTTP | internal/httpapi/orchestrator.go | JWT用户提交、查询、控制、确认、补参和SSE；工具密钥只能查询绑定用户任务 |
| SQL | internal/migration/006_orchestrator.sql | 持久化任务、依赖、执行、工具调用和元数据轨迹，保留原迁移校验和 |
| API | api/openapi.yaml、api/coze-plugin.openapi.yaml | 模块7 REST、SSE和只读工具契约 |
| Coze | ../../coze/banking/supervisor-agent | 原生配置参考、Supervisor Prompt、专业解释Prompt、JSON Schema、Workflow生成器 |

每个专业输出包含task_id、agent_type、status、result、evidence、errors、next_actions，并标记financial_execution_enabled=false。result保留专业模块原来的整数金额/证据结构；模型解释独立存入explanation，不允许修改result。trace/task/audit ID贯穿工具调用；日志和SSE只输出任务元数据，财务输入输出仅在持久化执行记录和受身份保护的查询中可见。

## 状态和金融边界

Job状态：queued→running→succeeded或failed。可进入paused、cancelled、awaiting_input、awaiting_confirmation、awaiting_gateway、blocked、review_required。Task状态包含pending、running、succeeded、failed、timed_out及上述等待/阻止状态。依赖只有上游成功后才能运行；源Goal Compiler明确blocked的任务保留protected_blocked，补参或确认不能解除源约束。

strategy=sequential顺序执行；parallel_read只并行read_only任务，有副作用的建议和操作意向串行处理。read_only指不修改金融资金事实，账单/订阅分析仍可持久化分析记录。priority越高优先，依赖优先于优先级。白名单工具决定角色与execution_mode，模型不能提高权限或伪造用户身份。

只读分析→生成建议→创建意向→用户确认→金融执行是不同边界。create_cancel_intent需要明确确认并调用模块8ProposalGateway；缺少网关保持awaiting_gateway。用户确认只允许后续建议/意向阶段，不能授权转账或完成订阅取消。模块7不执行资金交易，不修改已经完成的交易，不把未来预计节余记入余额。

发现重复扣费/订阅时安排核实任务并传递结构化证据；核实不等于认定服务闲置，取消意向不等于执行取消。自然语言任务使用原生Supervisor Workflow异步规划，接口先返回queued；真实模型未配置时安全失败。任务失败有限重试，超时和取消不把未知执行结果当成功；重启使用持久化租约和执行记录恢复。

## API和curl

Bearer JWT来自既有可信身份配置。不要将示例JWT或密钥写入仓库。BANKING_URL默认http://localhost:8090，以下为shell curl语法，PowerShell使用curl.exe。

```bash
curl -X POST "$BANKING_URL/api/v1/orchestrations" \
  -H "Authorization: Bearer $USER_JWT" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: orchestration-demo-1' -H 'X-Trace-ID: module7-demo' \
  -d '{"strategy":"parallel_read","use_coze":false,"tasks":[{"key":"billing","agent_type":"billing","tool":"analyze_spending","parameters":{"from":"2026-10-01T00:00:00Z","to":"2026-11-01T00:00:00Z"},"dependencies":[],"priority":20},{"key":"subscription","agent_type":"subscription","tool":"detect_subscriptions","parameters":{"from":"2026-07-01T00:00:00Z","to":"2026-11-01T00:00:00Z"},"dependencies":[],"priority":10}]}'
curl "$BANKING_URL/api/v1/orchestrations/$JOB_ID" -H "Authorization: Bearer $USER_JWT"
curl "$BANKING_URL/api/v1/orchestrations/$JOB_ID/result" -H "Authorization: Bearer $USER_JWT"
curl "$BANKING_URL/api/v1/orchestrations/$JOB_ID/trace?last_event_id=0&limit=100" -H "Authorization: Bearer $USER_JWT"
curl -N "$BANKING_URL/api/v1/orchestrations/$JOB_ID/events" -H "Authorization: Bearer $USER_JWT" -H 'Last-Event-ID: 0'
curl -X POST "$BANKING_URL/api/v1/orchestrations/$JOB_ID/pause" -H "Authorization: Bearer $USER_JWT"
curl -X POST "$BANKING_URL/api/v1/orchestrations/$JOB_ID/resume" -H "Authorization: Bearer $USER_JWT"
curl -X POST "$BANKING_URL/api/v1/orchestrations/$JOB_ID/cancel" -H "Authorization: Bearer $USER_JWT"
curl -X POST "$BANKING_URL/api/v1/orchestrations/$JOB_ID/confirm" -H "Authorization: Bearer $USER_JWT" -H 'Content-Type: application/json' -d '{"task_key":"confirmation","expected_version":3,"decision":"approve"}'
curl -X POST "$BANKING_URL/api/v1/orchestrations/$JOB_ID/inputs" -H "Authorization: Bearer $USER_JWT" -H 'Content-Type: application/json' -d '{"task_key":"subscription_details","expected_version":3,"parameters":{"subscription_id":"ACTUAL_ID"}}'
curl -X POST "$BANKING_URL/tools/v1/orchestrations/get_orchestration" -H "X-Banking-Tool-Key: $TOOL_KEY" -H 'Content-Type: application/json' -d '{"job_id":"ACTUAL_JOB_UUID"}'
```

也可提交task_plan_id（模块3真实持久化TaskPlan整数ID）及可信bindings；禁止同时混入多个任务输入来源。UUID路径必须规范小写UUID；SSE Last-Event-ID和trace游标为规范非负整数。SSE task携带持久化ExecutionTrace.id，不包含财务result；完整回放后终态发送end，连接30秒后end/reconnect，客户端保存最近ID继续请求。断线只终止流，不取消任务。

重复提交相同Idempotency-Key和相同输入返回原Job；不同输入冲突。确认和补参需要Job最新expected_version，越权资源返回NOT_FOUND；错误码沿用INVALID_ARGUMENT、INVALID_TASK_PLAN、STATE_CONFLICT、VERSION_CONFLICT、IDEMPOTENCY_CONFLICT、DEPENDENCY_UNAVAILABLE、COZE_UNAVAILABLE、MODEL_OUTPUT_INVALID、TOOL_OUTPUT_INVALID、GATEWAY_UNAVAILABLE。失败详情仅为安全错误码，不透传响应体或密钥。

## 启动、集成与验证

沿用deploy/banking/compose.yaml和模块1环境配置，先执行migrate再启动server。服务就绪检查需确认006迁移。Coze接入步骤与真实模型/插件ID要求见Supervisor目录README。模块8后续实现ProposalGateway；不通过当前插件追加资金执行权限。模块6新增用户POST /api/v1/financial-plans/{id}/replan仅生成建议，供Savings授权HTTP调用。

```powershell
python services/banking-service/scripts/generate_core_openapi.py
python -m unittest discover -s coze/banking/supervisor-agent -p 'test_generate*.py' -v
cd services/banking-service
go test -buildvcs=false ./...
$env:BANKING_TEST_ENV_FILE=(Resolve-Path '../../deploy/banking/.env.local').Path
go test -buildvcs=false -p 1 -tags integration ./... -count=1
```

实际执行结果和Docker健康记录由模块7交付测试记录列出。真实Coze发布/模型联调及模块8最终敏感执行在没有对应环境/实现时不能宣称完成。


## 模块8和故障恢复对接

工具策略是固定白名单：read_only、advice、intent、user_confirmation分别调度。最终金融执行不在白名单中。ProposalGateway仅接受已明确确认的Call，必须重新检查持久化用户、任务确认、订阅版本、幂等InvocationKey及风控，并返回pending_approval的真实模拟意向；不得在Propose内完成资金执行或订阅取消。main目前显式注入nil网关。

租约绑定单次执行波次，任务和执行记录同事务写入。旧租约/被取消波次的迟到结果不能覆盖状态。进程重启可重试未完成的只读调用（最多3次），分析记录可能因只读重试增加，成功检查点和已完成任务不会重复执行。建议/意向的未知中断或超时进入review_required；用户必须核实已有副作用后重新提交安全计划，不提供直接解除复核的接口。暂停耗尽次数的任务也进入复核。已知网关缺失可显式resume，在剩余次数内再检查，不能自动重试敏感意向。

单次调用超时100..60000毫秒，顺序模式每波1任务，只读并行模式每波最多4任务。单个worker顺序消费Job；多实例依靠SQL租约竞争。Coze调用取消只取消本地HTTP等待，不宣称中止Coze远端运行；迟到结果被fencing拦截。最终结果包括已执行成功/失败的标准化输出；未执行任务的状态在Job查询中可见。trace事件通过游标分页，executions/invocations为最近100条；单图最多32输入任务加理解/自动核实任务。

配置：BANKING_ORCHESTRATOR_API_BASE_URL为固定可信Banking地址，默认本容器http://127.0.0.1:8090；BANKING_SUPERVISOR_WORKFLOW_ID、BANKING_BILLING_AGENT_WORKFLOW_ID、BANKING_SUBSCRIPTION_AGENT_WORKFLOW_ID、BANKING_SAVINGS_AGENT_WORKFLOW_ID为真实已发布Workflow ID。角色Workflow只理解任务或解释已校验结果。未配置时结构化任务仍可用，自然语言/要求Coze解释的任务明确失败。

集成测试使用独立模拟用户和真实MySQL；调度器测试的Runner/Planner标明fixture。先在没有生产Orchestrator worker的专用数据库上运行手动调度测试，避免外部worker抢占测试任务；随后用Docker专项验证真实后台worker。当前本地实例是比赛模拟环境。
