# 贡献指南

感谢你参与 KubePilot。项目聚焦可控、可审计的 Kubernetes 智能运维能力，提交前请先确认改动符合这一边界。

## 开始之前

- Bug 和功能建议优先通过 GitHub Issue 讨论。
- 安全漏洞不得提交到公开 Issue，请遵循 [`SECURITY.md`](SECURITY.md)。
- 不得提交真实密钥、密码、kubeconfig、数据库、日志或个人信息。
- 贡献内容将按照项目的 Apache License 2.0 许可证发布。

## 开发环境

- Go 版本以 [`go.mod`](go.mod) 为准。
- Node.js 20 和 npm。
- Docker Engine 或 Docker Desktop，以及 Docker Compose v2。

完整启动和本地开发方式见 [`README.md`](README.md)。仅验证部署流程时，可以直接执行：

```bash
docker compose up --build -d
```

## 开发流程

1. Fork 仓库并从最新 `main` 创建主题分支。
2. 保持修改范围单一，不在同一提交中混入无关重构。
3. 新功能和 Bug 修复应补充自动化测试。
4. 用户可见行为、配置或部署方式变化时同步更新 README 和示例配置。
5. 提交前运行与改动范围匹配的全部检查。
6. 使用清晰的 Pull Request 描述说明行为变化、风险和验证结果。

提交信息使用 Conventional Commits，例如：

```text
feat(sync): 增加同步失败原因分类
fix(compose): 修复 KubePilot Core 健康检查
docs: 补充远程部署说明
```

## 质量检查

KubePilot Core：

```bash
go test ./... -count=1 -timeout 60s
go vet ./...
go mod tidy -diff
```

KubePilot Console：

```bash
cd web
npm ci
npm test
npm run check:i18n
npm run lint
npm run build
```

Compose：

```bash
docker compose config --quiet
node scripts/check-compose-security.mjs
docker compose build
node scripts/check-docker-secret.mjs kubepilot-core:v0.1
```

涉及镜像、端口、环境变量、健康检查或启动顺序时，还必须在隔离环境中实际启动四个服务并验证 KubePilot Console 与 Core 健康接口。

## KubePilot Console 与国际化

- 新增界面文案时同时维护中文和英文资源。
- 运行 `npm run check:i18n`，确保语言资源键一致且不存在硬编码界面文案。
- 用户输入、日志、YAML、PromQL 和资源名称属于业务原始数据，不应自动翻译。

## Kubernetes 安全边界

- 模型输出不能直接决定实际写操作目标；KubePilot Core 必须从受信任的集群对象反查目标。
- 集群写操作必须经过权限检查、人工审批和审计。
- 测试不得连接未经授权的生产集群。
- Issue 和 Pull Request 中不得粘贴未脱敏 kubeconfig 或 Secret 内容。
