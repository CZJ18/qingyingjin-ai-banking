# Coze Banking 工具接入：模块1

基于实际源码 backend/domain/plugin/service/tool/invocation_http.go 的服务 Token header 注入机制。

## 原生界面配置流程

1. 启动 Coze 和 Banking，确认两者共享 coze-network；在 Coze 的原生插件管理中创建自定义 HTTP 插件。
2. 使用 services/banking-service/api/coze-plugin.openapi.yaml 作为接口定义。若当前导入流程要求不同步骤，按本版本原生 UI 完成并保存实际配置，不使用未经证实的自动注册 API。
3. 服务地址设为 http://banking-service:8090，不能使用 Coze 容器内的 localhost 指向宿主。
4. 配置服务鉴权 Token：位置 header，key 为 X-Banking-Tool-Key，value 为 BANKING_TOOL_TOKEN 的实际值。只在受控管理配置中保存，不加入提示词或模型可见参数。
5. 保持只有 banking_integration_context 工具。可在已有测试 Agent 或测试 Workflow 中绑定此插件并执行工具调试。
6. 成功应返回 code=OK、actor.kind=tool、服务配置的 user_id 和 financial_execution_enabled=false。query user_id 不能改变身份。
7. 模块1未自动创建或发布 Agent/Workflow；真实 Coze 插件联调结果以验证文档为准。

## 双向通信

Coze → Banking：工具密钥，只能集成探测；后续按角色开放读取/提案 scope。
Banking → Coze：BANKING_COZE_BASE_URL + BANKING_COZE_PAT 成对配置，使用已有 /v1/workflow/run；PAT 不代表用户金融授权。

Workflow 输入需显式设计 task_id、trace_id、input_version、role、round 等字段，由可信应用提供。当前工作流客户端只负责真实 HTTP 协议，不假装拥有具体工作流 ID；操作员需先在 Coze 发布工作流并提供真实 ID。HTTP 头 trace 是否进入平台日志以联调为准，业务关联应同时保存 task_id 与 execute_id。

## 后续四角色边界

Supervisor：编译生活目标、调度专业分析、协商并整合版本计划。
Billing：消费分类/统计/异常，输出交易证据。
Subscription：周期费用识别和取消建议，不自行取消。
Savings：资金可行性和预算/储蓄计划，不自行转账。

上述为后续配置规范，本轮没有执行这些角色或生成金融结论。并行与子工作流配置必须在当前源码/平台验证，不预设 Agent-to-Agent API。

## 模块3健康目标解析

事实解析Prompt和原生三节点配置见 [goal-compiler](goal-compiler/workflow-config.yaml)；配置/发布说明见 [MODULE3.md](../../services/banking-service/MODULE3.md)。生成器使用当前源码真实canvas样例，不假设新的公开导入API。工作流不配置银行工具，不决定资金任务权限，只返回Schema事实；Go生成并验证计划，真实模型联调需要部署实际Coze及配置模型/PAT/workflow_id。

模块4交付：[Billing Agent配置](billing-agent/agent-config.yaml)。六个只读金融分析工具已接入，Coze真实模型配置/发布仍需实际实例和凭证；该模块历史镜像tag为module4；当前服务使用module5。


模块5配置位于 [subscription-agent/agent-config.yaml](subscription-agent/agent-config.yaml)，工具画布生成器基于当前原生 Plugin 节点。仍需实际实例配置与发布，不能视为已完成模型联调。
