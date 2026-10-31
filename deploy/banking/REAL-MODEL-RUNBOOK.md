# 真实模型受控验收操作

当前受限真实AI入口为 <http://127.0.0.1:30440/assistant>，实际验收见 output/banking-ai-final-20261031/advice-candidate/authorized-paid/acceptance-index.json。原30436、30438及历史应用/数据库保留。当前Go业务二进制为090f23a8ef676c282629a6aad37fa2b8bdc0d7b8f0bba3150cd6866ef5ac6065，核心内与独立网关的实际二进制均为d0914de91560c6165d8640971880b57a8aa023b50615e1bff64ee370b63c637b；新行为来自独立Coze克隆中已发布的精确Billingv0.0.9提示词，不能据此声称所有最新Go源码重新部署。

修复版已实际通过 Vue→Go/Python/LangGraph→自托管Coze Supervisorv0.0.10＋Billingv0.0.9→真实DeepSeek→Go只读工具/隔离SQL→Vue的一项账单任务，任务lg-8cd2747a-6ce7-4899-b9c9-bd16866a07fb。两次真实响应共1529token，给出可选购物延期、使用或修复现有物品、每周记录支出/必要购买/未满足需求并复核。Go的收入5000元、支出3900元及61笔合成记录与SQL/页面一致；结果刷新后恢复，没有金融执行。

当前固定周期只允许同一用户、账户27、2026年9月61笔合成账单及同一精确只读指令。新Approval18次已用2次、剩余最多16次；保留先前v0.0.8的2次及占额，全项目仍最多20次/0.10USD，当前审批余额0.09016960USD，并发1。没有新增额度、自动分析、补额或续期，原日调用/Token限制仍可能先拒绝。固定截止北京时间2026-11-01 19:58:19，不延长。

仅已验收本项建议和链路，未证明实际节省500元、七天复盘或任意自然语言问题均可处理。正式供应商原始消费账单仍缺，4次reported和0.00983040USD累计预占未正式结算；旧unknown/其他占额、历史任务和失败日志保留。初版v0.0.8的建议缺项没有被改写为成功，修复版的独立结果见上述证据。

操作步骤、精确指令和停止脚本见 output/banking-ai-final-20261031/advice-candidate/authorized-paid/HOW-TO-USE.md 及rollback.ps1。到期后可通过保留的30436/30438账户历史查看已保存结果，不自动续用本周期。

## 准备输入

所有材料由项目操作者本人准备、审核；费用审批、精确计划的独立确认、真实调用授权仍是三个明确步骤。

复用以下既有契约，不从历史报告自动导入值：

- `model-integration-manifest.json.example`：schema v1 非敏感信息；启用的工作流集合必须与审批主工作流及 additional_workflows 精确相等。
- `model-spend-policy.json.example`：审核后的准确模型、同币种输入/缓存命中/输出价格、官方证据摘要、价格版本、有效期和 token/累计限制。
- `model-call-approval.json.example`：本轮新 approval_id/run_id、Policy.Hash、预算/次数/并发/token、用户、持久任务请求摘要、数据库和数据范围、工作流与原始 Canvas 摘要。审批文件原始 SHA256 须另行确认。
- `model-acceptance-instructions.json.example`：精确测试输入、相同任务摘要、注册的只读/建议工具、预期数据、比对步骤、失败处置。工具字段是 `skill_id`、`skill_version`、`agent_type`、`tool`、`execution_mode`；例如已有 `billing_analysis/1/billing/generate_billing_report/read_only`。这不是工具授权。

任务摘要必须来自现有任务协议和实际账户绑定，不能用任意自然语言字符串的 SHA 替代。真实 Coze origin、角色/当前发布版本/原始 Canvas、受保护 scope、固定出口和价格/权限材料需要实际核验；历史发布记录及引用只用于定位。

凭据只通过获批准的专用秘密渠道注入；禁止放入上述文件、命令行参数、截图或报告。未得到读取凭据和 scope 探测的授权前，只运行离线步骤。

真实 `coze` 和 `direct_provider` 网关统一使用受保护注入：安全环境中的
`BANKING_MODEL_SUPPLIER_API_KEY`，或受保护网关配置里仅填写
`BANKING_MODEL_SUPPLIER_SECRET_FILE` 的只读 Secret 挂载路径。两者同时存在会拒绝。
真实模式不接受普通配置文件中的 Key 值；旧 Coze 配置需要先迁移至该安全渠道。
审批、计划独立确认、Policy 与目标数据库/供应商绑定仍先于凭据读取；该支持不授予任何调用权。

