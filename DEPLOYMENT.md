# 部署说明

从仓库源码构建并部署到自己的独立环境。按“构建镜像 → 配置数据库和角色 → 初始化空库 → 启动Go与Agent → 配置Web代理 → 浏览器检查”的顺序进行。

这是分步骤部署说明，不是一键启动包。依赖安装和镜像构建不能代替实际服务验收；全新机器完整全栈复演尚未在本轮材料制作中完成。

| 目标 | 从哪里开始 |
|---|---|
| 只看代码和跑非付费回归 | [README](README.md)、[TESTING](TESTING.md) |
| 启动完整业务系统 | 按下方第1、2节，设置自己的数据库、内部地址和端口 |
| 启用真实模型 | 完成业务部署后，按第3节及[真实模型手册](deploy/banking/REAL-MODEL-RUNBOOK.md)准备 |
| 对比原操作者环境 | 本文末尾的历史快照；旧端口和摘要不是新环境配置 |

## 1. 依赖与镜像构建

建议 Windows 11 + PowerShell 7、Docker Desktop Linux Engine、Go 1.26.9、Node 22、Python 3.13、uv 0.13.0。Go/Python/NPM 构建需要对应公共包源或已有缓存；构建镜像下载不代表模型调用授权。

在解压根目录执行（不会自动启动或替换容器）：

```powershell
docker build -f services/banking-service/Dockerfile -t qyj-submission-core:20261031 .
docker build -f services/banking-agent-service/Dockerfile -t qyj-submission-agent:20261031 services/banking-agent-service
docker build -f deploy/banking-web/Dockerfile -t qyj-submission-web:20261031 .
```

Go Dockerfile 同时构建业务、迁移、种子、费用网关和对账 CLI。`services/banking-service/go.mod` 的 `replace ../../backend` 是必需本地依赖，不要只复制服务目录。

## 2. 新环境配置与启动顺序

本包复用现有部署机制；没有新增一套绕过权限的“一键全开”脚本。全新机器未在本次材料制作中完成全栈复演，以下原有部署流程需要在空目录和独立 Docker 环境核验。

1. 阅读 `deploy/banking/README.md`。仅在全新解压目录运行 `deploy/banking/Initialize-Environment.ps1`，生成本地随机数据库/JWT/工具凭据。脚本遇到已有配置拒绝覆盖。不要把生成配置上传。
2. 阅读[Agent服务说明](services/banking-agent-service/README.md)和[Agent Compose](deploy/banking-agent/compose.yaml)，运行 `deploy/banking-agent/Setup-P5.ps1`：生成独立 Agent PostgreSQL/Redis 凭据并绑定原有任务委托。该脚本不启动模型。
3. Go 的兼容开发栈模板位于 `deploy/banking/compose.yaml`；Agent 模板位于 `deploy/banking-agent/compose.yaml`。先运行 `docker compose --env-file <本机新生成配置> -f <对应 compose.yaml> config --quiet`，不打印展开的秘密值。使用独立项目名称/端口/卷，原模板含固定网络名，启动前必须对新环境明确隔离，不能直接指向历史实例。
4. 采用 PostgreSQL 金融库时，按 `deploy/banking-postgres/README.md` 的迁移/运行角色分离流程建立新库。其 Setup 脚本以已有本地开发基础栈为前提；不会替操作者选择生产库。只对确认的空白隔离目标运行迁移。Go 支持 `-expected-database <精确库名> -test-only`，不能针对生产或旧快照试运行。
5. Agent 使用 `banking_agent.migrate` 迁移角色，服务使用非 owner 的 `banking_agent_app`。租户表保留 ENABLE/FORCE RLS；角色检查失败必须修复，不关闭校验。
6. 对新数据库完成迁移版本、运行角色、RLS 和只读就绪校验后，再启动 Go / Python。Go 通过 `BANKING_AGENT_PRODUCT_BASE_URL` 配置 Agent 同源代理，开发内部 HTTP 必须加入精确批准 origin。默认后台 Worker 保持关闭。
7. Web 使用 `deploy/banking-web`。Nginx 必须将 `/api`、`/product`、OpenAPI 代理到本轮新服务，保留 Host/端口；仅绑定回环地址。不可因静态首页 200 就宣布全栈就绪。
8. 在浏览器注册新的模拟身份，创建明确标注的模拟账户。导入 `sample/synthetic-full-month-202609.csv` 仅供分析验收；用预览与补录授权流程，不直接写余额。样本的月份、币种与目标账户要在界面重新核对。

不提供固定默认账户/密码。种子命令 `cmd/seed -confirm-simulated-data` 只用于操作者明确选择的隔离身份；它不会自动赋予浏览器注册用户历史账户。

## 3. 配置受控真实 AI

本包默认没有供应商密钥、工作流运行配置或有效 Approval。复用 `deploy/banking/REAL-MODEL-RUNBOOK.md`、manifest/Policy/Approval 示例及网关 CLI。只在新范围审批、精确计划独立确认、供应商权限和工作流 scope 有证据后启用；候选计划不能执行。不能继承历史 READY 或自动补额。

Coze Studio 的完整运行还需其独立中间件、已发布工作流和 Canvas/scope 核验；不把此源码包描述为已配置的外部工作流实例。

## 4. 停止服务与数据保留

只使用精确项目名执行 `docker compose -p <本轮项目名> ... stop`。禁止 `down -v`、删卷或清账。原运行环境及费用 unknown/reported、审批绑定、幂等记录继续保留。本次仅准备材料，无运行容器回滚动作。

<details>
<summary>历史隔离部署与源码差异（仅供比对）</summary>

以下是既有验收快照，不表示这些本机地址可供下载者访问，也不代表付费组件此刻仍在运行。

### 历史30436/30440验收快照

- 历史受限真实AI入口为 `http://127.0.0.1:30440/assistant`；30436/30438及更早应用保留。本地地址仅在原操作者本机有效。该历史精确账单用例已通过真实规划、解读、工具/SQL和Vue回传；周期固定于北京时间2026-11-01 19:58:19结束，不随代码发布延长。
- 原30432的Go核心镜像：`sha256:8d9020110a9c218e7b7be017069e042b2a0c7fc735e5380c9fb33e46adc322e2`。
- 本快照本轮部署修复镜像：`sha256:cddbc9ce72ba82fe2afb2173e4b5f437535e45bcdf7cd1f6d920de86c7c61148`，已在30436及30440隔离环境运行；原应用保持不变。
- 修复版 Go 二进制：`090f23a8ef676c282629a6aad37fa2b8bdc0d7b8f0bba3150cd6866ef5ac6065`。
- 网关二进制未变：`d0914de91560c6165d8640971880b57a8aa023b50615e1bff64ee370b63c637b`。
- 新部署镜像可能因平台/构建环境产生不同摘要，应记录实际值，不能只比镜像标签。
- 本包不携带当前数据库、账户口令或 Docker 私有网络，解压不会自动恢复该入口。

### 该验收快照与当前源码的区别

核心Go运行二进制仍为090f23a8…，共享直连提示词源码有后续改进但未据此重建当前核心；真实Coze模式使用另行确认发布的Billingv0.0.9，不能声称所有源码改变都已进入当前镜像。Canvas快照见coze/banking/published-review-20261101；快照本身不含审批或scope资格。新机器完整全栈部署仍需按上述流程实际核验。

</details>
