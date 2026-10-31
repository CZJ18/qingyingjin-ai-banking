# Stage12 隔离直连模式

`direct_provider` 是现有费用网关的一种显式传输模式，不是第二套账本。仅 `BANKING_ENVIRONMENT=test` 可启动；真实供应商仍固定为 `https://api.deepseek.com/v1`，由原网关使用 TLS、禁代理、禁重定向的 `/chat/completions` POST 调用。未提供新审批、精确计划和独立确认时，仍在读取受保护配置/供应商秘密之前拒绝。

普通 Coze 路径保留。schema v1 增加两个可选字段：manifest 和 Approval 的 `integration_mode`（省略等同 `coze`），以及 `direct_request_sha256`。旧 Approval 两个字段为空时 JSON 均省略，不改变其原有指纹算法；更改审批内容仍须独立审核，不能继承旧确认。模板明确写 coze，不会自动启用直连。

在 direct_provider 下：

- `direct_request_sha256` 绑定**精确 Chat Completions 请求字节**，不是自然语言文本的摘要。
- `task_request_sha256` 继续绑定包含账户/日期/限制的持久任务 canonical request。两者不能互换。
- Approval 的 `workflow_id`、`workflow_sha256`、`additional_workflows` 必须为空；manifest 所有 Coze 角色必须显式禁用且不携带绑定，Coze origin/scope 为空。不得编造已发布工作流或 Canvas。
- 执行前通过既有用户身份验证、Go 签名任务委托与 Python 持久任务/版本/活动租约核验；网关向现有账本提供本地 `direct_provider` 绑定。它不是 Coze 工作流 ID。
- 请求幂等键由任务和本地调用类型生成，调用者不得指定第二个键。原子 Reserve、审批 ID 绑定、次数/并发/token/金额限制和每次 HTTP 精确计划复核均保留。
- direct 模式禁用原生 tools/tool_choice、stream 和 Coze grants。现有 Supervisor DTO 只接收 billing/read_only 的注册工具计划，随后仍由现有 Go 账户绑定与工具参数校验执行。模型没有 SQL 或金融写入能力。

Go 服务配置：`BANKING_MODEL_INTEGRATION_MODE=direct_provider`、`BANKING_DIRECT_GATEWAY_ORIGIN`、`BANKING_DIRECT_GATEWAY_TOKEN`、`BANKING_DIRECT_MODEL`、`BANKING_DIRECT_MAX_OUTPUT_TOKENS`；不能同时配置 Coze 或专业模型工作流。Go 和 Python 不持有供应商 Key。

网关 CLI：`--integration-mode direct_provider`。已有 `--prepare-execution-plan`、`--preflight`、审批摘要及三项执行计划确认参数继续使用。直连预检以活动任务传输和精确请求绑定替代 Coze 发布/scope；报告明确标记 Coze NOT_APPLICABLE/NOT_VALIDATED。权限审核、官方价格审核、数据库就绪及余额检查并未省略。

真实 Key 只在全部前置门禁通过后从进程环境 `BANKING_MODEL_SUPPLIER_API_KEY` 或受保护 Secret 挂载 `BANKING_MODEL_SUPPLIER_SECRET_FILE` 读取，二者不能同时提供。真实 direct 模式拒绝在普通网关配置文件中填写 API Key。供应商权限审核仍绑定实际 Key 指纹、审批及证据，不能用合成权限证明替代。当前 Stage12 只注入明确标记的本地模拟 Key，真实 Key 未读取。

`model` 是实际请求/返回标识，费用记录保存供应商返回 model。`model_version` 仅为官方文档描述，不把 deepseek-flash 的可变别名当成不可变模型证明。

HTTP 错误、429、超时、断连、响应异常都保留 unknown 与占额；没有自动付费重试。usage、估算、reserved、reported 与正式 settled 分开。Stage12 模拟价格和 usage 不是官方账单。

本轮新环境 <http://127.0.0.1:29878> 与原 <http://127.0.0.1:29778> 分离。只读 schema 导出恢复到全新 PostgreSQL 集群，复制已有 schema/storage 元数据用于同版本校验；没有运行迁移程序，没有复制历史用户、任务、费用或认证行。所有 RLS、最小角色和 scheduler 函数所有权在新库复核。

真实调用仍需本轮新费用审批、权限审核及安全秘密注入、精确计划的独立确认和明确调用授权。尚未发出真实供应商请求，不能将模拟链路称作 DeepSeek 或 Coze 验收。

## 固定文本、零业务工具的首个冒烟任务

现有 `/product/api/v1/agent-tasks` 可提交单个显式任务：`agent_type=supervisor`、`tool=model_smoke`、`skill_id=task_planning`、`skill_version=1`、`execution_mode=read_only`、`parameters={}`、`dependencies=[]`、`use_coze=false`。根请求必须 `max_attempts=1`、`bindings={}`，不能混入其他任务或月度金融目标。它仍由实际 Python Worker、Go 签名活动任务委托和原费用网关执行；没有独立的供应商调用入口。

输入固定为 `Reply with BANKING_SMOKE_OK. No personal financial data; no tools or financial operations.`，不接受用户自定义文本、账户信息或工具参数。`coze.DirectSmokeRequest(model,max)` 生成待审批的精确请求字节，输出上限最多 128 token。请求显式携带官方 Chat Completions 的 `thinking={"type":"disabled"}`；网关只新增接受这一明确的非思考设置，不接受其他思考开关值或扩展字段。依据：<https://api-docs.deepseek.com/guides/thinking_mode/>。未调用供应商核验其实际行为。

执行计划只有在 direct Approval 的 `direct_request_sha256` 精确匹配该固定请求、`test_input` 精确匹配固定文本时，才允许 `deterministic_tools=[]`。普通 Coze/账单规划计划继续要求注册工具；固定冒烟计划不能夹带业务工具。更改请求格式、模型或 token 上限都会改变待审批摘要；旧确认不能继承。`model_smoke` 是模型传输探测，不应伪装成确定性金融工具列入计划。

冒烟结果必须精确返回 `BANKING_SMOKE_OK` 才报告任务成功，附实际响应模型标识和供应商响应 ID。页面通过原任务历史与完整返回展示结果。结果未知时保留原任务日志和费用占额；即使执行模式为 read_only，也禁止 Worker 把它当成确定性只读工具自动重放。额度拒绝不会产生新供应商请求。

本轮最新冒烟实例为 <http://127.0.0.1:29978>，供应商明确为本地模拟，仅证明工程路径；原 29778 和 Stage12 的 29878 保持不变。真实 DeepSeek、真实 Coze 和官方对账均尚未验收。
