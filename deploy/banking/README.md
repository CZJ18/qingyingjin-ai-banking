# 模块1启动与部署

当前本地短信验证与密码找回采用加密开发收件箱，schema 013，最后附加 `compose.sms-development.yaml`；启动、读取开发码和实际验收见 [SMS-AUTH.md](../../services/banking-service/SMS-AUTH.md)。不调用腾讯云或发送真实短信。

当前网页已改为手机号/密码登录，金融 schema 为 012。使用 [手机号登录交付与启动步骤](../../services/banking-service/PHONE-AUTH.md)，在 P7 overlay 后附加 `compose.phone-auth.yaml`；以下 P7/P6 镜像命令保留为历史记录。

当前本机金融核心已进入 P7 PostgreSQL 部署，旧 MySQL 保留并冻结。当前启动、迁移、受控回退与验收命令见 [P7 交付](../../services/banking-service/P7.md) 和 [PostgreSQL 部署](../banking-postgres/README.md)。下面未附加 P7 overlay 的 Compose 命令属于历史 MySQL 部署路径。

当前模型已切换至 DeepSeek 云 API。私有配置、原生 Coze 注册/七角色发布、运行日志级别核验与实际结果见 [DeepSeek 接入](DEEPSEEK.md)。下文涉及 Qwen 的内容是 2026-10-10 的历史联调记录；不作为当前启动步骤。

## 本机已有 Coze 容器接入（2026-10-10）

本机当前运行的是 `coze-server-local`，共享网络为 `coze-studio-debug_coze-network`，不是生产 Compose 中的 `coze-server`。已恢复原有 Coze 数据库、中间件及后端；Milvus 就绪后再启动后端可避免此前的初始化超时。不要启动 debug Compose 的另一个 `coze-mysql` 替代已有数据容器。

```powershell
docker start coze-agri-mysql coze-redis coze-minio coze-etcd coze-elasticsearch coze-nsqlookupd coze-nsqd coze-milvus
# 用 docker exec coze-milvus curl -fsS http://127.0.0.1:9091/healthz 确认就绪，再启动：
docker start coze-server-local
```

已在当前运行容器上连接网络并设置 `banking-service` 别名。后续重建 Banking 时附加 `compose.coze-local.yaml` 保留网络配置：

```powershell
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml config --quiet
# 真实 PAT 和已发布银行工作流 ID 配置完毕后应用环境：
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml -f deploy/banking/compose.coze-local.yaml up -d --no-deps banking-service
```

对应后端配置（此处只有地址，凭据和 ID 必须来自实际实例）：

- 容器 `.env`：`BANKING_COZE_BASE_URL=http://coze-server-local:8888`。
- 本机 `.env.local`：`BANKING_COZE_BASE_URL=http://127.0.0.1:8888`。
- 两份环境分别配置真实 `BANKING_COZE_PAT`；地址与 PAT 必须成对填写，未就绪时保持两者为空。
- 发布后填写 `BANKING_SUPERVISOR_WORKFLOW_ID`、`BANKING_GOAL_WORKFLOW_ID`、`BANKING_BILLING_AGENT_WORKFLOW_ID`、`BANKING_SUBSCRIPTION_AGENT_WORKFLOW_ID`、`BANKING_SAVINGS_AGENT_WORKFLOW_ID`。遵循 [Supervisor 接入](../../coze/banking/supervisor-agent/README.md) 的实际输入输出约定。
- Coze 插件访问银行使用 `http://banking-service:8090`，工具凭据仅留在受控插件配置中。

已通过 Coze 原生接口注册独立账号 `banking-local@qingyingjin.local`、创建 30 天 PAT、验证模型连接，并在其个人空间发布五个实际工作流。原农业工作流保持独立。账号密码、PAT 和模型 API key 仅保存于被忽略的 `output/coze-local-private.json` 与服务端环境文件；不进入浏览器。

| 角色 | 实际已发布 Workflow ID |
| --- | --- |
| Supervisor | 7694865787609153536 |
| Billing | 7694865791367249920 |
| Subscription | 7694865792856227840 |
| Savings | 7694865794450063360 |
| Goal | 7694865796199088128 |

Studio 使用官方固定镜像，通过 `nginx-coze-local.conf` 代理原后端，入口为 `http://127.0.0.1:8889/sign`。启动时附加 `--profile coze-local-ui` 并运行 `up -d --no-deps coze-local-web`。已有平台管理员保留；为联调账号追加本机持久化 `basic_config.admin_emails` 配置后，使用正常登录和管理员模型接口配置本地模型，未绕过鉴权。

运行模型为官方 Qwen2.5 1.5B GGUF，通过官方 llama.cpp CPU 服务提供 OpenAI 兼容接口；文件 SHA256 必须匹配 `6a1a2eb6d15622bf3c96857206351ba97e1af16c30d7a74ee38970e434e9407e`。模型服务由 `Start-CozeLocalModel.ps1` 启动在 11435，随机 API key 保护，Coze 访问 `http://host.docker.internal:11435/v1`。尝试下载的 3B 文件未通过 SHA256 校验，没有投入运行。下载、生成文件和 CPU 二进制均位于忽略的 `output/`。

