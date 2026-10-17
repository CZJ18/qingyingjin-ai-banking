# 模块4 Billing Agent

Go账单分析后端已实现；Coze原生Agent Prompt、Plugin OpenAPI、路由Workflow参考配置已提供。当前未运行Coze/配置Billing workflow_id，真实Agent发布及模型对话联调未完成；数据分析API可独立运行，不生成假AI结果。

## 工程与启动

`internal/billing`拆分model、repository、analyzer、schema、parser、service和测试；`internal/httpapi/billing.go`暴露API；`003_billing.sql`新增banking_billing_reports，JSON报告与原有审计同事务保存，账户/交易/预算表复用且只读。新增Go依赖为零，沿用模块3JSON Schema库。服务支持UTC/Asia/Shanghai，内嵌time/tzdata以适配distroless。

```powershell
# 从仓库根目录，保留已有.env与数据卷
./deploy/banking/Build-Image.ps1 -LocalBinaries
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d --no-build banking-service
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz
```

镜像为qingyingjin-banking-service:module4。Compose运行migrate先校验001/002并应用003，就绪检查要求003。没有删除数据卷。compose.test.yaml仅为本机集成发布数据库端口；正常部署可只用compose.yaml。

本机CLI迁移：

```powershell
Push-Location services/banking-service
go run -buildvcs=false ./cmd/migrate -env-file ../../deploy/banking/.env.local
Pop-Location
```

## API与契约

六个POST工具：get_transactions、get_account_summary、analyze_spending、compare_budget、detect_anomalies、generate_billing_report。用户路径前缀 `/api/v1/billing/`，要求用户JWT；Coze路径前缀 `/tools/v1/billing/`，要求服务端绑定用户的X-Banking-Tool-Key。所有工具复用同一权威分析引擎，返回统一Report，便于Agent之间传递，不应把重复工具返回的合计相加。

统一字段为user_id、analysis_period、total_income、total_expense、category_breakdown、budget_status、anomalies、potential_savings、evidence_transaction_ids。还包含report_id、transactions、transaction_count、accounts、daily/weekly/monthly、trend、findings、snapshot_at、audit_id、trace_id、task_id、financial_execution_enabled=false。所有金额单位为整数人民币分。

| 接口 | 行为 |
|---|---|
| POST {前缀}/get_transactions | 实际流水明细、完整期间合计及证据；明细分页不影响合计 |
| POST {前缀}/get_account_summary | 授权账户余额投影/状态及账单口径说明 |
| POST {前缀}/analyze_spending | 分类、每日/每周/每月及前期等长趋势 |
| POST {前缀}/compare_budget | 完整UTC月所有用户账户的真实预算使用与结余 |
| POST {前缀}/detect_anomalies | 未知分类、疑似重复、大额和偏离前期中位数的待核查事件 |
| POST {前缀}/generate_billing_report | 综合报告和带交易证据的findings，持久化报告 |
| GET {前缀}/reports/{uuid} | 按身份读取已保存报告供Supervisor/Savings使用 |
| POST /api/v1/billing/query | Coze路由自然语言问题，验证输出后由Go计算；模糊问题要求澄清 |

请求示例保存为billing.json：

```json
{
  "from": "2026-01-01T00:00:00+08:00",
  "to": "2026-03-01T00:00:00+08:00",
  "timezone": "Asia/Shanghai",
  "limit": 100,
  "offset": 0
}
```

可选account_id为正整数ID字符串，只能是当前身份所有的账户；不允许传user_id。分析期间半开且不超过366天。总计覆盖完整期间，transactions明细limit最大100、offset最大10000；载入交易（含预算完整月与趋势基线）超过10000条时返回422，要求缩短期间。

```powershell
Push-Location services/banking-service
$userToken = go run -buildvcs=false ./cmd/token -env-file ../../deploy/banking/.env.local -user-id 42
Pop-Location
curl.exe -X POST http://127.0.0.1:8090/api/v1/billing/analyze_spending -H "Authorization: Bearer $userToken" -H "Content-Type: application/json" -H "X-Task-ID: billing-task-001" -H "X-Trace-ID: supervisor-demo" --data-binary '@billing.json'
curl.exe -X POST http://127.0.0.1:8090/api/v1/billing/compare_budget -H "Authorization: Bearer $userToken" -H "Content-Type: application/json" --data-binary '@billing.json'
# 用实际report_id替换$reportId
curl.exe "http://127.0.0.1:8090/api/v1/billing/reports/$reportId" -H "Authorization: Bearer $userToken"
# query.json内容：{"question":"2026年1月我的餐饮支出是多少？","timezone":"Asia/Shanghai"}
curl.exe -X POST http://127.0.0.1:8090/api/v1/billing/query -H "Authorization: Bearer $userToken" -H "Content-Type: application/json" --data-binary '@query.json'
```

完整契约见api/openapi.yaml，运行地址/openapi.yaml；Coze插件文档为/coze-plugin.openapi.yaml。JSON Schema在internal/billing/schemas。重新生成文档运行`python services/banking-service/scripts/generate_core_openapi.py`，保留所有模块契约。

## 分类、金额和异常口径

只分析kind=external的持久交易，包括历史record_only事实，不等于余额变化；排除开户资金和内部转账。支持餐饮/交通/购物/运动/订阅/住房/医疗/教育/其他及常见中英文别名，保留original_category。未知类别不猜测商户，列入uncategorized并标记待核查；不回写账单分类，不依据饮食金额诊断健康。

