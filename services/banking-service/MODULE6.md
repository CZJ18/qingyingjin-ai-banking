# 模块6：Adaptive Savings 动态资金规划

模块6在独立 Banking Service 中计算、存储资金建议，不执行转账。Coze Savings Agent 通过受保护的 HTTP 工具获取计划，LLM 解释结果；资金、预算和多目标分配全部由 Go 整数代码计算。

## 目录与依赖

| 层 | 目录或文件 | 依赖与用途 |
| --- | --- | --- |
| Entity / Engine | internal/planning/model.go、engine.go | 统一实体 Base、整数分、确定性多目标分配 |
| Repository / Service | internal/planning | MySQL、模块2账户/储蓄账本、模块3目标、模块4账单、模块5订阅事实与分析 |
| API | internal/httpapi/planning.go | 既有 JWT、工具身份绑定、trace/task ID、错误封装 |
| Migration | internal/migration/005_planning.sql | 新增5表及事务事件触发器，保留001—004校验和 |
| Contract | api/openapi.yaml、api/coze-plugin.openapi.yaml | OpenAPI 3；工具响应显式展开类型，兼容当前 Coze 插件适配器 |
| Agent / Workflow | ../../coze/banking/savings-agent | 原生Agent配置参考、Prompt、Start→Plugin→End生成器 |

FinancialPlan保存当前设置和版本号；PlanVersion保存快照、结果、旧版本号、事件、原因、建议金额影响、数据摘要、重新确认标识及审计关联；PlanConstraint保存本版本的保护约束；ReplanEvent保存可重试事件；PlanDecision只记录用户选择的建议。

不新建前端，不修改Coze原生Agent、Workflow、Plugin或模型管理引擎。MySQL保存所有事实、版本和事件；Redis沿用模块1连接和可用性检查，不作为资金事实来源。

## 确定性资金规则

周期使用UTC自然月。Settings.period省略时由服务记录创建或更新时的月份；跨月旧设置不能直接用于非零建议，需要更新生活支出预留。account_ids只选用户授权的普通账户，已执行目标金不重复作为可支配现金。

输入 living_remaining_minor、essential_remaining_minor 为本期尚需保留的生活与必要支出，不是月初总额。缺少可靠生活或必要支出依据时阻止非零储蓄建议，不把未知开支当作0。生活预留取用户设置、目标/预算强制底线及必要预留的最大值，避免削减正常饮食与生活需求。

Goal Compiler中的mandatory自然语言约束不等于已经通过确定性验证。当前无法自动证明这些文字条件成立时，保留其原文与来源并将储蓄上限设为0，要求用户补充可验证条件；不由LLM声称条件已经满足。缺失信息、未解析目标和未验证强制约束都不能被模型忽略。

所选生活目标中明确的储蓄月额或目标总额必须与所选储蓄目标的总月额或总目标匹配；不匹配时保留原目标并阻止非零建议，提示用户核实绑定与目标。引擎不能静默改写自然语言目标金额。

cashflows.kind=necessary_expense表示生活预算之外的**额外**必要义务；不要把已经包含在生活预留中的同一笔费用再次填写。订阅实际待付义务独立保护。安全预留和现金流缓冲也必须保留。

`income_risk_reserve_minor = max(0, baseline_income_minor - future_income_minor)`。已知未来收入只用于收入降幅风险与说明，不加到当前余额。用户明确的future_income_minor优先；否则读取本期已知income现金流。不要同时填重复未来收入；预期没有减少不代表资金已经到账。

`disposable_minor = max(0, balance_minor - living_reserve_minor - additional_obligations_minor - safety_reserve_minor - cashflow_buffer_minor - income_risk_reserve_minor)`。

每个储蓄目标本期需求不超过目标剩余金额及月度目标减已执行本期存入记录。`savings_cap_minor = min(disposable_minor, target_demand_minor)`；存在阻断条件时上限为0。超出安全容量的目标需求通过conflict_minor和每个分配的unmet_minor表达，不修改用户原目标。

保守、平衡、积极方案分别使用安全储蓄上限的50%、75%、100%，整数分向下取整。多目标按实际剩余需求比例分配，通过大整数计算乘积并按余数排序分配剩余分；同余时按目标ID稳定排序。上限为0时仍返回三种0元建议。

`reserve_shortfall_minor`单独报告现有余额无法覆盖全部保护资金的缺口。存在该缺口或未澄清阻断条件时三个方案均`feasible=false`，0元建议表示停止进一步占用生活资金，不声称当前现金能覆盖所有必要开支。`conflict_minor`则是储蓄目标需求与安全容量的差额，两种缺口不能混用。

