# 版本发布、Docker 更新与验证

版本号采用 `主版本.次版本.修订号`，发布标签为 `v1.0.0`，镜像标签为 `1.0.0`。应用在已认证的 `/api/state` 和健康接口 `/api/health` 返回 `version`，用于核对实际运行版本。

## 发布顺序

1. 在一个完整提交中整理代码、文档、版本号和更新记录，运行 CONTRIBUTING 中的检查。
2. 核对版本号一致：`internal/version/version.go`、`web/package.json`/锁文件、Dockerfile 标签、Compose 默认镜像与本版文档。
3. 按部署目标分别构建和验证架构镜像。使用合成数据完成启动、健康、备份/恢复和迁移确认检查。
4. 由维护者明确推送提交和发布标签，GitHub Release 引用该标签并附上本版变化与使用说明。发布后的修正使用新提交及相应版本标签，便于追溯。
5. 标签推送会触发仓库的 Release image 工作流，在 GHCR 构建 amd64/arm64 镜像。在工作流完成后核对架构清单，确认镜像发布成功。
6. 分享镜像时设置并核对 GHCR 包的可见性；面向公开使用时测试匿名拉取，私有分享则准备相应读取权限。复用与分发按 README 的许可说明处理。

源码、标签、Release 和镜像各有独立的发布结果，依次核对后，再按自己的维护窗口更新云端实例。

## 本机构建与镜像发布

没有使用 GHCR 时，可在目标服务器从指定版本源码构建：

```sh
git fetch origin --tags
git checkout --detach v1.0.0
docker build --platform linux/amd64 -t sim-harbor:1.0.0 .
docker image inspect sim-harbor:1.0.0 --format '{{.Os}}/{{.Architecture}}'
```

准备新镜像期间保留现有运行配置与旧镜像。应用镜像、业务数据和私有配置分别管理，更新时更容易验证和回退。

使用已成功发布的包时：

```sh
docker pull ghcr.io/battery233/sim-harbor:1.0.0
docker buildx imagetools inspect ghcr.io/battery233/sim-harbor:1.0.0
```

确认包含目标架构。生产环境可进一步固定镜像清单摘要，使升级和回退目标可追溯。私有包使用具备 `read:packages` 权限的凭据在主机私密登录。

## 已有云端实例升级

下面以已有 Compose 项目 `sim-cloud`、原目录中的 `compose.yaml` 和 `deploy/cloud.private.yaml` 为例。全新安装按 Docker 文档操作；既有实例继续使用原项目名、配置、端口、数据卷和数据子目录。运行目录和卷名可继续沿用自己的命名习惯。

### 1. 升级前保留可回退副本

在网页保存当前数据库备份；另行私密备份 `.env`、初始化 JSON、私有 Compose 文件和数据目录内 `system.private.json`。记录当前镜像 ID、版本、数据卷、数据子目录、端口及卡状态。备份后如仍有新增记录，切换前再生成最终备份；保留旧镜像与数据卷，供验收与回退使用。

重要实例建议先在隔离数据副本上完成恢复演练。

### 2. 先准备新镜像，再改版本覆盖文件

按上节从标签构建或拉取固定镜像，确认成功后，在现有项目根目录创建 `deploy/release.private.yaml`。已有该文件时核对当前内容后编辑：

```yaml
services:
  sim:
    image: sim-harbor:1.0.0
```

使用 GHCR 时将 image 换成已验证的完整镜像引用。这个文件集中设置发布镜像，端口、配置、数据挂载与运行策略沿用原文件。旧私有文件若固定了 `sim:旧版本`，本文件必须放在最后以使新镜像生效。

### 3. 核对配置并切换

```sh
docker compose --env-file .env -p sim-cloud \
  -f compose.yaml -f deploy/cloud.private.yaml -f deploy/release.private.yaml \
  config --quiet

docker compose --env-file .env -p sim-cloud \
  -f compose.yaml -f deploy/cloud.private.yaml -f deploy/release.private.yaml \
  up -d --no-build --pull never --wait --wait-timeout 60

docker compose --env-file .env -p sim-cloud \
  -f compose.yaml -f deploy/cloud.private.yaml -f deploy/release.private.yaml ps
```

应用重建期间连接会短暂中断，板子随后重连，原配置和数据卷继续使用。若 `up` 失败，保留现场数据与日志，按[故障排查](troubleshooting.md)定位后继续。

以后重启、停服和升级继续携带相同三个文件。`config --quiet` 防止输出展开后的密钥。

### 4. 升级验收

- 容器健康、镜像版本/架构正确；网页或已认证 API 的 `version` 为 `1.0.0`。
- 数据目录、卷、HTTP/TCP 端口及私有配置未意外变化；历史、卡名称、备注和推送开关保持。
- 板子重连后查询新鲜状态，确认当前会话、两卡身份和注册情况。
- 自然到达的短信能入库，按卡通知和点击链接正常；如需 Bark 连接测试，由操作者确认执行。
- 已有恢复保护在升级后继续保留。需要外发时，按“数据维护”的迁移确认流程核对，再按需启用外发并逐条验证目标、费用和结果。
- 结果待定时查询原操作，核对后保存本版新备份，并安排稳定运行观察。

## 回退

先保存当前最新数据与配置，再将版本覆盖文件改回保留的旧镜像，用相同项目名和挂载重建，随后核对。

回退前核对旧镜像与当前数据的兼容性。需要恢复兼容备份时，先停止应用并保留当前目录，再恢复到全新目录、协调运行配置，保留两份数据供核对。

恢复旧备份时，结合保留的新库核对备份后的新增记录；恢复实例重新进入外发保护，按迁移确认流程处理。若需完整保留当前业务状态，也可选择保留现场并修复当前版本。

回退操作参照[数据维护](data-maintenance.md)。新版本稳定、备份可用且回退窗口结束后，可清理旧镜像，同时保留当前使用的数据卷。

镜像发布实现参考 [GitHub 官方容器发布说明](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)与 [Docker 多架构构建说明](https://docs.docker.com/build/ci/github-actions/multi-platform/)。