## 生成未授权候选

`--prepare-execution-plan` 复用现有校验器，读取显式的非敏感 manifest、审批、Policy 和验收说明。它不读取 env、证据引用或供应商配置，不连 SQL、不监听、不探测 scope、不创建费用预占、grant 或金融意向。

下面命令中的路径、摘要、run ID 和数据库必须由操作者替换为本轮实际材料，不能直接运行空模板。示例以 Linux shell 表示：

```sh
./model-gateway --prepare-execution-plan \
  --readiness-manifest-file /review/manifest.json \
  --approval-file /review/approval.json \
  --approval-sha256 APPROVAL_RAW_SHA256_CONFIRMED_SEPARATELY \
  --run-id CURRENT_RUN_ID \
  --policy-file /review/policy.json \
  --expected-database EXACT_ISOLATED_DATABASE \
  --acceptance-instructions-file /review/instructions.json > /review/candidate.json
sha256sum /review/candidate.json
```

只在退出码 0 且 status=`CANDIDATE_NO_EXECUTION` 时才形成候选。不能组合 `--preflight`、`--offline-preflight`、`--probe` 或执行确认参数。候选仅记录本地绑定，外部材料/凭据/账本均为 NOT_INSPECTED，无余额、余次或并发快照；空 blockers 也不代表外部就绪。

Windows 上避免 PowerShell 的文本重定向改变编码。可用 Python `subprocess.run([...], stdout=open(path, 'wb'), check=True)` 将原始 stdout 字节写入候选文件，再计算该文件摘要。不要对已确认计划重新格式化、添加 BOM 或换行；任何字节变化都必须重新审核和确认。

## 独立确认后做普通预检

操作者审核具体候选的全部内容后，通过独立步骤提供该**精确文件**的确认摘要。不要从生成结果自动填入确认参数。仅候选独立确认还不能启动网关；需要另行允许受保护配置读取、只读权限和 scope 核验。

普通预检沿用 `--preflight`，加上本轮审批、Policy、manifest、workflow-policy-file、目标数据库、gateway-origin，以及 `--execution-plan-file`、`--execution-plan-sha256`、`--confirm-execution-plan-sha256`。价格证据、权限审核、受保护配置和 `--allow-runtime-scope-probe` 按既有门禁显式提供；无此授权不运行这一步。权限审核绑定审批语义摘要、实际凭据指纹及证据；不在报告输出凭据。

预检只查询账本并执行已授权的只读核验，不发模型请求、不预占费用。READY_SNAPSHOT_ONLY 是新的快照文件，其原始字节摘要须再次独立确认；旧候选摘要不能授权新快照。

## 单独授权真实调用

审批有效、当前发布 Canvas/scope/角色 pins、实际网关 origin、价格与凭据权限、用户/任务/数据库/工具范围及余量全部核验后，向操作者展示 READY 精确计划、最大请求数/并发/token、币种/最大费用风险、测试输入和未知结果处置。仍须取得本轮明确的真实调用授权，才能启动并进入受控验收。

启动时现有门禁重验 READY 精确确认；每个 HTTP 准入/调用重验计划字节，原子账本约束继续生效。候选、manifest、预检均不能替代任何授权。超时、断连或未知结果保留预占/次数/幂等，不自动重放。reported、usage、估算、预占与正式 settled 分开；正式账单须独立对账。

## 直连账单二次解读（需独立的新审批）

隔离 `test` 环境的 Go 核心可显式设置 `BANKING_DIRECT_BILLING_EXPLANATION=true`。
它启用本地 `direct_provider_billing_explanation` 适配器；这不是 Coze 已发布工作流 ID。
`BANKING_BILLING_AGENT_WORKFLOW_ID` 等 Coze 配置仍要求真实数字 ID，直连模式下保持为空。
未设置该开关时原行为不变；非法开关值拒绝启动。Subscription、Savings、Lifestyle 不因此获得直连资格。

现有显式 billing 只读任务可使用 `use_coze=true` 请求解读。该字段是既有执行 DTO 的历史命名，
实际供应商路径由服务端配置决定，不能凭字段为 true 声称 Coze 验收通过。
直连 Supervisor 仍禁止模型生成 `use_coze=true` 的任务，因此不会在已批准的单次规划后自动追加付费调用。

