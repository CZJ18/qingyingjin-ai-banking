# Module13 / P5：Go LangGraph 安全桥

2026-10-14 实际实现与验收记录。模块13 为独立 Python LangGraph 服务提供用户/任务绑定的短期委托和受保护 Go 工具调用，继续复用 P1 Skill、P2 生活规划、P3 Coze 与原 HTTPExecutor。金融事实、整数金额、账户归属、风控、授权、执行和账本仍由原 Go/MySQL 实现；本模块不替换旧 Go/Coze 调度，不实施 P6 前端或 P7 金融数据库迁移。

本机 Go/P5/Coze 已运行，独立 Agent PostgreSQL 002 已应用。已切换官方 DeepSeek deepseek-flash，四专业角色与 Supervisor 真实模型验收通过，完整 P5 总记录 p5_accepted=true。原本本地模型启动阻碍已由云模型替换解决；旧 Qwen2.5-1.5B 模型文件及下载缓存已由用户删除并核实完成。实际测试、镜像与交付证据见 ../banking-agent-service/output/p5/acceptance.json。Python 图、RLS、journal 和恢复细节见 [P5.md](../banking-agent-service/P5.md)。

## 双鉴权与服务器绑定

新增 `BANKING_LANGGRAPH_SERVICE_TOKEN`，为空时安全桥返回 DEPENDENCY_UNAVAILABLE。配置要求独立、非模板、至少 32 bytes，并拒绝复用 BANKING_USER_JWT_SECRET 或 BANKING_TOOL_TOKEN。Python 对应 BANKING_AGENT_SERVICE_KEY；`deploy/banking-agent/Setup-P5.ps1` 一次生成并同步私有环境文件，不打印值、不覆盖旧 DB/JWT/PAT 凭据。

三条 `/api/v1/langgraph/*` 路由同时检查已验证 Bearer 和 `X-Banking-Agent-Key`。服务密钥使用 SHA256 后常量时间比较；随后读取真实 active 模拟用户。服务密钥不能替代用户身份、签发 JWT、授权或执行资金。用户 ID 来自已验证 Actor，角色与任务来自 Go 签发的 Agent JWT；请求体、查询字符串和 X-Task-ID 不得覆盖这些身份。

用户授权 Python 在一次请求中取得具体任务/角色的 Agent JWT。Go 将 JWT 签名密钥留在本进程，委托有效期固定 **120 秒**。Agent 权限为其注册角色的 READ/PLAN/PREPARE；不包含 AUTHORIZE/EXECUTE。Python 运行容器没有 Go JWT 签名密钥、Coze PAT 或 MySQL URL。

## 实际新增 API

路由实现在 [internal/httpapi/langgraph.go](internal/httpapi/langgraph.go)，实际契约在 [api/openapi.yaml](api/openapi.yaml)。所有请求使用严格 JSON 解码，未知字段不能增加权限。

|方法 / 路径|必须的身份|请求与响应|
|---|---|---|
|POST /api/v1/langgraph/delegations|用户 JWT + 服务密钥|`{task_id,agent_type}`；返回 token、expires_in_seconds=120、已绑定 task_id/agent_type|
|POST /api/v1/langgraph/run|已签名专业 Agent JWT + 服务密钥|`{skill_id,skill_version,tool,execution_mode,parameters,use_coze}`；返回受校验 AgentOutput|
|POST /api/v1/langgraph/plan|Supervisor Agent JWT + 服务密钥|`{instruction,bindings}`；返回已校验 tasks、真实 workflow_execute_id、owner_engine=langgraph、financial_execution_enabled=false|

委托 task_id 必须是 `lg-规范UUID.node_key`，整体不超过 128 字符；角色仅 supervisor、billing、subscription、lifestyle、savings。委托路由拒绝 Agent/Tool 身份签发新凭据。Go 在这里验证用户、任务命名空间和角色，不查询 PostgreSQL 任务/节点是否存在；任务 owner_id 与 owner_engine 由 P5服务校验。run 拒绝 Supervisor 身份，只接受注册的专业 READ/PLAN 工具；plan 仅接受 Supervisor。

run/plan 使用原生 60 秒请求截止时间，超时返回 TOOL_TIMEOUT。Python bridge 超时略长于此截止时间，以接收 Go 的明确错误；它不把网络失败当成功。AgentOutput 必须匹配已签名 task_id、agent_type、成功状态并禁止 financial_execution_enabled=true；Go补充实际 Skill ID/version/rule_version，Python继续校验结果、归属、整数金额、证据及响应大小。

安全桥没有 `/langgraph/authorize` 或 `/langgraph/execute` 接口。未配置桥、无服务密钥、错误用户/角色/任务、未注册 Skill/mode、错误模型或工具结果均返回明确错误，不降级为不受保护的成功路径。

