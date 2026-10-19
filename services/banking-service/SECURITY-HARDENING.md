# 安全加固：测试目标、通信及模型计量

本轮修改源码和独立测试环境，不自动提交、构建/发布Docker镜像或更新运行服务。源码要求schema019；当前已部署的历史版本不因源码变化而自动升级。发布前备份目标库、用迁移身份执行019、确认权限和配置，再部署匹配的新程序。不要把本轮验证写成新的全面渗透测试。

## 1. 测试脚本前置保护

`docs/security/test_safety.py`统一验证测试目标；`redteam_setup.py`、`remediation_setup.py`、`money_cap_validation.py`使用该模块。缺少保护配置立即拒绝，不自动采用旧私有连接。脚本导入不创建数据库或角色。原始证据保留，新用例写入新目录。

必须明确设置：

- `BANKING_TEST_ENVIRONMENT=isolated`。
- `BANKING_TEST_RUN_ID`：完整小写UUID，每次运行唯一。
- `BANKING_TEST_ALLOWED_TARGETS`：JSON数组，每项精确包含driver、host、整数port、database；禁止通配符。不只检查名称后缀。

示例结构（仅示意，端口和名称必须与自己的新测试资源完全一致）：

```json
[{"driver":"postgres","host":"127.0.0.1","port":25433,"database":"banking_example_test"}]
```

目标必须是允许清单中的完整四元组，数据库采用受限标识符和`_test`后缀；已知产品库/系统库即使在清单中也拒绝。PostgreSQL创建库/角色需要额外明确允许同一隔离实例的`postgres`管理库，不能复用产品投影库DSN。

本地PostgreSQL连接还核对Docker发布端口与完整run标签；MySQL执行器核对容器标签、ID/映射并查询实际目标库。仅有localhost不证明隔离。原生本地数据库没有Docker归属证明时这些脚本默认拒绝，不能以放宽校验绕过。

非本地主机必须精确列出，并设置`BANKING_TEST_REMOTE_CONFIRM=ALLOW_REMOTE_TEST:<完整run UUID>`；PostgreSQL还要求`sslmode=verify-full`。DSN拒绝多主机、service、hostaddr、options等隐式重定向；连接器主动固定解析后的地址，避免PGHOSTADDR/PGSERVICE环境变量替换。错误只输出脱敏端点或通用拒绝原因。

`remediation_setup.py`额外需要`BANKING_TEST_BASE_CONFIG`和`BANKING_TEST_POSTGRES_ADMIN_DSN`；必须使用合成配置，旧配置缺少标识不会自动获准。历史脚本/已打包ZIP是当时快照，不会被新加固自动改写。重跑HTTP前必须重建对应测试配置、编译当前二进制；旧固定容器名、端口、私有指针不能视作授权。

启动、迁移入口还要求`BANKING_TEST_BIN_DIR`指向本轮编译的绝对目录并具备所需二进制，禁止回落到旧私有目录。`money_cap_validation.py`、配套HTTP及SQL验证入口使用显式`BANKING_TEST_REPAIR_DIRECTORY`，在数据库操作前复核目标；导入这些模块不创建数据库、不写入mock模式、不启动服务。

## 2. Docker归属与清理

新测试容器、网络、卷均带`banking.test.managed=true`、`banking.test.environment=isolated`和完整`banking.test.run_id`，保存Docker返回的实际ID。固定名字不能作为删除依据。

创建前必须证明资源名未被占用，无法列举时失败关闭；创建后立即核对实际ID及run标签再使用。尤其不能让`docker volume create`把已有同名数据卷当成本轮新卷。

`redteam_setup.py --cleanup`仅处理对应run目录记录的ID，并重新inspect标签和ID；无法证明、名字替换或删除失败均记录skipped。默认清理容器/网络，**不删除卷**。删除卷必须另给`--delete-volumes-confirmation DELETE_TEST_VOLUMES:<run UUID>`，仍须归属核验。清理结果每次写新文件。测试失败时已创建资源仍有清单，不对其他项目运行prune或down -v。

## 3. 凭据服务通信策略

`BANKING_ENVIRONMENT`只允许production/development/test，未设置按production；未知值启动失败。生产环境Coze与Executor目标均要求HTTPS，不提供HTTP例外。证书按Go标准验证，不降级、不跳过验证、不跟随重定向。

development/test允许字面量回环IP的HTTP；其他HTTP来源必须在`BANKING_HTTP_ALLOWED_ORIGINS`中按完整origin显式批准，逗号分隔，无路径、用户信息、query或通配符。不根据`.internal`、Docker名或请求参数自动信任。

本地Compose显式声明development。`compose.coze-local.yaml`只批准实际挂载Coze网络上的`http://coze-server-local:8888`。这只是开发信任声明，不是网络隔离证明；操作员须核对网络成员。改为production时该HTTP例外会导致配置失败，需配置经过验证的HTTPS服务。自调用Executor也不能静默使用HTTP。

