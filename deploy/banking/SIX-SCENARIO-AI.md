# 六场景助手接入

现有操作建议桥和 `prepare → authorize → execute` 保持不变。新增能力是按所选账户查询转账、卡片、模拟理财、订阅和已登记生日的最小只读概览；账单继续使用原有确定性工具。

## 实际调用关系

Vue → Python 持久任务／Supervisor → Go 规划入口 → 现有费用网关 → 模型规划 → Go 白名单和账户／计划绑定校验 → LangGraph 受控查询或准备确认 → Vue 结果 → 用户进入原业务表单。

`supervisor/get_scenario_overview` 使用 `task_planning` 版本 1、`read_only`，参数必须为 `{ "scenario": "cards", "account_id": "所选账户ID" }`。scenario 允许 `transfer`、`cards`、`investment`、`subscriptions`、`birthday`；账户必须已选择并属于当前用户。它不创建金融意向、调用授权、费用预占或自动执行。返回最多五十项，截断时明确提示。姓名、手机号、卡号、原始操作载荷和密钥不进入概览。

## 直连模式

旧的 `BANKING_MODEL_INTEGRATION_MODE=direct_provider` 仍默认仅账单规划，原 `DirectPlannerPrompt` 和请求生成方式未修改。

隔离环境明确设置 `BANKING_DIRECT_PLANNER_PROFILE=scenarios` 才选择本地适配器 `direct_provider_scenarios`。这不是 Coze 工作流 ID。它仍只向既有费用网关发送请求，不持有供应商密钥。新提示词改变了精确请求正文，必须使用本轮新的 Approval、Policy、任务和数据范围、执行计划及独立确认；旧账单审批和摘要不能授权它。

该模式禁止递归规划、smoke、未绑定的订阅全局查询、模型自动解读子请求、直接执行，以及自动代答风险问卷。缺少收款人、卡片或产品选择时查询概览，引导本人选择。金额、余额、账户归属、风险和最终授权继续以 Go 为准。

可以提出“看看这张账户的卡片状态”“比较可查看的模拟理财产品”“检查已有订阅”“查看已登记生日计划”等问题。操作对象或精确参数不足时，不猜 ID、日期、问卷答案或收款人。

## Coze 与真实执行

旧的已发布窄范围 Coze 工作流没有被自动修改或重新发布。要使用新工具，须在经授权的工作流发布流程中更新提示与工具范围，并重新核验当前 Canvas、scope 和费用审批。不将本地协议响应计作真实 Coze 或真实模型验收。

转账、卡片、理财、生日订购和本地订阅取消仍是模拟业务。真实资金和商户操作还需要操作者选定机构、取得官方接入和测试权限；本机配置开关不能替代机构接口。没有供应商原始消费明细时，usage、reported、reserved 和 unknown 均不能改称正式已结算。