活动订阅按实际周期从next_billing_at累计到本期月底；过期待付证据未核实前保守保留。最近支付观测涨价时保护较高金额并保留证据，需要核实的降价不自动降低已采用义务。

订阅取消的future_avoided_spend_minor单独说明，绝不增加账户余额、可储蓄上限或已完成进度。餐饮消费金额不作为健康诊断；体重与限制饮食不参与奖励或处罚。

## 事务事件与版本

005迁移建立MySQL AFTER INSERT/UPDATE触发器，在原交易事务中写入ReplanEvent：账户变更、交易新增、预算/必要支出变化、生活目标/解析版本变化、储蓄目标/执行流水、订阅状态/扣费证据变化均进入持久队列。源事务回滚时事件同时回滚；不用内存队列代替持久化。

消费者按队列状态、重试时间读取事件，锁定事件及用户计划，生成新快照和不可变版本，再标记处理结果。事件唯一键及`unique(plan_id,event_id)`防止同一事件重复生成该计划版本。失败保留错误与重试信息，服务重启可以继续处理。一个事务可能产生多个不同来源事件，每个事件保留自身来源原因，不能把这些不同事实误称为重复投递。

POST事件中的resource_id是来源提示字符串，reason是通知说明，不是金融事实或资金数值。用户通知只能触发读取自身真实持久数据重新计算，不能用通知内容伪造余额、收入到账、订阅取消或储蓄进度。自动事件缺少原关联ID时使用稳定的replan-event-ID标识任务，并由版本event_id及snapshot.sources关联真实来源。

每个版本保留原快照/结果，previous_version指向前版本，impact_minor记录建议变化。数据摘要绑定当前快照；用户选择必须提供准确版本和摘要。需要重新确认的新版本不继承旧版本选择。即使用户选择方案，也只是确认建议，不消费转账授权、不修改已完成交易、不生成自动资金操作。

## API

| 方法与路径 | 输入 | 结果 / 身份 |
| --- | --- | --- |
| POST /api/v1/financial-plans | CreateInput：name、settings | 201 View；用户JWT |
| GET /api/v1/financial-plans/{id} | 路径计划ID | 当前View；用户JWT |
| GET /api/v1/financial-plans/{id}/versions | 路径计划ID；limit默认100、范围1—100；offset默认0、范围0—1000000 | 按版本降序分页历史PlanVersion数组；用户JWT |
| PUT /api/v1/financial-plans/{id}/settings | expected_version、settings | 新View；用户JWT |
| POST /api/v1/financial-plans/{id}/decisions | expected_version、data_digest、option、confirm_advice | 选择记录；用户JWT，只有建议确认 |
| POST /api/v1/replan-events | type、resource_id、reason；Idempotency-Key | 201持久事件；用户JWT |
| PUT /api/v1/savings/goals/{id}/settings | expected_target_minor、expected_monthly_minor、expected_ends_at；新target_minor、monthly_minor、ends_at | 三个旧值CAS校验，用户自行更新储蓄目标；用户JWT，无资金变化 |
| POST /tools/v1/savings/get_financial_plan | plan_id字符串 | 200当前View；固定用户工具Key |
| POST /tools/v1/savings/replan_financial_plan | plan_id字符串 | 200重新计算View；固定用户工具Key |
| POST /tools/v1/savings/compare_financial_plans | plan_id字符串 | 200 View.options中三方案；固定用户工具Key |

金额为整数分；单资源ID为规范正整数字符串，settings中的ID数组为JSON整数数组。Settings的可空数值若缺少则省略，不向Coze输出primitive null。所有工具只读事实或重新计算建议，不能更新设置、选择方案或确认资金操作。

错误沿用统一Envelope：INVALID_ARGUMENT、UNAUTHENTICATED、FORBIDDEN、NOT_FOUND（含越权资源）、VERSION_CONFLICT、STATE_CONFLICT、IDEMPOTENCY_CONFLICT、RISK_REJECTED、ANALYSIS_LIMIT、DEPENDENCY_UNAVAILABLE。金融安全约束不应通过模型提示绕过。

## curl示例

以下仅为调用模板；替换为本机真实JWT、账户/目标/计划ID。模板不代表真实执行结果。不要在日志或版本控制提交凭据。首次可通过模块2创建模拟账户和储蓄目标，模块3/4/5沿用原API生成目标与分析事实。

