# Savings Agent 原生配置参考

本目录不修改Coze引擎，也不提供自定义界面。agent-config.yaml和system-prompt.md用于在原生Agent管理界面配置；原生发布与真实模型验证需要实际运行的Coze实例。

三个受保护工具为get_financial_plan、replan_financial_plan、compare_financial_plans；成功均HTTP 200。输入只有plan_id及X-Trace-ID/X-Task-ID，用户身份由原生Plugin密钥绑定。后两种查询均可向Supervisor提供View.result.options三方案；无方案选择或转账工具。

在原生Plugin管理界面配置`services/banking-service/api/coze-plugin.openapi.yaml`，密钥配置在X-Banking-Tool-Key；API路径、operationId必须与契约一致。按实例网络设置Banking地址，不能照搬宿主机localhost到容器内。创建并发布实际工具后，用真实ID生成Start→Plugin→End参考canvas：

```powershell
python coze/banking/savings-agent/generate_tool_canvas.py --tool compare_financial_plans --plugin-id "$ActualPluginID" --api-id "$ActualAPIID" --plugin-name "$ActualPluginName" --plugin-version "$ActualPluginVersion" --out savings-compare.canvas.json
```

generator读取当前仓库`backend/domain/workflow/internal/canvas/examples/code_plugin.json`，复用模块5generate_operation适配函数，使用节点类型1/4/2。它验证规范正整数实际ID，但无法判断ID是否真的已发布；fixture ID仅适用于测试，禁止当成可运行实例配置。

在原生Workflow界面按生成参考配置并发布，使用本实例真实发布方式；不假设存在任何公共canvas导入API。原生Plugin输出需保留显式object/list字段类型，End返回结构化data以保留计划版本、摘要、金额、约束和审计关联。没有自动重试，以避免模型把重复调用误认为金融授权。

工具返回的FinancialPlanView schema见插件契约components.schemas。模型只解释Go结果，保留来源和阻断原因，不能编造金额或把未来预计收入/订阅节余计入现金。
