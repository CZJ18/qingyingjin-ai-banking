# P6 独立产品入口部署

当前本地镜像为 `qingrongjin-banking-web:sms-auth`，注册验码与密码找回使用开发短信收件箱。当前流程及部署见 [SMS-AUTH.md](../../services/banking-service/SMS-AUTH.md)，不代表真实短信发送。

当前入口为手机号/密码「登录 / 注册」，使用 `qingrongjin-banking-web:phone-auth`。新的部署与实际验收见 [PHONE-AUTH.md](../../services/banking-service/PHONE-AUTH.md)；以下 P6 镜像及令牌说明是历史记录。

完整运行边界、API 契约、用户令牌使用和验收见 [P6.md](../../frontend/ai-banking/P6.md)。默认访问 `http://127.0.0.1:8088`。

在仓库根目录运行：

```powershell
powershell -NoProfile -File deploy/banking-web/Setup-Local.ps1
powershell -NoProfile -File deploy/banking-web/Build-Image.ps1 -LocalDist `
  -NginxRuntimeImage mirror.gcr.io/library/nginx:1.28-alpine
docker compose --env-file deploy/banking-web/.env -f deploy/banking-web/compose.yaml `
  up -d --no-build banking-web
powershell -NoProfile -File deploy/banking-web/Verify-Web.ps1 `
  -OutputPath frontend/ai-banking/test-results/p6/deployment.json
```

先启动现有 Go / FastAPI 及数据库依赖。本 Compose 只管理静态 Web 容器，不创建业务数据库。外部网络 `qingyingjin_banking-egress` 由现有 Banking Compose 提供。

`-LocalDist` 每次先编译当前源码，再生成只包含静态资源的镜像。本轮镜像内 npm 下载出现 ECONNRESET，因此实际采用此路径；完整多阶段源构建尚未在该网络环境验收成功。构建日志中的 ECharts 大于 500 kB 警告仍存在。

nginx 以用户 101、只读根目录、去除所有 capability 运行。只有 `/tmp` 为受限 tmpfs。生产代理读写超时为 210 秒，关闭 API 缓存和写请求重试，透传 SSE；不注入签名密钥、Coze PAT 或模型密钥。只读健康检查不会签发用户身份或执行金融写入。