```powershell
$settings = @{
  account_ids = @([long]$AccountID)
  savings_goal_ids = @([long]$SavingsGoalID)
  life_goal_ids = @()
  living_remaining_minor = 100000
  essential_remaining_minor = 60000
  safety_reserve_minor = 30000
  cash_buffer_minor = 10000
  baseline_income_minor = 0
  future_income_minor = 0
  cashflows = @()
}
$body = @{ name = '正常生活优先的健康目标金'; settings = $settings } | ConvertTo-Json -Depth 8
$body | Set-Content -LiteralPath ./plan-input.json -Encoding utf8
curl.exe -X POST http://127.0.0.1:8090/api/v1/financial-plans -H "Authorization: Bearer $UserJWT" -H 'Content-Type: application/json' -H 'X-Trace-ID: savings-demo' -H 'X-Task-ID: module6-create' --data-binary '@plan-input.json'
curl.exe "http://127.0.0.1:8090/api/v1/financial-plans/$PlanID/versions" -H "Authorization: Bearer $UserJWT"
@{plan_id="$PlanID"} | ConvertTo-Json | Set-Content -LiteralPath ./plan-tool-input.json -Encoding utf8
curl.exe -X POST http://127.0.0.1:8090/tools/v1/savings/compare_financial_plans -H "X-Banking-Tool-Key: $BankingToolKey" -H 'Content-Type: application/json' -H 'X-Trace-ID: savings-demo' -H 'X-Task-ID: module6-compare' --data-binary '@plan-tool-input.json'
@{expected_version=[long]$PlanVersion; data_digest="$DataDigest"; option='balanced'; confirm_advice=$true} | ConvertTo-Json | Set-Content -LiteralPath ./plan-decision.json -Encoding utf8
curl.exe -X POST "http://127.0.0.1:8090/api/v1/financial-plans/$PlanID/decisions" -H "Authorization: Bearer $UserJWT" -H 'Content-Type: application/json' --data-binary '@plan-decision.json'
```

读取结果中version.version与version.data_digest后再提交decisions；旧快照或版本冲突需要重新读取并由用户再确认。

版本历史完整保存在MySQL，不因单次查询限制100条而删除。查询`/versions?limit=100&offset=100`可读取下一页；自动重新规划可能增加新版本，分页显示以查询时记录为准。

## 启动、文档生成与验证

```powershell
# 仓库根目录；使用既有隔离模拟数据库与Compose
./deploy/banking/Build-Image.ps1 -LocalBinaries
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d --no-build
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz
python services/banking-service/scripts/generate_core_openapi.py

cd services/banking-service
go test ./...
$env:BANKING_TEST_ENV_FILE = 'D:\智慧农业\deploy\banking\.env.local'
go test -tags integration ./tests/integration -count=1
```

迁移005包含触发器，需要数据库迁移账号有TRIGGER权限；运行账号沿用既有配置。专用本地模拟MySQL的Compose启动参数加入`--log-bin-trust-function-creators=1`，保留默认ROW binlog，用于允许schema账号创建事务事件触发器。首次迁移实际遇到MySQL1419后已调整配置并重启；该实例只承载隔离模拟业务。使用外部MySQL时应由数据库管理员按其权限和binlog策略配置触发器部署，不把本地模拟参数直接作为生产默认。真实执行结果以交付记录为准，以上命令说明不冒充已通过测试。

## Coze对接及后续模块

在Coze原生Plugin管理界面配置现有插件OpenAPI，工具Key放原生密钥设置，固定身份绑定沿用模块1。实际Coze服务器应使用可访问Banking Service的内部地址；Docker内服务地址与宿主机loopback不同。

使用`coze/banking/savings-agent/agent-config.yaml`与`system-prompt.md`配置原生Agent。发布后的真实Plugin ID、API ID、插件名和版本由操作者填写到生成器；生成器只读取当前源码原生Plugin示例，复用模块5的适配逻辑，不声称存在自动导入或发布API。具体命令见该目录README。

Supervisor通过配置的原生Agent或发布后的工具Workflow读取同一用户计划。模块7可使用版本、数据摘要、约束及证据协调建议；模块8必须另行创建、绑定并验证明确用户授权后才允许模拟资金执行。计划选择不是资金授权。

尚需在真实Coze实例配置模型、导入/配置Plugin与Workflow并发布后完成Agent端到端验证。当前没有发布ID或模型凭据，参考配置和fixture验证不等于真实已部署Agent。