```powershell
# 重启本机后先恢复 Docker / Coze 依赖，再运行：
./deploy/banking/Start-CozeLocalModel.ps1
# 开发容器缺少官方 Dockerfile 中的 Deno / sandbox.py 时恢复原生沙箱资源：
./deploy/banking/Initialize-CozeLocalSandbox.ps1
node deploy/banking/bootstrap-coze-local.mjs --publish --verify
# 仅在发布和验证成功后写入 Banking 服务端环境：
node deploy/banking/bootstrap-coze-local.mjs --configure
```

bootstrap 不输出凭据。`--refresh` 更新所有现有工作流版本，`--refresh-goal` 只更新目标工作流，`--verify-goal` 只重新验证目标。Goal 使用真实模型解析、原生沙箱代码节点依据原文证据精确转换元／分，再交由 Go 服务独立验证 Schema、语义和证据；缺失或含糊证据报错，不填造金额，也不执行金融操作。原后端域代码未重写，恢复的 `sandbox.py` 与仓库原运行资源一致。联调结果和真实执行 ID 保存在忽略的 `output/coze-live-results.json`。

当前模块8启动、授权调用和实际验证见 [可信执行网关](../../services/banking-service/MODULE8.md) 与 [模块8交付](../../docs/banking-module8-delivery.md)。Compose 使用模块8镜像和001至007迁移，保留现有MySQL/Redis卷。旧金融写入端点必须改用网关的 prepare → authorize → execute。

模块2已添加SQL迁移、业务API及容器启动依赖。当前启动方法和curl以 [Banking Core](../../services/banking-service/MODULE2.md) 为准；下面内容为模块1历史记录。

以下命令从仓库根目录 D:/智慧农业 执行。没有新增或修改自定义前端。

## 1. 环境初始化

Windows PowerShell：
```powershell
powershell -NoProfile -File deploy/banking/Initialize-Environment.ps1 -ToolUserId 42
```

脚本生成 deploy/banking/.env（容器配置）和 .env.local（本机连接配置），全部关键凭证由密码学随机数产生，不覆盖已存在文件，不打印密钥。这两个文件被现有 .gitignore 的 .env 规则忽略；.env.local 另有目录级忽略规则。42 是演示 Banking 用户，不是自动发现的 Coze 用户；需要映射时传入操作员核实的 -CozeUserId。当前是单用户集成基础。

Linux/macOS 可复制 .env.example 后手动填入独立随机凭证，再为本机开发创建 .env.local；模板中的 CHANGE_ME 会被服务拒绝。

环境变量分组：
- BANKING_HTTP_ADDR / BANKING_LOG_LEVEL：监听地址和 JSON 日志级别。
- BANKING_CHECK_TIMEOUT：依赖连接和检查超时，默认 2s。
- BANKING_MYSQL_HOST/PORT/USER/PASSWORD/DATABASE/TLS：MySQL 连接，独立 qingyingjin schema；TLS 为 true 或 false。
- BANKING_REDIS_ADDR/USERNAME/PASSWORD/DB：Redis 连接；本机使用 127.0.0.1:16379，容器使用 banking-redis:6379。
- BANKING_USER_JWT_SECRET / BANKING_JWT_ISSUER / BANKING_JWT_AUDIENCE：用户短期令牌。
- BANKING_TOOL_TOKEN / BANKING_TOOL_USER_ID / BANKING_TOOL_COZE_USER_ID：工具服务凭证和可信用户映射。
- BANKING_COZE_BASE_URL / BANKING_COZE_PAT：可选，必须成对配置；Coze overlay 下 URL 为 http://coze-server:8888。
- BANKING_COZE_TIMEOUT：工作流 HTTP 请求超时，默认 30s。
- BANKING_MYSQL_ROOT_PASSWORD：只给 MySQL 容器初始化，不传给 Banking 服务。

本模块 MySQL 镜像自动创建空 schema 和专用数据库用户；没有金融建表迁移，也不使用 AutoMigrate。Redis 启用 AOF，MySQL/Redis 使用独立持久卷。

## 2. 独立 Docker Compose

```powershell
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml config --quiet
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml up -d --build
curl.exe http://127.0.0.1:8090/healthz
curl.exe http://127.0.0.1:8090/readyz
```

readyz=200 要求真实 MySQL SELECT 1、Redis PING 成功；依赖故障则 503。healthz=200 只说明进程还活着。

镜像为 Go 1.26.4 构建 + distroless nonroot 运行；所有版本固定，不使用 latest 的 Banking 镜像。专用 Docker ignore 限定必要代码并排除本地凭证。数据库默认不映射宿主端口。

## 3. 与现有 Coze 合并

