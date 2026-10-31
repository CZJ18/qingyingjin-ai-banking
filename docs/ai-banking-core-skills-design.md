# 轻盈金 AI Banking 六个核心 Skill 设计

版本：设计稿 v0.3｜日期：2026年10月11日｜状态：规划实现，未接入运行时。

Goal Compilation、Task Planning及网关侧Execution Preparation采用用户指定方向；其余三项根据当前项目能力提出。本文配合最小可行架构方案使用，不表示已有Skill Layer。原有业务能力与待新增技能契约分别说明。

## 统一约定

- Agent理解任务、选择允许的领域Skill、解释结果；领域Skill组织规则和受控工具步骤；Tool访问固定API或已有服务适配器。Execution Preparation由Trusted Execution Gateway持有，作为跨Agent的操作准备能力；调用者权限不会因网关持有该Skill而提升。确定性业务计算继续由Banking Service承担。
- user_id、task_id、trace_id、认证身份及授权状态由服务端上下文提供，不能由模型传入或覆盖。Skill自身没有SQL、余额更新及金融execute权限。
- 金额采用整数分，周期使用数值字段。用户示例的saving、target_amount、3_month需规范化为现有savings、savings_target_minor、period_months；转换后仍要经过Schema校验。
- 技能输出拟增加skill_id、skill_version、skill_run_id和status，业务结果复用已有类型。示例中的result只展示关键字段，完整Schema须在实施时与Go模型、OpenAPI和Coze配置同步。
- 技能声明权限与既有身份权限取交集，不能提升权限。分析或规划成功不代表交易执行成功；任务确认不等同于用户金融授权。

## 1. Goal Compilation Skill

**Skill名称：**Goal Compilation Skill；ID：goal_compilation；首版版本：1。

**所属Agent：**Supervisor Agent。

**功能目标：**将自然语言金融目标转换为规范化目标和具有约束、缺失信息及冲突说明的编译计划。例如“三个月存3000元，同时保持健康生活”，不能在收入未知时直接判断方案可执行。

**输入参数：**用户原始text、financial_context，以及已有目标的goal_id、expected_version。上下文复用goal.FinancialContext：月收入、消费预算、必要开支及可选budget_id；未知金额必须保留为空。创建与更新严格复用现有接口要求。

**输出结构：**技能封装goal.View，其中包含Goal、GoalVersion及TaskPlan；解析部分复用goal.Parsed，计划部分复用goal.Plan。关键字段示例：

```json
{
  "skill_id": "goal_compilation",
  "skill_version": 1,
  "status": "awaiting_clarification",
  "result": {
    "parsed": {
      "goal_type": "savings",
      "savings_target_minor": 300000,
      "period_months": 3,
      "currency": "CNY"
    },
    "missing_information": ["monthly_income_minor", "spending_budget_minor", "essential_expenses_minor"],
    "financial_execution_enabled": false
  }
}
```

**核心处理逻辑：**校验输入→复用goal.Parser调用已配置Workflow→校验goal.Parsed→调用goal.Compile进行整数计算与预算保护→保存版本、证据和TaskPlan。保留正常饮食、住房、医疗等必要生活约束；月储蓄推导值为建议，不形成资金执行授权。

**调用工具：**复用目标创建、/goals/{id}/parse及/goals/{id}/plan的受控API适配，或现有goal.Service窄接口。目标服务继续承担数据库读写，Skill不接收数据库连接。现有Goal Compiler与Supervisor plan_instruction是不同路径，不能互相冒充。

**权限边界：**PLAN；仅解析本人目标和本人预算快照，允许保存编译结果。不能创建资金交易、削减预算预留或授予金融执行权限。

**异常处理：**输入非法返回INVALID_ARGUMENT；模型或输出无效返回COZE_UNAVAILABLE/MODEL_OUTPUT_INVALID；缺失信息输出awaiting_clarification；存在预算或目标冲突输出conflict并保持相关任务blocked。并发版本及未知解析结果沿用现有错误和状态，不无限重试模型解析。

**现有基础与新增部分：**Parser、Compile、目标版本与计划已有；Skill注册、封装及技能审计待新增。

## 2. Task Planning Skill

