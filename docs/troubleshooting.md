# 故障排查

先记录发生时间、应用版本、部署方式和板子固件。从网页的当前状态、运行诊断与通知诊断入手，反馈时使用脱敏摘要。

| 现象 | 先检查 |
| --- | --- |
| 容器健康但板子离线 | 应用与数据库已启动，继续核对板子 WiFi、TCP 目标、两层来源规则和 AES |
| TCP 端口能连接但无状态 | 网络已可达，继续检查设备编号、模式、Key/IV 与有效上报 |
| Cloudflare 502 | 从连接器所在网络检查回源地址和端口；独立容器的回环不是宿主；核对 IPv4/IPv6 |
| 401 或登录失效 | 区分 Access 登录与应用 Token，使用当前实例保存的凭据，不公开 Token |
| Bark 显示未配置 | 核对当前数据目录及生效的统一配置；只改 `.env` 不会覆盖旧 `system.private.json` |
| Bark 已接受但未看到 | 在手机检查 Bark 通知权限、网络和显示设置；可用通知诊断中的 Bark 连接测试核对链路 |
| 收件正常但某卡不推送 | 检查固定卡身份的通知开关、消息完整性和恢复暂停状态 |
| 已打开推送但旧消息没补发 | 开启只对后续新业务生效，不回放关闭期间或恢复暂停的任务 |
| 配置保存后仍旧值 | 查看“待重启生效”标记，保存后通过网页确认重启应用；确认读取的是当前实例/数据目录 |
| 换 IP 后板子失联 | 检查家宽出口是否改变，更新实际 TCP 来源规则；网页 Access 地址与此无关 |
| SIM 状态异常或灯闪烁 | 按实际型号核对电源、天线、接触、注册和网络；结合应用状态与原厂页面核对硬件和注册情况 |
| 恢复后提示阻止外发 | 属于持久恢复保护，参见[数据维护](data-maintenance.md#恢复后的保护) |
| 私有 JSON 读取失败 | 检查文件存在、绑定路径、0600 和容器 UID/宿主映射；按容器实际用户设置最小所需读取权限 |

## Docker 只读检查

在自己的项目根目录，沿用安装时的项目名和文件。下面是基础安装示例；升级时增加了 `deploy/release.private.yaml` 的实例，终端检查也携带该文件，自定义项目名与覆盖文件按实际替换：

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml ps
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml logs --since 15m --tail 100 sim
```

按所需时间截取日志，在本机审阅并脱敏后分享错误摘要。`docker inspect`、展开后的 Compose 配置和 `.env` 可能包含凭据，作为私有运维资料保存。

应用日志位于业务数据目录的 `logs/`，容器正常部署为 `/data/logs/`；使用恢复子目录时随数据目录变化。网页诊断下载只包含有限的近期记录，保留容量和时间见[配置说明](configuration.md#日志保留)。

## 源码拉取与版本发布

| 现象 | 处理方式 |
| --- | --- |
| HTTPS 拉取/推送停在 Password 提示 | 使用自己的 GitHub Personal Access Token；终端输入凭据不回显字符。也可沿用已配置的 SSH 认证，见 [GitHub 认证说明](https://docs.github.com/en/get-started/git-basics/why-is-git-always-asking-for-my-credentials) |
| 本地能看到标签，GitHub 没有 | 核对标签指向，再单独推送该标签；分支与标签分别同步 |
| GitHub 没有 Release | 在 Releases 页面基于已有标签创建；操作顺序见[版本维护](releases.md#标签release-与镜像的关系) |
| Packages 中没有镜像或拉取失败 | 核对 Actions 的 Release image 结果、镜像标签、目标架构与包可见性；源码本机构建可独立使用 |
| 更新后出现空白历史库 | 核对 Compose 项目名、实际卷与 `data_directory`，保留原卷和备份，按原参数恢复接入 |
| 移动数据目录后启动失败 | 核对初始化 JSON 与新目录内 `system.private.json` 的 `data_directory` 一致，文件所有者和权限保持正确 |

## 按链路定位

以最近有效上报判断当前状态，并结合板子本地历史核对收件。应用收件箱保存已收到的固件上报，板子历史可单独查询。网络恢复后先查询原任务结果，再决定后续操作，保留清晰的发送与维护记录。

需要进一步协助时，提交脱敏的复现步骤和错误摘要，区分“网页可达”“TCP 可达”“有效状态”“收件入库”“Bark 接受”和“手机实际到达”。