先按当前 Coze 要求创建并配置 docker/.env；不复用 Banking 密钥作为平台数据库或 PAT。
```powershell
if (!(Test-Path docker/.env)) { Copy-Item docker/.env.example docker/.env }
# 编辑 docker/.env；如文件已存在，应保留原配置，不覆盖。
docker compose --env-file docker/.env --env-file deploy/banking/.env -p coze-studio -f docker/docker-compose.yml -f deploy/banking/compose.yaml -f deploy/banking/compose.coze.yaml config --quiet
docker compose --env-file docker/.env --env-file deploy/banking/.env -p coze-studio -f docker/docker-compose.yml -f deploy/banking/compose.yaml -f deploy/banking/compose.coze.yaml up -d --build
```

第一份 Compose 位于 docker/，所以 overlay 专门将 Banking build context 改为 ..。不要把 --project-directory 改成 deploy/banking，否则原平台的挂载路径会被错误解析。若现有平台使用不同项目名，沿用该项目名。

原平台依赖 .env 和模型配置；overlay 不修改原平台核心、用户库或管理界面。原平台镜像是 latest，正式联调需选定与源码对应的固定版本或按 backend/Dockerfile 构建；不能宣称当前分析等同于 latest 镜像行为。

本轮只做不解析 service env_file 的配置检查时，可执行：
```powershell
docker compose --env-file docker/.env.example --env-file deploy/banking/.env.example -p coze-studio -f docker/docker-compose.yml -f deploy/banking/compose.yaml -f deploy/banking/compose.coze.yaml config --no-env-resolution --quiet
```
这是配置解析验证，不能证明平台已经启动。

## 4. 本机 Go 开发和真实依赖测试

先启动独立 MySQL/Redis 并只映射 loopback 测试端口：
```powershell
docker compose --env-file deploy/banking/.env -f deploy/banking/compose.yaml -f deploy/banking/compose.test.yaml up -d banking-mysql banking-redis
Set-Location services/banking-service
go run -buildvcs=false ./cmd/server -env-file ../../deploy/banking/.env.local
```

无可用 Git HEAD 的当前快照使用 -buildvcs=false。正常 Git checkout 可启用默认 VCS 信息。
编译及测试：
```powershell
go test ./... -count=1
go vet ./...
go build -buildvcs=false -o ../../output/banking-service.exe ./cmd/server
$env:BANKING_TEST_ENV_FILE=(Resolve-Path '../../deploy/banking/.env.local').Path
go test -tags integration ./tests/integration -count=1 -v
```

默认单元测试包含 HTTP httptest、OpenAPI 响应校验、JWT/工具隔离、Coze 测试 HTTP 服务及旧领域包兼容性验证。Coze 测试服务不等于真实 Coze 联调。

integration 测试使用真实 MySQL/Redis：SELECT、临时表写入读回、带 TTL 的 Redis SET/GET/清理和真实依赖 HTTP readyz。临时表不构成正式数据库迁移，也不证明金融事实已持久化。

## 5. curl 调用

PowerShell 先通过操作员 CLI 签发用户令牌，输出捕获到变量，不写日志：
```powershell
Set-Location services/banking-service
$userToken = go run -buildvcs=false ./cmd/token -env-file ../../deploy/banking/.env.local -user-id 42 -ttl 1h
curl.exe -H "Authorization: Bearer $userToken" -H "X-Trace-ID: demo-module1" http://127.0.0.1:8090/api/v1/context
curl.exe http://127.0.0.1:8090/openapi.yaml
```

工具接口需实际密钥（由插件服务鉴权配置注入）：
```powershell
# $toolToken 从受控环境配置读取；不要打印或放到提示词中。
curl.exe -H "X-Banking-Tool-Key: $toolToken" -H "X-Trace-ID: demo-module1" -H "X-Task-ID: integration-probe" http://127.0.0.1:8090/tools/v1/integration/context
```

无凭证访问 /api/v1/context 或工具接口应为 401；用户名 query 不会覆盖可信身份。模块1不存在金融操作 API。停止 Banking 服务时保留数据卷，不使用 down -v。

## 模块3目标编译器

当前镜像tag为module3，增量迁移002随migrate执行。目标API、Coze模型/工作流配置与实际验证见 [MODULE3.md](../../services/banking-service/MODULE3.md)。BANKING_GOAL_WORKFLOW_ID只接受已发布的实际工作流ID，未配置时parse返回503，不伪造结果。`Initialize-Goal.ps1`仅初始化原始模拟生活目标，不执行解析或资金操作。

当前模块4：[模块4部署与API](../../services/banking-service/MODULE4.md)。六个只读金融分析工具已接入，Coze真实模型配置/发布仍需实际实例和凭证；当前镜像tag为module4。


当前镜像为 qingyingjin-banking-service:module5，自动迁移至004。新增 Initialize-Subscriptions.ps1 初始化持久模拟扣费与candidate，脚本不取消订阅；完整命令见 ../../services/banking-service/MODULE5.md。