**Skill名称：**Task Planning Skill；ID：task_planning；首版版本：1。

**所属Agent：**Supervisor Agent。

**功能目标：**依据Goal Compilation结果生成可调度金融任务计划，完成任务拆解、Agent选择、Skill选择、依赖分析及顺序/并行和失败重试策略设计。“可调度”不等于已获准执行资金操作。

**输入参数：**本人task_plan_id与来源版本，或服务端已校验的编译目标；bindings中的查询期间、account_id、financial_plan_id；服务端可见的技能目录。不得相信客户端自行声明goal_verified=true。暂缺查询期间或规划ID时标注待补充。

**输出结构：**复用TaskSpec任务数组，拟增加skill_id、skill_version；附调度策略和来源计划引用。以下为新增契约示例，当前API尚未支持技能字段：

```json
{
  "skill_id": "task_planning",
  "skill_version": 1,
  "status": "planned",
  "result": {
    "strategy": "parallel_read",
    "max_attempts": 2,
    "timeout_ms": 30000,
    "tasks": [
      {
        "key": "billing_review",
        "agent_type": "billing",
        "skill_id": "billing_analysis",
        "skill_version": 1,
        "tool": "generate_billing_report",
        "parameters": {"from": "2026-10-01", "to": "2026-10-11"},
        "dependencies": [],
        "priority": 100,
        "execution_mode": "read_only"
      },
      {
        "key": "subscription_review",
        "agent_type": "subscription",
        "skill_id": "subscription_analysis",
        "skill_version": 1,
        "tool": "detect_subscriptions",
        "parameters": {"from": "2026-10-01", "to": "2026-10-11"},
        "dependencies": [],
        "priority": 100,
        "execution_mode": "read_only"
      },
      {
        "key": "savings_plan",
        "agent_type": "savings",
        "skill_id": "savings_planning",
        "skill_version": 1,
        "tool": "replan_financial_plan",
        "parameters": {"plan_id": "42"},
        "dependencies": ["billing_review", "subscription_review"],
        "priority": 90,
        "execution_mode": "advice"
      }
    ],
    "financial_execution_enabled": false
  }
}
```

其中plan_id=42仅为示例，必须由真实授权资源绑定，不能由模型编造。完整输出还需包含skill_run_id、来源引用和规则验证状态。

**核心处理逻辑：**读取并核验目标版本→保留缺失信息、冲突及protected_blocked→按已注册能力生成候选任务→校验Agent/Skill/Tool/模式一致→调用现有ValidateTasks验证1—32个任务和无环依赖→生成现有Scheduler能执行的计划。旧Goal计划可复用fromGoalPlan确定性映射；结构化目标供模型规划需要新增窄适配，现有PlanWithEvidence只接收instruction和bindings，不能声称已经支持此输入。

技能只生成策略；任务持久化、顺序执行、最多四个只读任务并行及失败重试继续由Orchestrator/Scheduler承担。max_attempts采用现有1—3次边界，timeout_ms采用100—60000ms边界；只有只读步骤允许对现有可恢复错误重试。业务写入或意向超时进入review_required。禁止递归plan_instruction和伪造自动任务保留键。

**调用工具：**现有TaskPlan读取、fromGoalPlan、ValidateTasks及Supervisor Workflow适配。plan_instruction作为Task Planning的现有调度入口接入技能校验；request_confirmation与require_gateway仍是控制操作，不增加领域Skill，也不能被规划器用作资金执行工具。

**权限边界：**PLAN；只能创建本人分析、建议或受控准备任务。不能任意选择Agent、注册Skill、修改重试上限、绕过任务确认或标记金融授权已完成。

**异常处理：**来源版本变化返回VERSION_CONFLICT；未知Skill/工具、越权模式、循环依赖、递归任务返回INVALID_TASK_PLAN；缺少参数输出awaiting_input；来源conflict或blocked必须继承阻塞。模型失联不生成猜测任务，也不自动改为执行资金操作。

**现有基础与新增部分：**任务规划、计划映射、依赖校验和Scheduler已有；结构化编译目标的统一技能适配、技能选择和版本校验待新增。

## 3. Billing Analysis Skill

**Skill名称：**Billing Analysis Skill；ID：billing_analysis；首版版本：1。