执行顺序沿用现有工具执行器：Go 工具校验归属并返回报表 → 过滤商户原文、身份和波动的
task/report/audit/snapshot 字段 → 保留整数汇总、分类和允许的证据引用 → 同一费用网关 →
严格结构、证据白名单和既有定性文字校验 → 原任务结果返回。金额始终使用原 Go 报表。
稳定的 `billing_report` 引用由 Go 映射回本次真实报表 ID，不允许模型选择其他报表。

准备精确计划时复用 `coze.DirectBillingExplanationRequest` 的请求正文契约。
审批仍绑定 `direct_request_sha256`、用户、原始任务摘要、数据库、模型、Policy 与 token 限额；
没有新增通配正文、第二套预算或自动审批。事实变化会改变请求哈希，旧审批无法放行。
规划请求的摘要不能授权解读请求。真实调用前仍需新审批、权限审核、精确计划及 READY 独立确认。
使用独立请求审批时可先提交显式只读任务而不再次调用规划模型，避免未经批准增加次数。

正式供应商账单仍只通过既有 `model-spend-reconcile` 的独立审阅入口处理。
usage、估算和余额变化均不自动等同于正式已结算；没有账单证据时保留 reported 与占额。

## 提示词和范围修复后的复验

Billing v0.0.8 与 Supervisor v0.0.10 在历史独立克隆中已有发布读回及调用记录，原始 Canvas 候选位于 output/banking-user-closure-20261030/real-review/。v0.0.8 要求结论、依据、局限、建议、具体行动和复核方法，JSON 字段及512输出上限不变。历史发布成功不等于下一轮当前发布版本或 scope 已核验；新实例必须重新做获授权的只读核验。共享质量评估只是格式和行动结构的验收辅助，不能证明节省金额可行，也不会把旧任务改成失败或自动发起重试。

修改 Canvas 会改变 SHA256，必须更新本轮 Approval 的主/附加工作流绑定、manifest、独立克隆的受保护 scope 和精确执行计划；不能把候选文件写成当前发布证据。发布读回和只读 scope 核验通过后，仍按上文独立确认 READY，再明确决定付费。准备候选不会授予这两项确认。

初版v0.0.8建议缺项后已停止该批处理，保留2次reported和0.00491520USD预占。随后操作者独立确认了v0.0.9发布/权限/计划及新的精确READY和付费决定；新Approval只承接18次/0.09508480USD，不增加额度。v0.0.9已完成两次真实复验并通过具体行动、替代办法、按周复盘要求，当前只启用余下最多16次供用户主动提交相同精确任务。全部4次及0.00983040USD预占保留，项目总上限20次/0.10USD，原截止不延长；新ID不能重新申请20次或清理旧记录。

正式对账需要供应商原始消费证据、真实币种、真实金额及可审核的请求归属。本轮已只读提取三个现存隔离数据库中的33条未结算记录（27 reported、6 unknown），见 output/banking-ai-final-20261031/billing/unsettled-lookups.json；这不是全历史数据库的合计，未出现在所查 PostgreSQL 中的 banking_stage17_real_test 标记 NOT_INSPECTED。官方余额接口不能替代消费账单，充值发票也不能替代逐笔消费归属；若原件币种与Policy不同，应停止并另行审阅，不自行折算为USD结算。当前没有原件，不能创建正式结算记录；后续步骤见同目录 supplier-billing-handoff.md。

## 回滚约束

隔离 test 环境也可单独设置 `BANKING_DIRECT_LIFESTYLE_EXPLANATION=true`，启用
`direct_provider_lifestyle_explanation` 本地适配器，默认关闭。它只解读已有
`lifestyle_planning/1` 原生目标/方案，不增加 Coze 工作流授权或金融工具权限。
复用 `coze.DirectLifestyleExplanationRequest` 生成精确请求正文；审批必须重新绑定
该正文摘要、原生方案、专用用户、任务和数据库。旧账单规划审批不能授权新目标。
模型只选择已核实的 finding_ids，Go 保留金额和具体措施；缺失信息/目标冲突结论不得省略。
未授权的目标适配不能接入已有付费周期，也不能据此声称完成真实 Coze 目标建议验收。

CLI 准备和直连解读验证均不要求替换现有业务容器。新核心、网关制品在独立测试容器中核验，未通过本轮明确准入前不启动真实模型出口。回滚只停止对应阶段新建的容器，保留原应用、全部卷、历史 unknown/reported、审批绑定和测试证据。源码回退仅恢复本轮实际修改文件，不回滚或清空账本。禁止通过数据库迁移、清账或重放 unknown 恢复可用额度。
