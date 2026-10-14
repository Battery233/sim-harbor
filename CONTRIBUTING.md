# 开发指南

项目由 Go 后端和 Vue 前端组成。先阅读 [README](README.md)、[架构](docs/architecture.md)及[开发说明](docs/development.md)。当前未添加许可证，提交贡献前请先与维护者明确授权范围。

## 变更与验证

1. 保持改动聚焦，涉及行为变化时同步使用文档。
2. 使用合成数据验证，不把私有配置或实体短信带入测试。
3. 针对变动运行检查；只改文档时核对链接、命令与实现一致性即可。
4. 说明解决的问题、最终行为和实际完成的验证；硬件结果与模拟结果分别描述。

后端完整检查：

```sh
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
```

前端完整检查：

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run format:check
npm --prefix web run build
```

提交前运行 `git diff --check`。实体设备测试、网络故障、收费动作和云端部署不会由这些检查自动完成。

## 问题反馈

说明应用版本、部署方式、系统/架构、板子型号与固件、复现步骤及脱敏错误摘要。安全问题请按 [SECURITY.md](SECURITY.md) 处理，不公开提交凭据、完整报文或数据库。
