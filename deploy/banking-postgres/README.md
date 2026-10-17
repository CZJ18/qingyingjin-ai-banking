# P7 PostgreSQL 金融账本部署

当前本地金融 schema 已增量升级 013，增加验证码验证/密码重置元数据；[SMS-AUTH.md](../../services/banking-service/SMS-AUTH.md) 为当前启动组合及验收入口。历史冻结 MySQL 保持不变。

后续手机号登录已将主金融库增量升级为 012；当前镜像、迁移和启动组合见 [PHONE-AUTH.md](../../services/banking-service/PHONE-AUTH.md)。P7 的迁移/回退 receipt 记录的是当时快照；冻结旧 MySQL 011 不能直接接收新增凭据列或成为自动回退源。

复用产品服务的 PostgreSQL 17.6 实例，但金融库与产品投影库分离：

| 库 / 角色 | 用途 |
| --- | --- |
| `qingrongjin_agent` | 保留现有产品投影和 LangGraph 状态，P7 不迁移此库 |
| `qingrongjin_finance` | 新金融核心库，初始只待迁移验收，不自动成为资金权威源 |
| `qingrongjin_finance_test` | 导入与一致性验收专用测试库，保持导入快照，禁止在其中运行会清理或追加数据的完整核心回归 |
| `qingrongjin_finance_core_test` | 完整 Go 核心回归专用库，与导入验收库隔离，仅迁移角色可连接 |
| `qingrongjin_finance_acceptance_test` | 独立成功导入验收库，与保留的失败重放证据及核心回归库隔离，仅迁移角色可连接 |
| `banking_finance_migrator` | 仅拥有四个金融库，不是超级用户，无创建数据库、创建角色或绕过 RLS 权限 |
| `banking_finance_app` | 运行时登录，仅金融 schema 使用权和业务表 DML/序列使用权，无 DDL/控制表写权限 |

所有本地敏感配置均为 `*.env.local`，由同目录 `.gitignore` 排除。脚本仅输出状态、角色名和文件名，不输出密码、Coze PAT、JWT 签名密钥或解析后的 Compose。

在仓库根目录执行：

```powershell
powershell -NoProfile -File deploy/banking-postgres/Setup-Local.ps1
powershell -NoProfile -File deploy/banking-postgres/Link-Network.ps1
powershell -NoProfile -File deploy/banking-postgres/Verify-Configuration.ps1
powershell -NoProfile -File deploy/banking-postgres/Setup-MySQLTests.ps1
powershell -NoProfile -File deploy/banking-postgres/Verify-DatabaseAccess.ps1
```

`Setup-Local.ps1` 从现有 `deploy/banking/.env.local` 与 `.env` 在内存中复制 Redis、JWT、工具凭据和 Coze 配置。首次生成相互独立的 256 位迁移/运行密码，之后重复使用，生成的托管配置会根据源配置更新。Windows 私有文件仅允许当前用户和 SYSTEM 访问。不以 SQL 命令行参数传密码。`-ConfigureOnly` 只生成配置，不连接数据库。

| 配置文件 | 连接 |
| --- | --- |
| `finance.migrate.env.local` | 主机 15432，迁移角色，金融库 |
| `finance.test.env.local` | 主机 15432，迁移角色，导入验收测试库 |
| `finance.core-test.env.local` | 主机 15432，迁移角色，完整 Go 核心回归测试库 |
| `finance.acceptance-test.env.local` | 主机 15432，迁移角色，独立成功导入验收库 |
| `finance.app.env.local` | 主机 15432，运行角色，金融库 |
| `finance.compose.env.local` | Docker 私有网络 5432，运行角色及迁移任务所需独立凭据 |
| `finance.mysql-test.env.local` | 主机 13306，独立 MySQL 测试源库 `qingrongjin_p7_test` |
| `finance.mysql-rollback-test.env.local` | 主机 13306，独立 MySQL 回退测试库 `qingrongjin_p7_rollback_test` |
| `finance.mysql-core-test.env.local` | 主机 13306，独立 MySQL 核心回归测试库 `qingrongjin_p7_core_test` |
| `finance.mysql-acceptance-test.env.local` | 主机 13306，独立成功导入源库 `qingrongjin_p7_acceptance_test` |

`Setup-MySQLTests.ps1` 为导入、回退、核心回归和成功导入验收添加四个独立 schema，以及仅对这四个 schema 拥有权限的 `banking_p7_migrator`。它使用独立随机测试密码，不修改现有业务库，不重启 MySQL。并行测试正在使用此角色时，使用 `-AdditiveOnly` 增量添加固定测试库及权限，避免默认权限收敛操作短暂撤销已有权限；随后仍须运行精确权限检查。已存在的测试库均保留数据，不会因为重跑 Setup 被清空。`Verify-DatabaseAccess.ps1` 使用只读事务实际检查 PG 角色、库/schema 权限、产品库隔离及 MySQL 测试角色权限范围，不输出数据库凭据。

开发环境使用 loopback / Docker 私有网络与 `sslmode=disable`。生产部署需配置受验证的 TLS；不可直接复制该本地配置。

`Link-Network.ps1` 仅将现有 PG 容器增量接入 `qingyingjin_banking-egress`，不重启容器、不删除原网络、不创建卷。为确保以后由 Compose 重建时仍保留该连接，使用原产品 Compose、原测试端口 overlay 和 P7 网络 overlay 的完整组合：

```powershell
docker compose --env-file deploy/banking-agent/.env -f deploy/banking-agent/compose.yaml -f deploy/banking-agent/compose.test.yaml -f deploy/banking-postgres/compose.product-postgres.yaml config --quiet
```

此命令只验证；首次迁移期间不重建现有产品 PostgreSQL。原 `agent-internal`、`agent-test` 网络、15432 loopback 端口及数据卷均保留。

金融服务 overlay 验证：

```powershell
docker compose --env-file deploy/banking-postgres/finance.compose.env.local -f deploy/banking/compose.yaml -f deploy/banking-postgres/compose.yaml config --quiet
```

运行容器只注入 app 密码，迁移任务才注入 owner 密码。overlay 不依赖 MySQL 就绪，并将运行时旧 MySQL 密码清空；现有 MySQL 容器和卷仍保留用于比对和回退。

**切换门槛：** 完成 PostgreSQL schema、控制表权限收敛、导入、账本/余额比对、并发/授权/幂等验收后，才运行服务切换。Setup 和 Link 脚本均不切换金融权威源、不自动运行迁移、不写业务数据。迁移命令必须在创建控制表后撤销 app 对控制元数据的 DML，Setup 重跑不重新授权已有表。
