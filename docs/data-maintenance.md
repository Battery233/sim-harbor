# 数据导出、备份与迁移

从日常归档到本地/云端迁移，SIMHarbor 提供短信导出、一致备份、完整性检查和受控恢复，让数据随自己的部署选择流转。

## 短信导出

在“数据维护”选择卡身份、号码和日期范围，下载 JSON 或 CSV。JSON 保留原文，CSV 对公式样式文本加保护前缀；导出保留原有已读状态与任务状态，适合独立归档。

单次支持最多 10,000 条 / 32 MiB；较大范围可按日期分批导出，保证每个文件完整。JSON 使用 `format_version=2`，包含筛选条件与记录；导出包含私人内容，请妥善保存。

## 一致备份

在网页勾选私人数据保存确认，生成并下载备份 ZIP。应用通过 SQLite Online Backup API 取得包含业务库与 manifest 的一致快照，日常运行中即可完成备份，并纳入 WAL 中的有效数据。

单份备份支持最大 256 MiB 的数据库和 272 MiB 的 ZIP，包含短信、来电、卡身份/偏好、发件历史与通知任务。`system.private.json`、环境变量和日志分别保存；完整迁移时一并私密备份配置与凭据。ZIP 未加密，SHA-256/CRC 用于完整性检查，保存建议见[安全说明](../SECURITY.md#私有数据与备份)。

## 检查与恢复：源码工具

在项目根目录使用与备份兼容的程序版本。先检查：

```sh
GOTOOLCHAIN=local go run ./cmd/maintain inspect -backup /private/path/sim-backup.zip
```

工具返回版本、数量及 `archive_sha256`，以摘要形式完成核对。核对后恢复到**不存在的新目录**，将下面占位摘要替换为 inspect 返回值：

```sh
GOTOOLCHAIN=local go run ./cmd/maintain restore   -backup /private/path/sim-backup.zip   -to data/restored-instance   -confirm-archive REPLACE_WITH_ARCHIVE_SHA256
```

恢复到独立新目录后，原库与配置继续保留，便于比对与回退。再准备指向新目录的初始化配置：原生运行示例目标为 `data/restored-instance`，容器运行则使用卷内路径。恢复工具与备份版本应兼容，原始备份留存。

没有网页时，可停止占用该目录的应用，再执行停服备份。输出文件必须不存在：

```sh
GOTOOLCHAIN=local go run ./cmd/maintain backup   -data-directory data/instance   -output /private/path/sim-backup.zip
```

应用与维护工具共用进程锁，协调目录占用；保留 `.sim.lock` 和恢复标记，由工具完成检查与状态管理。

## Docker 维护工具

镜像内的工具为 `/app/sim-data`。以下假设采用[Docker 文档](deployment-docker.md)中的镜像和项目名，宿主私有备份目录 `/srv/sim-backups` 需自行创建、放入所选 ZIP，并让容器 UID 65532 可读。工具以非 root 运行，请按容器用户权限准备备份目录。

先独立检查备份包，临时内存目录用于校验解包，容器退出后释放：

```sh
docker run --rm --network none --read-only --tmpfs /data:rw,uid=65532,gid=65532,mode=0700,size=640m   --mount type=bind,src=/srv/sim-backups,dst=/backup,readonly   --entrypoint /app/sim-data sim-harbor:1.0.0   inspect -backup /backup/sim-backup.zip
```

恢复到卷内新子目录，先确认该卷属于自己的应用、目标不存在。恢复不需要启动应用：

```sh
docker run --rm --network none --read-only   --mount type=bind,src=/srv/sim-backups,dst=/backup,readonly   --mount type=volume,src=sim_sim-data,dst=/data   --entrypoint /app/sim-data sim-harbor:1.0.0   restore -backup /backup/sim-backup.zip   -to /data/restored-instance   -confirm-archive REPLACE_WITH_ARCHIVE_SHA256
```

`/data/restored-instance` 是容器内路径，在 Docker 卷中，不是宿主同名目录。确认恢复完成后，在专用初始化配置中将 `data_directory` 指向此路径，单独设置对应私有凭据再启动。运行配置按目标部署方式单独准备。

## 恢复后的保护

恢复保留卡备注、固定身份、按卡通知偏好和历史，新到收件按正常配置处理。同时取消旧发送预览，保留待核对的外发结果，并将旧待通知任务置为 `restore_held`，让新实例从清晰的任务状态接续运行。新增外发在完成迁移确认后开放。

恢复保护用于在迁移后核对完整收费历史，避免重复发送。通过下方迁移确认流程解除，配置中的外发开关随后继续独立管理日常发送。

## 迁移确认后恢复外发

在“数据维护”完成历史核对、备份与确认，即可恢复外发能力。确认动作保存迁移记录，实际短信仍按逐条预览与确认流程发送。

1. 停止原实例继续外发，以后只由当前实例管理发送。
2. 核对原实例备份之后的完整发送尝试与结果。未知结果可能已产生费用，按原操作核对；历史尚待补齐时，先保留保护。
3. 在当前实例上方“数据库备份”生成并下载一份新备份，另行保存配置和凭据。
4. 在下方逐项确认“原实例已停止”“历史完整”“未知结果已核对”“备份已保存”，点击“预览恢复外发”。
5. 核对预览后，再勾选最终确认，点击“确认解除恢复保护”。核对完成后立即生效。
6. 页面读回已保存状态。若系统配置仍关闭外发，需单独开启并重启应用；之后每条短信仍需预览、费用和目标确认。

备份与一次性预览在当前进程内有效 15 分钟，并与当前恢复身份、发送历史绑定。重新备份会更新预览依据；应用重启或相关状态变化后重新备份、预览。确认响应待定时，重新读取已保存状态核对结果。

确认记录包含时间、备份摘要与策略，并保留原恢复来源。已有发送记录、限额、卡身份校验、待核对结果、已取消预览与 `restore_held` 通知均按原状态保留。每次从备份恢复，都会建立新的恢复身份并重新进入保护流程，便于按次核对迁移。

原实例停止与历史完整性由操作者核对并声明；应用校验当前实例的备份、预览与保存状态。整个流程沿用管理入口的身份和来源检查。

## 本地与云端迁移

1. 在原实例生成最新一致备份，并保存对应私有配置、应用版本与摘要。
2. 私密传输，在目标实例 inspect，核对摘要、版本和数量后恢复到新目录。
3. 用目标网络地址、Bark、AES 和访问策略准备配置；先登录核对历史、卡记录及推送开关。
4. 完成最终新增记录核对，正常停止旧接入实例，再把板子切到目标实例。
5. 确认新状态与自然收件，由目标实例统一管理后续历史，并保留旧副本以便核对。

两端数据各自保存，迁移时核对备份后的新增记录；回退前保存目标实例最新备份，并确认版本兼容性。备份保留生成时的数据，包括当时存在的短信，恢复时可结合自己的数据保留计划检查。
