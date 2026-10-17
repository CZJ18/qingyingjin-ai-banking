# Supervisor 原生 Coze 接入

本目录提供当前源码支持的配置和生成器。P3 已通过原生鉴权接口发布/更新本机模型工作流，实际执行与验收状态见 [MODULE11](../../../services/banking-service/MODULE11.md)。Plugin Wrapper 仍需实际插件/API ID，不能把参考画布称为已发布工具。保持 Coze 原生 Agent、Workflow、Plugin、模型管理界面不变。

1. 原生插件导入 Banking Service 的 `/coze-plugin.openapi.yaml`，在管理端设置 `X-Banking-Tool-Key`。仅绑定服务器指定的一个用户；不可接受模型提供的user_id。Supervisor新增工具仅查询任务和结果，提交/确认/控制使用用户JWT API。
2. 使用当前实例真实model ID/name生成Supervisor和四个专业解释Workflow：

```powershell
python coze/banking/supervisor-agent/generate_canvas.py --model-id ACTUAL_ID --model-name ACTUAL_NAME --role supervisor --out supervisor.canvas.json
python coze/banking/supervisor-agent/generate_canvas.py --model-id ACTUAL_ID --model-name ACTUAL_NAME --role billing --out billing-explanation.canvas.json
# subscription、savings、lifestyle角色同理
```

原生节点复用 `backend/domain/workflow/internal/canvas/examples/llm_node_with_skills/llm_node_with_workflow_tool.json` 的 Start/LLM/End。Start接收input_string，End.output返回JSON字符串；模型temperature=0、结构化JSON模式、无额外原生重试。生成器移除原示例中的外部工具ID引用。Canvas节点数组顺序不代表执行顺序，edges决定Start→LLM→End。

3. 原生管理端配置并发布Workflow，把实际workflow ID填入Banking Service对应环境变量。执行复用已验证的 `/v1/workflow/run`同步调用；不假设存在Agent-to-Agent HTTP API。已有专业角色接收过滤后的计算结果和证据。Lifestyle 接收 Go 从已验证方案生成的事实目录，仅返回 task_id、agent_type 和 finding_ids；Go 校验选择并渲染说明、解析证据引用，不接受模型自由改写金融事实。
4. 原生查询工具Wrapper复用既有模块5 Plugin adapter：

```powershell
python coze/banking/supervisor-agent/generate_tool_canvas.py --tool get_orchestration --plugin-id ACTUAL_PLUGIN_ID --api-id ACTUAL_API_ID --plugin-name ACTUAL_NAME --plugin-version ACTUAL_VERSION --out supervisor-status.canvas.json
```

该adapter基于 `backend/domain/workflow/internal/canvas/examples/plugin_node_with_types/sourcecode_plugin.json` 当前真实Plugin节点。API成功HTTP200，响应显式object类型。不将密钥、用户确认或执行权限作为模型参数。

5. `use_coze=false`支持明确任务和已持久化Goal Compiler任务计划的确定性协调。自然语言规划必须配置真实Supervisor Workflow，失败不得假装已完成模型解析。配置和测试中的123/456只是测试夹具ID。

模块8网关缺失时敏感proposal保持awaiting_gateway。即使用户明确确认本任务，也不表示金融执行已获授权；本模块不执行资金变更。

```powershell
python -m unittest discover -s coze/banking/supervisor-agent -p 'test_generate*.py' -v
```