## Skill、工具及金融准备边界

Go 使用原 `skills.Resolve` 再次校验 Skill/version/role/tool/mode。当前 Skill version 为 1：

|专业角色|Skill|安全桥可运行工具|
|---|---|---|
|Billing|billing_analysis|get_transactions、get_account_summary、analyze_spending、compare_budget、detect_anomalies、generate_billing_report；read_only|
|Subscription|subscription_analysis|detect_subscriptions、get_subscription_details、analyze_subscription_cost、suggest_subscription_actions、get_subscription_status；read_only|
|Lifestyle|lifestyle_planning|get_lifestyle_goal、get_lifestyle_plan；read_only。compile_lifestyle_plan；advice|
|Savings|savings_planning|get_financial_plan、compare_financial_plans；read_only。replan_financial_plan；advice|

Supervisor 的 task_planning 与 preparation/confirmation 节点由 Python 图协调。Python 的 `create_cancel_intent` / `require_gateway` 先调用真实 LangGraph interrupt，在用户提交绑定任务、节点、摘要、nonce、版本与有效期的 PREPARE_ONLY 确认后，通过原 `/api/v1/execution/operations` 创建 pending_authorization 意向；这不经过 `/langgraph/run`。

**P5 确认只准许准备，不是资金授权。** 原网关 authorize 必须使用独立用户身份，execute 继续验证已产生的用户授权、意向摘要、版本、账户归属、风险、余额与审计。Agent JWT 与服务密钥不具备这两项权限。取消 P5 任务停止未来推进，不能宣称撤销已经准备的意向或回滚上游规划。

所有金额来自 Go 确定性代码。Python 与 Coze 不能重算余额、发明 source ID、改写可信金额或替换工具结果。advice 可以创建规划版本，需要区分其副作用与纯读取；P5 不具有资金执行能力。

## owner_engine、调用幂等与旧引擎

新任务持久化在独立 PostgreSQL 中，固定 owner_engine=langgraph、外部 task_id=lg-UUID。Go只取得请求期委托，不查询或接管该 PostgreSQL 任务。安全桥 plan 不创建旧 Go Job，run 不提交旧 Go调度器；原 Go UUID Job 和历史 Coze任务仍由原引擎持有。两套任务标识与恢复入口分离，不能靠 Header 将旧任务伪装成 LangGraph 任务。

Go HTTPExecutor 接收固定 `InvocationKey="langgraph:"+已签名task_id`，传给原工具的 Idempotency-Key；Python 准备意向也使用稳定的同任务/节点调用键。已成功 journal 结果由 Python复用，原生幂等准备与生活方案编译支持安全重试；非幂等 replan 的未知结果必须停止并核实已有 Go规划。租约 fencing、RLS 与 pending writes 持久化由 Python服务实现，详情见 P5 文档；不存在跨 PostgreSQL/MySQL 原子提交或所有副作用 exactly-once 的承诺。

Go本模块新增 API、配置与调用键，没有新增金融 SQL迁移，没有改写账本逻辑或历史迁移。Agent PostgreSQL 002 只增加用户任务、确认、journal、事件、checkpoint/pending writes，复用非所有者 banking_agent_app 与 ENABLE/FORCE RLS。

## Coze 真实推理范围

HTTPExecutor 继续使用原 Go工具的严格 DTO、归属与证据校验。`use_coze=false` 返回确定性工具结果；`use_coze=true` 调用对应原生已发布解释工作流。自然语言 plan 必须真实调用已发布 Supervisor，取得并校验 workflow_execute_id，失败不得模拟成已完成模型解析。

|配置|用途|
|---|---|
|BANKING_SUPERVISOR_WORKFLOW_ID|自然语言任务规划|
|BANKING_BILLING_AGENT_WORKFLOW_ID|Billing 专业结果解释|
|BANKING_SUBSCRIPTION_AGENT_WORKFLOW_ID|Subscription 专业结果解释|
|BANKING_SAVINGS_AGENT_WORKFLOW_ID|Savings 专业结果解释|
|BANKING_LIFESTYLE_AGENT_WORKFLOW_ID|从 Go事实目录选择 finding_ids，Go渲染解释|
|BANKING_GOAL_WORKFLOW_ID / COZE_LIFESTYLE_GOAL_WORKFLOW_ID|原 Goal / Goal v2 解析，属于已复用 P3 能力|
|BANKING_BILLING_WORKFLOW_ID|独立自然语言 Billing Query Parser，和 Billing 解释不同；当前未配置|

