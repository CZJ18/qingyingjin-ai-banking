# 模块4 Billing Agent 交付与实际验证

2026-10-08。Go账单分析服务、六个受保护工具、报告持久化、JSON Schema和API契约已实现并运行在Docker。Coze原生Agent Prompt、Plugin OpenAPI、只读路由Workflow配置/画布参考生成器已提供；**真实Agent发布及模型联调尚未完成**，本机没有运行中的Coze、PAT或已发布Billing workflow_id。

目录、API、curl、运行命令、模型/工具配置和协同规范见 [MODULE4.md](../services/banking-service/MODULE4.md)；架构见 [设计](superpowers/specs/2026-10-08-billing-agent-design.md)；主API见 [OpenAPI](../services/banking-service/api/openapi.yaml)，插件文档见 [Plugin](../services/banking-service/api/coze-plugin.openapi.yaml)。

## 已实现

- Entity/DTO、Repository、Service、Handler、Schema和确定性规则分文件组织；复用模块2账户/交易/预算实体、原有鉴权/审计及模块3Schema/Coze客户端。
- 在一个MySQL REPEATABLE READ只读事务内读取真实授权数据；整数分完成分类、日/周/月、等长期间趋势、完整自然月预算和异常检测；没有交易表/账户表写入。
- 六个工具get_transactions、get_account_summary、analyze_spending、compare_budget、detect_anomalies、generate_billing_report；用户JWT和服务端绑定工具身份两套受保护路由。
- 统一Report包含全部要求字段，附真实交易ID、时间、整数金额、原类别、商户、预算证据和审计/任务关联信息。
- 003增量迁移仅新增banking_billing_reports；成功分析JSON与原有审计同事务保存；按用户读取已存report_id供Supervisor/Savings使用。
- 自然语言查询使用已核实Coze同步workflow/run协议，输出只读工具/时间/原文证据；异常模型输出、金额/身份/执行字段及非法任务不被接受。无模型配置返回503，不返回假LLM结果。
- 分类未知时不猜测、不回写；疑似重复仍完整计入支出；异常只是待核查；confirmed_savings固定0，避免把未确认异常金额当资金。没有餐饮金额健康诊断或体重/限制饮食奖惩。

统计别名归并与预算原始类别口径区分：category_breakdown为分析大类，预算category沿用Core持久原类别的精确匹配和预留规则，不自动重写预算分类。整月总支出包括所有外部消费，不因类别未匹配而漏算。预算scope明确为所有用户账户/完整UTC月，与单账户/部分期间消费不能混作同口径。

## 已执行测试与部署

| 检查 | 实际结果 |
|---|---|
| Go全量单元 `go test -json ./... -count=1 -timeout 90s` | 68个测试/子测试记录，11个测试包通过，0失败 |
| Go全量真实集成 `go test -tags integration ./tests/integration -json -count=1 -timeout 120s` | 42个测试/子测试记录通过，含模块2/3回归、8个Billing分析子场景和3个自然语言协议/真实数据库子场景，0失败 |
| Billing API | 无交易、跨月、分类、重复、大额异常、全月预算、六工具/分页不影响合计、报告持久化/越权、服务工具绑定及未配置Coze行为通过 |
| Billing规则/模型协议 | 空数据、排除内部转账、错误类别、重复、未知/异常金额、有效路由读取真实空数据库、模糊范围不生成分析、恶意金融任务/身份拒绝、五笔历史中位数、时区边界、等长趋势、整数溢出及异常模型JSON通过 |
| `go vet ./...` / `go mod verify` | 退出0，all modules verified |
| Coze参考canvas | Python unittest 1个通过；复用已核实三节点原生结构，无工具/聊天历史；不代表发布或真实模型运行 |
| OpenAPI | 49个主API路径/53个方法与路由一致，8个只读Plugin路径；成功集成请求和全部响应通过契约校验；插件无资金写入/授权端点；根据实际exec_tool.go的Type/Properties遍历，插件响应展开allOf并显式声明字段类型，递归契约测试通过 |
| SQL迁移 | 001/002校验保留，003实际执行及重复验证；Compose迁移容器退出0；未重置数据卷 |
| 镜像 | Go1.26.4本地交叉编译linux/amd64，distroless nonroot运行镜像构建成功，module4实际运行 |
| 健康 | MySQL、Redis、Banking Service healthy；healthz/readyz HTTP200，schema要求003 |
| 实际容器工具 | 用户42六工具及工具密钥调用成功；读取3笔真实已有外部流水：收入100000分、支出24000分；开户/内部转账未混入 |
| 资金不变 | 同一用户既有账户在六工具分析前后完全一致；集成验证record_only分析不改1000000分余额 |
| 报告持久化 | GET读取实际保存报告成功；Docker服务重启后该报告、金额和关联信息保留 |
| 当前自然语言API | 因Coze未配置实际返回503 COZE_UNAVAILABLE；不能声称真实模型路由/报告解释已验证 |
| 原Coze源码 | 模块1记录的20个原源码文件SHA256全部未变；没有前端开发、原生Agent/Workflow重写 |

证据：[测试与源码核对](banking-module4-test-results.json)、[Docker实际API返回](banking-module4-docker-api.json)、[健康与重启持久化](banking-module4-health.json)、[文件修改及SHA256](banking-module4-files.json)。原始日志为本机services/banking-service/output/module4/unit.jsonl及integration.jsonl。

## 修改清单

新增：internal/billing/model.go、repository.go、analyzer.go、schema.go、parser.go、service.go、analyzer_test.go、parser_test.go及schemas两份JSON；httpapi/billing.go；migration/003_billing.sql；tests/integration/billing_test.go；scripts/generate_billing_openapi.py；MODULE4.md；coze/banking/billing-agent的Agent配置、Prompt、只读查询Prompt、画布生成器、测试/忽略文件；架构、交付与证据文档。

修改：cmd/server装配/003就绪检查；config增加BANKING_BILLING_WORKFLOW_ID；identity增加billing:read工具范围；httpapi/server增加模块装配；migration追加003；cmd/migrate更新提示；主/Plugin OpenAPI及生成器；契约测试扩大受保护只读Plugin允许清单；Compose/Build-Image改module4并传配置；.env.example、相关README。001/002 SQL、Coze核心源码、Go依赖文件和本机真实凭证未修改。

当前镜像qingyingjin-banking-service:module4。采用-LocalBinaries实际构建运行，完整Dockerfile远程源码构建路径本轮未验证。Git元数据不可用，未创建提交，采用文件清单记录实现。

## 仍需配置与边界

1. 真实Coze模型、原生Billing Agent、插件挂载/发布和Billing只读路由Workflow发布；填写可达地址、PAT及实际workflow_id后进行真实对话联调。测试HTTP fixture只是协议和异常验证，不是实际模型成果。
2. 当前工具凭证固定绑定一个已授权用户，只适用于该用户的私有Agent上下文；不同金融用户须独立凭证或后续用户绑定凭证注册，不能让模型传user_id切换租户。用户REST始终按各自JWT隔离。
3. 载入记录上限10000，期间<=366天；超限明确失败，不伪装完整报告。查询明细最多100笔/页，合计不分页截断。重复规则检测查询期间内的近时同商户记录，未声称全量欺诈识别。
4. Savings/Supervisor接收权威Report和report_id，依同用户授权上下文读取。自动退款、取消订阅、转账、执行调度、金融授权与风控不属于Billing模块；分析报告和模型文本不能成为资金授权。