**所属Agent：**Billing Agent。

**功能目标：**生成具有交易证据的收支分析、分类趋势、预算对比和疑似异常报告，识别可供用户审核的节省机会。

**输入参数：**复用billing.Input：from、to、timezone、可选account_id、limit和offset。查询用户来自认证上下文；分析时间和分页规范由billing.Normalize校验。

**输出结构：**技能封装billing.Report，保留report_id、analysis_period、currency、total_income、total_expense、category_breakdown、budget_status、anomalies、potential_savings、evidence_transaction_ids和audit_id。total_income/total_expense沿用项目字段，但单位明确为整数分；financial_execution_enabled保持false。

**核心处理逻辑：**获取本人期间交易→调用现有后端分析→比较预算及必要支出预留→检查异常证据→输出报告和解释。保留报告的scope，不把分页样本冒充完整账单；疑似重复扣款不等于确认损失，节省候选不等于可用余额。复用后端分析器，不在Skill或模型重新计算另一套金额。

**调用工具：**generate_billing_report；按任务需要调用get_transactions、get_account_summary、analyze_spending、compare_budget、detect_anomalies。首版优先使用完整报告工具，避免每次重复调用全部分析API。多步骤复用现有任务DAG。

**权限边界：**READ及确定性分析；允许保存报告，不修改原交易、账户余额或预算规则。预算保护作为本Skill规则输出供Savings复用，不新增独立Budget Skill。

**异常处理：**时间、分页或金额非法返回INVALID_ARGUMENT；资源不属于用户拒绝或返回NOT_FOUND；无流水返回真实空结果；依赖失败按只读有界重试。输出归属、Schema或证据异常返回TOOL_OUTPUT_INVALID/MODEL_OUTPUT_INVALID。

**现有基础与新增部分：**账单工具和报告已有；技能契约、受控组合与技能审计待新增。

## 4. Subscription Analysis Skill

**Skill名称：**Subscription Analysis Skill；ID：subscription_analysis；首版版本：1。

**所属Agent：**Subscription Agent。

**功能目标：**识别周期性订阅、评估成本、价格变化及可能重复或闲置情况，生成保留、核验或取消建议，不自动取消。

**输入参数：**复用subscription.DetectInput的from、to及account_id；明细查询使用subscription_id。取消准备所需expected_version由服务端最新订阅明细取得，不能从分析建议推造。

**输出结构：**技能封装subscription.Report/Detail，保留subscriptions、duplicate_groups、suggestions、total_recorded_expense_minor、active_recurring_forecast、cancelled_avoided_spend、evidence_transaction_ids及audit_id。建议包含subscription_id、action、reason和requires_user_confirmation；financial_execution_enabled=false。

**核心处理逻辑：**检测本人订阅及支付证据→分析周期和成本→根据真实使用证据判断可能闲置→生成可追溯建议。没有使用信息时维持unknown，不把“无记录”当作闲置事实；取消后的预估少支出不能算已到账资金。

**调用工具：**detect_subscriptions、get_subscription_details、analyze_subscription_cost、suggest_subscription_actions、get_subscription_status。取消建议提交网关侧Execution Preparation；create_cancel_intent继续作为现有取消准备兼容入口，不加入分析Skill的工具白名单。

**权限边界：**READ及分析；允许保存识别和报告结果，不直接变更订阅取消状态、不调用确认接口、不执行商户真实代扣取消。

**异常处理：**查询归属或ID异常沿用接口错误；无可靠周期证据返回候选或待核验；缺少使用证据不输出确定性闲置结论；数据版本变化要求重新查询；外部不可用进行只读有界重试，不能转为自动取消。

**现有基础与新增部分：**订阅识别、成本及建议工具已有；分析与操作准备的Skill隔离待新增。

## 5. Savings Planning Skill

**Skill名称：**Savings Planning Skill；ID：savings_planning；首版版本：1。

**所属Agent：**Savings Agent。

**功能目标：**在已确认目标、实际余额、现金流、必要生活支出与安全储备范围内生成或更新可解释的资金规划。

