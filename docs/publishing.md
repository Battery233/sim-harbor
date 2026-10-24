# 维护者发布指南

本文面向版本维护者，说明 Git 标签、GitHub Release 与 GHCR 镜像的发布流程。安装、升级和回退见[版本维护](releases.md)。

正式版本采用 `主版本.次版本.修订号`，候选版本使用 `-rc.N` 后缀，N 从 1 开始递增。

## 标签、Release 与镜像的关系

| 项目 | 作用 | 创建方式 |
| --- | --- | --- |
| 应用版本 `1.1.5` | 网页和 API 显示的运行版本 | 在源码与构建配置中维护 |
| Git 标签 `v1.1.5` | 固定本版源码对应的提交 | 本地创建后推送，或在 GitHub 发布页面创建 |
| GitHub Release | 展示版本说明、源码包及附件 | 在 Releases 页面基于标签发布 |
| GHCR 镜像 `:1.1.5` | Docker 安装与更新使用的构建产物 | `Release image` 工作流构建并上传 |

[Release image 工作流](../.github/workflows/release.yml)仅在收到 `v*` 标签推送后运行，负责镜像验证与发布；Release 页面由维护者另外填写和发布。本地提交、普通 `main` 分支推送均不触发镜像上传；工作流不提供手动触发或定时发布入口。

[Checks 工作流](../.github/workflows/ci.yml)在 PR、`main` 更新或手动触发时执行版本、文档与相应代码检查，不上传镜像或部署。它与标签触发的镜像发布各自保留检查结果，范围见[贡献指南](../CONTRIBUTING.md#自动检查)。

镜像发布前复用 `tools/ci_check.py`，核对后端、前端包及锁文件、Docker 标签、Compose 默认镜像与工具链版本，并检查仓库内文档链接。标签去掉 `v` 后须与通过检查的应用版本一致，版本可带 `-rc.N` 后缀；检查失败时停止发布。构建验证通过后，工作流使用 `GITHUB_TOKEN` 的 `packages: write` 权限上传 `ghcr.io/<仓库所有者>/<仓库名>:<版本>`，同时提供 `linux/amd64` 和 `linux/arm64`；不会生成 `latest` 标签或执行实例部署。包可见性及外部拉取权限需在 GitHub Packages 中单独核对。

## 发布顺序

1. 整理代码、文档、版本号与更新记录，运行 CONTRIBUTING 中与改动相关的检查。
2. 核对版本号一致：`internal/version/version.go`、`web/package.json`/锁文件、Dockerfile 标签、Compose 默认镜像与本版文档。
3. 按部署目标分别构建和验证架构镜像。使用合成数据检查启动、健康、备份/恢复和迁移确认。
4. 确定包含本版最终代码与文档的提交，核对工作区和提交：

   ```sh
   git status --short --branch
   git log -1 --oneline
   ```

   使用 GitHub Desktop 时，先推送 `main`，再在 History 中选中发布提交，通过 Create Tag 创建 `v1.1.5`，并单独推送标签。标签必须对应最终发布提交；已发布标签保持固定，后续修正使用新版本。
5. 打开 Actions → Release image，检查构建及发布结果，确认 Packages 中存在目标版本，并核对镜像的 amd64/arm64 架构清单。
6. 在 GitHub 的 Releases → Create a new release 中选择已有标签，填写标题和本版说明。RC 版本标记为预发布，不设为最新正式版，再 Publish release。也可在该页面创建新标签并选择目标提交，再发布以触发构建，步骤见 [GitHub 官方指南](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository)。
7. 分享镜像时核对 GHCR 包的可见性；公开使用测试匿名拉取，私有分享准备相应读取权限。复用与分发按 README 的许可说明处理。

GitHub Release、镜像发布与云端部署各有独立结果，按上述顺序核对后，再安排实例更新。

镜像发布实现参考 [GitHub 官方容器发布说明](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)与 [Docker 多架构构建说明](https://docs.docker.com/build/ci/github-actions/multi-platform/)。