P3历史真实全链路重点覆盖 Goal v2、Supervisor、Lifestyle；Billing/Subscription/Savings 有工作流更新记录，不能据此宣称当前 P5模型场景都已验收。工作流 ID 配置或私有状态的 published=true 不能证明当前原生发布与可调用状态。原生 Plugin Wrapper、独立 Coze Agent实体的参考画布不冒充已正式导入发布。

原生源码核实使用真实实现的 `/api/workflow_api/workflow_detail_info`；指定 workflow_version 才读取具体持久发布版本。普通 workflow_detail 读取 Draft；released_workflows/list_publish_workflow 当前处理器为空响应 stub，不能作为发布证明。`/v1/workflow/run` 验证 PAT用户空间、LatestPublishedVersion，并运行最新具体发布版本；其同步接口不支持原生 WorkflowInterrupted，P5 中断/恢复由 LangGraph负责。验收应保存真实 execute_id、合法输出与 Go来源校验。

Coze PAT 只由 Go→Coze 客户端持有，不交给 Python或模型。用户 Bearer、Agent JWT 和服务密钥只用于受控运行时 client/context，生产 Agent Compose固定关闭 LangSmith/LangChain tracing；任务、checkpoint、模型输入和安全日志不得保存这些凭据。

## 部署与重新认证

默认镜像为 `qingyingjin-banking-service:module13` 与 `qingrongjin-banking-agent-service:p5`。沿用现有 Banking数据库、卷、网络、端口和 Coze overlay，禁止 down -v/reset。Go源构建 helper 的历史默认 tag 未自动改变，因此实际构建需显式传入 module13：

```powershell
./deploy/banking-agent/Setup-P5.ps1
./deploy/banking/Build-Image.ps1 -ImageTag qingyingjin-banking-service:module13
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml config --quiet
# 新镜像备妥后，仅更新应用，不运行 Banking迁移：
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml up -d --no-deps banking-service
```

Agent 独立配置与升级步骤见 P5.md。Go继续只读 rootfs/非root运行，Python保持非root、只读 rootfs、cap_drop=ALL、no-new-privileges。服务密钥可选配置不代表可跳过桥鉴权；未配置时 Python任务运行返回依赖错误。

重启后 Python服务没有保存用户 token 的 worker 自动续跑。用户重新提供有效 JWT，读取同一 lg-任务与最新 version，通过 run 或 PREPARE_ONLY resume 继续；每次委托只签发新的短期 Agent JWT。健康/readyz 只检查所声明的服务依赖，不能替代真实 Coze模型调用验收。

## 验收范围与仍需补齐的证据

本稿依据实际源代码/API/迁移整理，不填写未核实的通过数量。Go桥双鉴权、五角色委托、非法命名空间/身份覆盖、Skill/mode拒绝、无execute路由、超时与错误输出已有测试入口；Python具有图、checkpoint、RLS、journal、租约、确认与恢复测试入口。测试代码存在不等于实测通过，结果必须引用本次日志和退出码。

后续总交付需核对：Go构建与回归、真实 PostgreSQL租户隔离与 pending writes、真实HTTP的并行/依赖/确认/取消/超时、进程与容器重启恢复、旧租约 fencing、非幂等未知结果停止、凭据不进入持久化/日志、镜像源码哈希及金融状态前后对比。Coze已实际恢复并切换DeepSeek，五角色真实模型执行ID与输出/证据验收已完成；错误输出、超时与恢复回归见本轮测试记录。

`banking-agent-service/scripts/verify_p5_live.py --models` 需模型真正可用，不能使用 fake runner 或配置标记替代。其普通验收入口包含由运维测试身份通过原网关 authorize/execute 初始化隔离、零资金目标的夹具步骤；该行为不属于 P5服务权限，也不能把它描述成纯只读验收。

P6界面、P7金融数据迁移、自动资金授权/执行、持久化用户token后台worker与模型任意扩权均不在模块13范围。

## 本轮实际验收

Go 四个相关包与 OpenAPI 契约测试退出0，Go module13镜像已实际构建部署。FastAPI 224项回归、真实数据库/HTTP/SSE/重启/重复恢复及DeepSeek四专业角色与Supervisor模型调用均通过，余额和版本保持不变。完整日志、命令、退出码及镜像/源码核验见 ../banking-agent-service/output/p5/acceptance.json；架构与curl见 ../banking-agent-service/P5.md。

模型现为DeepSeek deepseek-flash，原生model_id=100003；7个既有工作流ID保留、发布新绑定版本。旧农业业务源码与路由已清理并重新编译运行；不修改金融Core或数据库账本。旧Qwen2.5-1.5B三个模型文件及parts/parts4下载缓存已删除，已逐项核实不存在；当前工作流使用DeepSeek。
