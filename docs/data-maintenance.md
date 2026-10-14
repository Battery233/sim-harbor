# 数据导出、备份与迁移

## 短信导出

在“数据维护”选择卡身份、号码和日期范围，下载 JSON 或 CSV。JSON 保留原文，CSV 对公式样式文本加保护前缀；导出不标已读、不建立推送或外发任务。

单次最多 10,000 条 / 32 MiB，超过时需缩小范围，不返回截断文件。JSON 使用 `format_version=2`，包含筛选条件与记录；导出包含私人内容，请妥善保存。

## 一致备份

在网页勾选私人数据保存确认，生成并下载备份 ZIP。应用使用 SQLite Online Backup API 取得一致快照，包含业务库及 manifest，不需要停服。不要复制正在运行的单个 `sim.db`，因为 WAL 中可能还有数据。

数据库上限 256 MiB、ZIP 上限 272 MiB。备份包含短信、来电、卡身份/偏好、发件历史与通知任务；不含 `system.private.json`、环境变量、日志。配置和凭据须另行私密备份。ZIP 未加密，SHA-256/CRC 只检查完整性。

## 检查与恢复：源码工具

在项目根目录使用与备份兼容的程序版本。先检查：

```sh
GOTOOLCHAIN=local go run ./cmd/maintain inspect -backup /private/path/sim-backup.zip
```

工具返回版本、数量及 `archive_sha256`，不打印短信正文。核对后恢复到**不存在的新目录**，将下面占位摘要替换为 inspect 返回值：

```sh
GOTOOLCHAIN=local go run ./cmd/maintain restore   -backup /private/path/sim-backup.zip   -to data/restored-instance   -confirm-archive REPLACE_WITH_ARCHIVE_SHA256
```

不覆盖原目录、不自动切换运行库。先保留原库与配置，再准备指向新目录的初始化配置；原生运行示例目标为 `data/restored-instance`，容器运行则使用卷内路径。当前程序支持 schema v4–v14 备份并迁移恢复副本，旧程序不一定能打开新库。

没有网页时，可停止占用该目录的应用，再执行停服备份。输出文件必须不存在：

```sh
GOTOOLCHAIN=local go run ./cmd/maintain backup   -data-directory data/instance   -output /private/path/sim-backup.zip
```

应用与维护工具共用进程锁。不要删除 `.sim.lock` 或恢复未完成标记来绕过检查。

## Docker 维护工具

镜像内的工具为 `/app/sim-data`。以下假设采用[Docker 文档](deployment-docker.md)中的镜像和项目名，宿主私有备份目录 `/srv/sim-backups` 需自行创建、放入所选 ZIP，并让容器 UID 65532 可读。工具以非 root 运行，不能仅凭宿主 root 可读推断容器可读。

先检查包，无需挂载业务卷；临时内存目录供校验解包使用，容器退出后释放：

```sh
docker run --rm --network none --read-only   --mount type=bind,src=/srv/sim-backups,dst=/backup,readonly   --entrypoint /app/sim-data sim:release   inspect -backup /backup/sim-backup.zip
```

恢复到卷内新子目录，先确认该卷属于自己的应用、目标不存在。恢复不需要启动应用：

```sh
docker run --rm --network none --read-only   --mount type=bind,src=/srv/sim-backups,dst=/backup,readonly   --mount type=volume,src=sim_sim-data,dst=/data   --entrypoint /app/sim-data sim:release   restore -backup /backup/sim-backup.zip   -to /data/restored-instance   -confirm-archive REPLACE_WITH_ARCHIVE_SHA256
```

`/data/restored-instance` 是容器内路径，在 Docker 卷中，不是宿主同名目录。确认恢复完成后，在专用初始化配置中将 `data_directory` 指向此路径，单独设置对应私有凭据再启动。备份包不会替你创建运行配置。

## 恢复后的保护

恢复会取消旧发送预览，对未确定的外发保留未知状态，持久阻止新增外发。旧待通知任务转为 `restore_held`，不会自动补推；新到收件按正常配置处理。卡备注、固定身份、按卡通知偏好和历史仍保留。

恢复保护用于防止旧备份缺失收费历史后重发，不是配置错误。不能仅把 `outbound_enabled` 改为 true 解除，不应手工删保护记录。

## 本地与云端迁移

1. 在原实例生成最新一致备份，并保存对应私有配置、应用版本与摘要。
2. 私密传输，在目标实例 inspect，核对摘要、版本和数量后恢复到新目录。
3. 用目标网络地址、Bark、AES 和访问策略准备配置；先登录核对历史、卡记录及推送开关。
4. 完成最终新增记录核对，正常停止旧接入实例，再把板子切到目标实例。
5. 确认新状态与自然收件；保留旧副本以便核对，不同时维护两份独立发送历史。

两端没有自动同步，也不自动合并备份后的新增记录。回退须先保存新实例的最新备份并核对 schema 兼容性。旧备份可能包含已删除短信，恢复旧包可能带回内容；删除当前记录不会清理外部副本。
