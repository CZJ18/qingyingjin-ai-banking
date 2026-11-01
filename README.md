# 轻融金 AI Banking

Vue 3 / TypeScript、Go Banking Service、Python FastAPI / LangGraph、自托管 Coze、模型费用网关、PostgreSQL / Redis。

本仓库是参赛源码与部署参考。保留已有实现和上游 Apache-2.0 许可。代码仓库：https://github.com/CZJ18/qingyingjin-ai-banking。

## 仓库与参赛上传范围

本仓库对应报名页面的“源码与部署材料”：项目源码、README、部署配置示例、依赖锁文件、API契约、测试用例、合成样本及必要工程文档。

技术文档、答辩PPT、安全自评报告在报名网站各自的栏位单独上传；演示录像、封面素材、讲稿和验收导出记录由操作者本地保管，不随本仓库及源码ZIP打包。代码仓库链接填写本仓库首页地址。

## 实际完成情况

- 六类业务已有页面、后端接口和持久化：智能转账、账单分析、理财、卡片、订阅/代扣、生日跨场景。金融执行仍为模拟，不连接真实银行、发卡或商户资金。
- 2026-10-30 历史验收完成六个只读自然语言用例、7次真实请求；这是历史批次，不等于所有新任务均获授权。
- 最新账单用例已实际通过 Vue → Go → Python/LangGraph → 已发布 Coze Supervisor v0.0.10 / Billing v0.0.9 → 真实 DeepSeek → Go只读工具 → 隔离SQL → Vue。61笔九月合成账单，金额由Go计算；模型给出可选购物延期、复用/维修、每周复盘。完整验收记录由操作者在本地保存。
- 先前失败及v0.0.8建议缺项保留；v0.0.9用2次请求复验，共1529token。同项目两轮合计4次、2956token；金额状态为预占/估算/reported，尚无正式供应商账单对账。
- 未证明实际节省500元或完成7天复盘；任意问题、生产合规和真实银行执行均不能由本用例推导为通过。

## 运行与开发

1. [部署说明](DEPLOYMENT.md)：依赖、源码构建、独立数据库、角色与RLS要求。
2. [测试说明](TESTING.md)：默认非付费回归和真实验收边界。
3. [代码索引](CODE-INDEX.md)：业务、模型和契约路径。
4. [真实模型操作流程](deploy/banking/REAL-MODEL-RUNBOOK.md)：复用已有Policy、Approval、账本和精确执行确认，不另建付费通道。

```powershell
cd services/banking-service
go test ./internal/orchestrator ./internal/coze ./internal/modelgateway ./cmd/model-gateway ./internal/modelspend
go build ./cmd/server
go build ./cmd/model-gateway
```

Go需要1.26.9及本仓库backend本地replace依赖。前端使用锁文件npm ci后运行npm test和npm run build；Python使用其uv.lock与服务README。需要SQL的专项须显式提供独立测试数据库，普通回归不使用生产库。

## 配置和授权

仓库不提供供应商Key/PAT、真实账单、私人数据库、会话、有效Approval、独立确认或READY。模板和Canvas快照均不是授权。浏览器与Worker不得绕费用网关直连供应商；金额、账户、工具及金融授权由Go确定。

本机30440是操作者的隔离实例，不是公网服务或克隆即运行的地址。曾确认周期固定于北京时间2026-11-01 19:58:19到期，且仅限精确任务；代码发布不会延长或激活它。新部署需自己的配置、审批与独立确认。未配置时明确拒绝，不回退fixture冒充真实模型。

## 上游许可

Coze Studio来源为 https://github.com/coze-dev/coze-studio 。保留原版权头与 [Apache-2.0许可](LICENSE-APACHE)，见 [第三方说明](THIRD-PARTY-NOTICE.md)。本仓库与提交材料不声称上游代码均为原创。
