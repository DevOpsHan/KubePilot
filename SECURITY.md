# 安全策略

## 支持范围

安全修复面向 `main` 分支的最新版本。尚未发布正式版本前，不承诺为历史 commit 单独提供安全补丁。

## 报告漏洞

请勿通过公开 Issue、Discussion 或 Pull Request 披露未修复漏洞。请在仓库的 **Security** 页面使用 GitHub 私密漏洞报告功能：

<https://github.com/DevOpsHan/KubePilot/security/advisories/new>

报告中请尽量包含：

- 受影响的 commit、组件和部署方式。
- 可复现的最小步骤、预期影响和攻击前提。
- 已完成脱敏的日志、请求或概念验证。
- 你建议的修复方向，以及是否计划公开披露。

不得提交真实 kubeconfig、Token、Cookie、密码、模型 API Key、Webhook、数据库文件或其他第三方凭据。需要证明凭据泄露问题时，请使用立即失效的测试凭据。

维护者会先确认报告范围，再通过私密安全公告协作修复与披露。修复发布前，请不要在公开渠道讨论漏洞细节。

## 部署安全

- 默认 Compose 仅将 KubePilot Console 绑定到宿主机 `127.0.0.1`，MySQL、Redis 和 KubePilot Core 不发布宿主机端口。
- 远程部署应设置独立数据库密码，使用 HTTPS 反向代理与防火墙，并在首次登录后立即修改默认管理员密码。
- 自动生成的加密密钥保存在 `backend-data` 卷。生产备份必须同时保护数据库和该密钥卷，或安全备份显式设置的 `AIOPS_SECRET_KEY`。
- 导入的 kubeconfig 应使用最小权限身份，不建议直接使用集群管理员凭据。