**输入参数：**首版复用planning.IDInput的plan_id，以及后端已验证的依赖证据引用。规划设置使用该用户已保存的Settings，包括账户、目标、生活与必要开支、安全储备、现金缓冲和现金流。当前专业Agent没有任意创建规划的工具；无plan_id时引导用户通过既有规划入口补齐，不能虚构计划ID。

**输出结构：**技能封装planning.View：plan、version、result、constraints、decisions与financial_execution_enabled=false。version保留data_digest、snapshot和audit_id；金额字段复用现有planning.Result。冲突与缺失信息必须通过明确状态表示，不用模型文字替换结构化结果。

**核心处理逻辑：**核验本人规划→读取后端权威快照→复用planning.Calculate/Replan确定性计算→保留生活、安全储备及预算保护→输出版本、约束与建议。账单/订阅依赖仅作为解释证据，当前计算继续读取真实后端快照，不把Agent报告直接当作可信余额输入。取消订阅预测少支出和未来收入不当作实际可转出资金。

**调用工具：**get_financial_plan、replan_financial_plan、compare_financial_plans。当前编排HTTPExecutor的savings分支主要支持plan_id查询与重规划；完整比较Skill步骤需补齐from/to版本参数适配，不能把只返回当前规划冒充版本比较。首版最低路径只使用查询与重规划。

**权限边界：**PLAN；允许保存规划版本，不自动扣款、转账、调整账户余额或自动选定用户规划决定。预算保护规则读取并复用，不修改预算规则。

**异常处理：**缺plan_id或关键设置返回INPUT_REQUIRED/awaiting_input；版本、归属、数据摘要异常拒绝；不可实现的目标输出约束或冲突，不降低必要支出兜底；重规划超时保持现有review_required语义，先查询版本再决定后续处理。

**现有基础与新增部分：**规划引擎、持久化版本和重规划已有；技能输入输出适配、审计及可选比较步骤待新增。

## 6. Execution Preparation Skill

**Skill名称：**Execution Preparation Skill；ID：execution_preparation；首版版本：1。它是确定性后端能力，不是新增大模型Agent。

**所属Agent：**不隶属于任何业务Agent；所属组件为Trusted Execution Gateway。Subscription、Savings和Supervisor等仅能在各自权限范围内提交操作建议，不能直接取得该Skill的内部调用权限。

**功能目标：**集中整理操作参数、核验资源归属和操作类型、生成持久化意向及待确认请求。对外形成规范Action Proposal，供用户核对；不执行金融操作。统一契约为订阅取消、模拟转账、储蓄操作及未来方案调整提供扩展位置。

**输入参数：**操作kind、严格类型化parameters、幂等键及可选expires_in_seconds，复用execution.PrepareInput；用户身份、来源Agent、技能运行ID和任务关联由服务端提供。原有编排取消入口仍使用subscription.IntentInput的subscription_id、expected_version，租约和InvocationKey由Scheduler提供，Gateway.Propose核验持久化状态。模型提供的confirmed=true、风险等级或授权ID不能形成可信授权。

**输出结构：**拟新增统一Action Proposal封装，内部复用execution.View的operation：操作ID、类型、服务端参数与资源快照、金额与币种、摘要、nonce、有效期和pending_authorization状态；附用户确认要求及准备阶段检查状态。完整风险结果仍需执行阶段复核。现有编排Propose只返回SubscriptionActionIntent，统一视图需要增加本人操作联查适配，不能宣称已经完成。

```json
{
  "skill_id": "execution_preparation",
  "skill_version": 1,
  "status": "pending_authorization",
  "result": {
    "proposal_type": "action_proposal",
    "operation_kind": "cancel_subscription",
    "operation_status": "pending_authorization",
    "requires_user_authorization": true,
    "execution_risk_assessment": "pending_recheck",
    "financial_execution_enabled": false
  }
}
```

网关操作状态pending_authorization与旧订阅意向状态pending_approval区分。示例省略真实资源ID与摘要；实际输出必须包含服务端生成的操作ID、绑定摘要、确认nonce、参数快照和有效期，不能仅返回一段待确认文字。

**核心处理逻辑：**业务Agent及其领域Skill生成操作建议→通过受控Tool提交到网关侧Execution Preparation→校验来源权限、操作白名单、参数和资源版本→幂等创建意向→返回规范Action Proposal及确认请求→用户明确确认→执行阶段Risk Assessment复核最新状态→Execution Gateway调用确定性业务服务执行并审计。准备阶段可做基础合法性检查，但不能代替执行前风险复核。