## 4. 费用字段与schema019

019只增加字段和`banking_model_reconciliations`审计表，不重写001–018，也不把旧cost变成零、人民币或已对账。旧记录默认cost_status=legacy_unverified、usage_status=legacy；原reported_cost、实际token和预占保持原值。MySQL部分DDL中断可在列类型/长度/空值/default匹配时继续；不匹配拒绝，不覆盖。PostgreSQL使用事务DDL。

- `raw_reported_cost`：供应商不可信原值。超长原值只存最多4096字节的UTF-8完整前缀，完整响应有report_digest；不能当作金额。
- `validated_reported_cost`：仅格式通过的原报告值，**不是实际费用**。接受非负普通十进制、最多18整数位/8小数位，不接受负数、NaN、Infinity、指数、逗号或空白。这个界限是数据格式上限，不是虚构供应商定价。
- `cost_status`：legacy_unverified / missing / invalid / reported_unverified / reconciled。缺币种/单位仍为未核实报告，不能当发票。
- `cost_currency`、`cost_unit`：仅由操作员配置已核实的字段含义：`BANKING_MODEL_REPORTED_COST_CURRENCY`三位大写代码、`BANKING_MODEL_REPORTED_COST_UNIT=currency_major`必须成对提供，默认都空。代码不自动从上游或模型推断CNY/价格。
- `confirmed_cost`、`confirmed_currency`、`confirmed_unit`：仅操作员凭独立依据核对后填入，和原报告值分离；不能自动累计为硬额度。
- `usage_status`：新请求pending；有合法实际token结算为settled；人工核对为reconciled。成功但没有token仍缓存完成结果、保留预占，并保持pending；没有伪造actual_tokens=0。
- `reported_tokens`、`provider_execute_id`、`report_digest`连同调用ID、请求键/参数摘要保留核对依据。格式错误、类型错误和未知结果不放回预算。

新增审计表纳入49表离线迁移清单和冻结触发器。PostgreSQL迁移会撤销审计表默认继承给非owner角色的写权限；部署后再给**专用操作员角色**必要权限。MySQL须明确收敛运行账户的schema级ALL权限，不能宣称库级ALL账户受表级撤销约束。

## 5. 未知用量的受控核对

新增离线`banking-model-reconcile`，不注册HTTP路由、Agent工具或Skill。独立高熵管理凭据不注入普通server容器；操作员身份来自受保护配置，不能由模型生成。运行前同时验证独立管理凭据、明确的expected-database、数据库访问权限；所有实际核对在同一SQL事务写审计，审计失败整体回滚。

操作员私有配置需要`BANKING_MODEL_RECONCILIATION_ADMIN_TOKEN`（至少32字节、不得复用JWT/Tool/Coze凭据）和`BANKING_MODEL_RECONCILIATION_OPERATOR`；呈示凭据通过`BANKING_MODEL_RECONCILIATION_TOKEN`传入进程环境，不能放命令行。专用数据库角色需users行锁、model windows/calls必要读写，以及reconciliation审计SELECT/INSERT；普通运行角色不授予审计写权限。

```text
banking-model-reconcile --env-file <private-operator-env> --expected-database <exact-db> --list-pending
banking-model-reconcile --env-file <private-operator-env> --expected-database <exact-db> --user-id <owner> --call-id <id> --tokens <verified-integer> --idempotency-key <key> --evidence-ref <nonsecret-log-or-invoice-reference>
```

核对费用还需`--actual-cost`、`--currency`、`--unit currency_major`和证据；不填写token就不会释放token预占。reserved可能仍在执行，默认拒绝核对；只有操作员已独立确认供应商调用停止时才能加`--confirm-no-active-run`，不能把超时等同已停止。

幂等键绑定操作员和用户，参数摘要防变参；重复核对不重复扣减，不同请求再次结算拒绝。迟到响应不再重复计token，冲突用量拒绝。unknown调用不会被自动重放为模型请求。

运维应定期查看pending列表（每次上限100，含调用/工作流/请求摘要与已报告token），结合原请求及供应商执行记录逐项核对，并设置人工处理时限。无独立证据时保持预占和pending，不自动过期退款；超过列表上限可依据日志中的调用ID直接核对或用受限SQL查询。代码提供可追踪的处理途径，不保证运维已执行对账，也不默认清空历史占用。

## 6. 项目金额控制的后续实现与限制

2026-10-18新增schema020、独立Go模型出口及项目累计金额预占/离线账单核对，说明见[MODEL-SPEND.md](MODEL-SPEND.md)。仅在隔离库及模拟供应商验证，未部署。真实价格、币种/额度、供应商费用上界和真实账单仍缺业务确认及外部证据；每用户金额归属和整个供应商账户硬停未验收。019格式校验、Token配额或本轮合成费用都不能单独关闭真实金额风险。