日期分组按请求时区，周一开始；没有记录的期间返回0和空集合，不编造流水。趋势比较前一个相同长度窗口，基线为0时比例null/insufficient_baseline，不称为增长无穷大。比例由big.Int中间运算得到基点，金额用int64/溢出校验，无float金融计算。

预算类别按原始category精确匹配，category_breakdown的别名大类归并不自动改写预算规则。预算沿用Core的UTC整月逻辑，即使只查一天或一个账户，也显示scope=all_user_accounts_full_UTC_month；budget_status交易ID覆盖该完整月，remaining=total-spent，必要预留remaining=max(reserve-category_spent,0)，启用enforce_reserve再计算available_after_reserve。未设预算则budget_status=[]，不会默认猜出预算。

疑似重复：查询期间内同账户、非空同商户、同金额5分钟内的记录。它们可能是合法购买，两笔都计入支出，不自动退款/去重。大额阈值为100000分（声明策略，不是用户健康或欺诈判断）；存在至少5笔前期同类别消费且金额>=下中位数3倍时为unusual_amount，否则large_amount_review。每项附实际交易ID、时间、金额、原分类、商户和review_required=true，confirmed_loss=false。

potential_savings.confirmed_minor固定0；重复扣费候选只是待核查金额，requires_confirmation=true，不保证储蓄，不用于自动扣款。正常饮食和必要支出不能为了建议储蓄而削减。

## Coze Agent、Plugin与Workflow

使用当前原生能力，不新造Agent引擎。`coze/banking/billing-agent/agent-config.yaml`为配置参考，system-prompt.md约束工具选择、引用证据、不得自算金额和不得推断健康。通过Coze原生插件配置导入coze-plugin.openapi.yaml，设置实际可达banking-service地址；在服务API token设置中配置Header X-Banking-Tool-Key，不把凭证暴露为模型参数。导入后挂载实际生成的工具/插件到Billing Agent并选择本机真实模型、发布。

源码依据包括backend/application/plugin/info.go（原生OpenAPI/鉴权配置）及backend/application/workflow/workflow.go（同步运行协议）。没有假设额外公开Agent创建/工作流导入API，没有虚构plugin_id、bot_id、模型ID。

工具服务凭证绑定一个Banking用户，仅供该用户的私有Agent上下文。禁止把同一凭证共享为不同金融用户的身份；工具多用户部署须配置独立身份凭证。用户REST支持各自JWT，模型不能传user_id或绕过账户归属过滤。工具插件仅包含集成探测和账单读/分析/报告读取，没有开户、交易写入、转账、审批或取消订阅。

自然语言REST额外需要原生三节点只读路由工作流：Start必填input_string；LLM引用Start input，system=query-prompt.md加billing-query.schema.json，user={{input}}，temperature=0，JSON格式，关闭工具和历史；End用returnVariables返回字符串output。生成器复用模块3已核实画布结构：

```powershell
python coze/banking/billing-agent/generate_canvas.py --model-id $实际模型ID --model-name $实际模型名称 --out output/billing-router.canvas.json
```

生成的是工程参考，不声称存在将该JSON直接上传的公开导入API。原生界面配置/发布后，在本机.env及.env.local配置已存在的BANKING_COZE_BASE_URL/BANKING_COZE_PAT，再设置BANKING_BILLING_WORKFLOW_ID为实际已发布ID。Coze未配置时query返回503，不影响确定性账单工具。

路由输出只能包含工具、日期、时区、缺失信息和问题原文证据；Schema拒绝金额/身份/写任务及异常JSON，Go验证范围再调用只读分析。自然语言解释与报告措辞由原生Billing Agent基于工具结果完成；Banking保存的权威Report不会被模型自由文本覆盖。当前实例缺少Coze服务/PAT/workflow_id，原生Agent发布、工具真实模型调用及对话端到端联调尚未验证。

## 协同与错误

Supervisor通过原生Agent/工作流或已授权REST调用六工具，用X-Task-ID和X-Trace-ID关联。Savings Agent读取同用户report_id并复用category_breakdown、budget_status、trend和证据，不把候选异常当成已确认资金，不把报告当授权。数据改变后应生成新报告，保留旧报告snapshot_at与audit_id追溯。

错误：400 INVALID_ARGUMENT；401 UNAUTHENTICATED；403 FORBIDDEN；404 NOT_FOUND（不存在/其他用户账户或报告）；422 ANALYSIS_LIMIT、INVALID_TRANSACTION_DATA、MODEL_OUTPUT_INVALID；503 DEPENDENCY_UNAVAILABLE或COZE_UNAVAILABLE。模型错误和不明确问题不产生金融任务，所有重要分析由真实数据库证据支持。

## 测试

```powershell
Push-Location services/banking-service
go test ./... -count=1 -timeout 90s
$env:BANKING_TEST_ENV_FILE='D:\智慧农业\deploy\banking\.env.local'
go test -tags integration ./tests/integration -count=1 -timeout 120s
go vet ./...
go mod verify
Pop-Location
python -m unittest discover -s coze/banking/billing-agent -p test_generate_canvas.py -v
```

覆盖空数据、跨月、错误分类、重复与异常金额、预算口径、分页完整合计、报告持久化、越权与工具身份绑定、趋势、时区、整数溢出、模型异常。实际结果和修改清单见../../docs/banking-module4-delivery.md。测试的Coze HTTP fixture明确是协议测试，不是实际模型结果。