当前订阅编排额外要求用户先确认“准备取消意向”，Gateway.Propose仍检查本人运行任务、有效租约、确认状态、参数和执行记录。此设计保留该前置要求，不把它与最终金融授权合并；若未来精简确认次数，需要另行修改流程及测试。

**调用工具：**网关内部使用Prepare及限定本人资源的Get等窄适配，不提供execute方法。现有Agent编排先通过create_cancel_intent/ProposalGateway接入；用户端通用prepare_operation或/api/v1/execution/operations沿用原有鉴权接入。授权和执行接口由后续独立用户确认及执行流程调用，不作为本Skill的工具。

**权限边界：**PREPARE；所属网关不代表具有网关EXECUTE权限，只能创建意向和待确认请求。有效权限取调用者、Agent、操作类型和资源归属的交集。Subscription不能因接入统一Skill而准备任意转账；Savings不能自行授权资金方案。操作幂等、最终用户授权、风控和金融审计继续由网关分别控制。

首版Agent开放范围保持已支持的订阅取消准备；网关用户API已有模拟转账、储蓄存取等prepare能力，可复用统一封装。将这些操作开放给其他Agent、增加储蓄方案调整或资金方案确认适配均为规划实现，需逐操作定义角色与确认语义。仅确认规划建议不等于授权任何资金交易。

**异常处理：**身份、操作权限或资源归属非法拒绝；Agent编排无持久化确认或有效租约拒绝；缺网关返回GATEWAY_UNAVAILABLE；参数、摘要或资源版本变化需重新核验；同幂等键不同参数拒绝。准备超时后查询原意向，不能换新键自动创建；用户拒绝或意向过期不能自动授权续期。执行阶段风险拒绝由执行网关记录，不伪装为准备成功后必然可执行。

**现有基础与新增部分：**通用Prepare/View、订阅ProposalGateway和最终用户授权执行已有；网关侧Skill契约、统一Action Proposal、技能绑定和技能审计待新增。Gateway持有的准备Skill与Gateway执行器分离，准备Skill不得取得执行句柄。

## 架构接入及验收补充

第一版保持现有Scheduler、Executor和领域服务，新增技能注册与受控ToolInvoker，任务及审计绑定技能ID、版本、运行ID。Task Planning替换旧草案中的独立Budget Protection项；预算规则复用，不丢弃其安全约束。

Task Planning的plan_instruction特殊调度路径必须接入技能校验，不能只给HTTPExecutor加拦截。Goal Compilation独立目标API也需接入，后端自动生成的订阅任务使用同一注册表校验。无技能记录的合法旧任务按唯一映射兼容；非法、歧义或未知版本明确拒绝或暂停审核。

验收重点：结构化目标输入与版本绑定、缺失信息不被丢弃、只读并行和有界重试、Agent/Skill/Tool错配拒绝、准备确认不能冒充金融授权、重启后技能版本一致、每个工具步骤有审计证据。真实Coze、数据库并发和迁移测试分别报告实际状态。

代码依据：

- [Goal解析与编译](/D:/智慧农业/services/banking-service/internal/goal/service.go:75)、[编译规则](/D:/智慧农业/services/banking-service/internal/goal/compiler.go:17)
- [任务规划特殊路径](/D:/智慧农业/services/banking-service/internal/orchestrator/service.go:433)、[Scheduler](/D:/智慧农业/services/banking-service/internal/orchestrator/scheduler.go:32)
- [工具策略](/D:/智慧农业/services/banking-service/internal/orchestrator/graph.go:20)、[Executor](/D:/智慧农业/services/banking-service/internal/orchestrator/executor.go:63)
- [账单输入输出](/D:/智慧农业/services/banking-service/internal/billing/model.go:13)、[订阅契约](/D:/智慧农业/services/banking-service/internal/subscription/service.go:41)
- [规划契约](/D:/智慧农业/services/banking-service/internal/planning/model.go:1)、[取消准备安全核验](/D:/智慧农业/services/banking-service/internal/execution/orchestrator.go:18)
