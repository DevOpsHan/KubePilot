## 修改内容

请说明本次修改解决的问题以及主要实现。

## 修改类型

- [ ] 新功能
- [ ] Bug 修复
- [ ] 重构或性能优化
- [ ] 文档、构建或测试

## 验证结果

请列出实际执行的测试与结果。

- [ ] `go test ./... -count=1 -timeout 60s`
- [ ] `go vet ./...`
- [ ] `go mod tidy -diff`
- [ ] `npm test`
- [ ] `npm run check:i18n`
- [ ] `npm run lint`
- [ ] `npm run build`
- [ ] `docker compose config --quiet`
- [ ] 已按改动范围完成 Compose 构建或启动验证

## 风险检查

- [ ] 未提交真实密钥、密码、kubeconfig、数据库、日志或个人信息
- [ ] 用户可见行为已同步更新 README
- [ ] 已检查 `docker-compose.yml`、Dockerfile、`.env.example` 和 `config.docker.yaml` 是否需要同步
- [ ] 集群写操作仍受权限、审批和审计约束
