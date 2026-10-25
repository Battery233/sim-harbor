# Docker 安装

本地与云端共用本页的初始化、构建和运行方法。先阅读所选的[本地部署](deployment-local.md)或[云端部署](deployment-cloud.md)，在首次启动前完成对应配置。

以下使用 Compose、Python 3 和本机构建镜像，方便从源码开始部署。Linux 需要 Docker Engine 与 Compose 插件；macOS 可使用 Docker Desktop。安装方法见 [Docker 官方文档](https://docs.docker.com/get-started/get-docker/)，安装后先用 `docker version` 和 `docker compose version` 确认可用。安装目录可按自己的习惯选择。使用局域网端口覆盖示例时，Compose 需为 2.24.4 或更新版本。

本文使用 `1.1.7` 镜像，支持本机构建或从 GHCR 拉取；升级与兼容性见[版本维护](releases.md#从-v116-升级到-v117)。

## 1. 获取源码

```sh
git clone https://github.com/Battery233/sim-harbor.git
cd sim-harbor
```

HTTPS 拉取使用 GitHub 支持的访问凭据；也可在已配置 SSH 的主机使用 `git@github.com:Battery233/sim-harbor.git`。认证排障见[故障排查](troubleshooting.md#源码拉取与版本发布)。

已有实例可保留当前配置，按[版本维护](releases.md)完成备份与更新。

## 2. 生成自己的私有配置

在项目根目录执行。生成 `.env`、`configs/container.private.json` 和 `deploy/site.private.yaml`；脚本会保护已有文件，凭据保存到权限 0600 的私有文件中。

```sh
python3 - <<'PY'
import json, os, secrets
from pathlib import Path
paths = [Path('.env'), Path('configs/container.private.json'), Path('deploy/site.private.yaml')]
if any(p.exists() or p.is_symlink() for p in paths):
    raise SystemExit('私有文件已存在，请先核对，不能重复初始化。')
values = {
    'SIM_ADMIN_TOKEN': secrets.token_urlsafe(32),
    'SIM_AES_KEY': secrets.token_urlsafe(12),
    'SIM_AES_IV': secrets.token_urlsafe(12),
    'SIM_BARK_KEY': '',
    'SIM_IMAGE': 'sim-harbor:1.1.7',
    'SIM_HTTP_PORT': '18880',
    'SIM_DEVICE_BIND': '127.0.0.1',
    'SIM_DEVICE_PORT': '39090',
    'SIM_CONFIG': './configs/container.private.json',
}
config = json.loads(Path('configs/container.example.json').read_text())
config.update(environment='local', public_url='http://127.0.0.1:18880', status_alerts_enabled=False)
contents = [
    ''.join(k + "='" + v + "'\n" for k, v in values.items()),
    json.dumps(config, ensure_ascii=False, indent=2) + '\n',
    'services:\n  sim: {}\n',
]
for path, content in zip(paths, contents):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as f:
        f.write(content)
print('三个私有文件已创建，凭据未输出。')
PY
```

`deploy/.env.example` 可用于查看环境变量名称；上面的脚本会自动生成带随机凭据的文件。管理员 Token 为随机字符串；AES Key 和 IV 各 16 个 ASCII 字符。板子已有密钥时，可沿用并协调应用端配置。完成基础接入后，按需配置 Bark、短信外发和持续异常提醒。

配置示例默认关闭留言，已有实例不必补填新增字段。当前版本支持局域网 HTTP 直连和经受控代理的公网 HTTPS 录音上传，需主动配置，见[留言说明](voicemail.md)。v1.1.7 沿用 schema 21，从旧版升级前先核对[数据库兼容性](releases.md#从-v116-升级到-v117)，不要对已有部署重复执行初始化脚本。

## 3. 编辑与权限

用本机编辑器修改 `.env` 与 JSON，占位设备编号和来源必须替换；设备 TCP 绑定也需按部署方式设为可达的局域网接口或云端接口。具体值见[本地](deployment-local.md#配置网络)或[云端](deployment-cloud.md#配置应用与入口)说明。

宿主机端口与容器端口不同：本例宿主 HTTP 为 18880、设备 TCP 为 39090；容器内部保持 8080/9000。容器内的 `0.0.0.0` 是正常监听方式；是否发布到外部由宿主端口映射和防火墙决定。

Linux 下，容器以 UID/GID 65532 运行，需要让它读取绑定的私有 JSON：

```sh
chmod 600 .env configs/container.private.json deploy/site.private.yaml
sudo chown 65532:65532 configs/container.private.json
```

`.env` 仍归执行 Compose 的用户所有。使用 rootless Docker、用户命名空间或 NAS 权限映射时，按实际用户映射调整文件所有者。macOS 绑定挂载权限受 Docker Desktop 共享机制影响；遇到读取失败见[排障](troubleshooting.md)。

## 4. 构建与启动

以下为全新安装示例，逐条执行，失败后停止并保留现场。所有操作使用同一项目名和同一组文件；已有实例若使用版本覆盖文件，所有命令都需携带它，见[已有实例升级](releases.md#已有实例升级)。

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml config --quiet
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml build
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml up -d --no-build --pull never --wait --wait-timeout 60
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml ps
```

`config --quiet` 避免把展开后的密钥打印出来。`build` 按当前 Docker 平台构建；需要跨架构时显式指定目标，并在目标主机验证。

启动后应看到应用健康、网页监听 `127.0.0.1:18880`，命名卷 `sim_sim-data` 挂载到容器 `/data`。新卷由镜像准备私有目录权限，已有卷保留原数据。登录首页查询板子状态，继续完成设备接入验收。

这里的宿主端口来自生成的 `.env`。如已有其他端口配置，按自己的值核对。

在运行主机打开 `http://127.0.0.1:18880`，使用 `.env` 中的管理员 Token 登录。远程服务器可先通过 SSH 转发：

```sh
ssh -N -L 18880:127.0.0.1:18880 your-user@your-server
```

## 5. 重启、停服与升级

网页保存配置后，可直接勾选“确认按已保存的配置重启应用”，点击“确认重启应用”。页面恢复后核对配置与板子连接即可。

终端也可重启应用：

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml restart sim
```

停服并保留数据：

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml stop
```

网页重启适用于应用内配置；修改 `.env` 的端口映射、镜像或容器环境变量后，重新执行上面的 `up` 以应用新的容器配置。业务配置首次运行后由数据目录中的统一配置管理，见[配置说明](configuration.md)。

升级前保存数据库备份、私有配置和旧镜像版本，再构建新镜像并执行 `up`；需要回退时核对镜像与备份的兼容性，步骤见[版本维护](releases.md)。

`unless-stopped` 在进程退出后按策略恢复服务。容器仍在运行但健康检查异常时，可结合日志排查。日常维护保留数据卷，另用应用备份保存一致快照；`down -v` 会删除卷，应与普通停服操作区分。

## 发布与升级

版本镜像、既有云端实例的更新、核对与回退步骤见[版本维护](releases.md)。更新时沿用原 Compose 项目名、数据卷和私有配置。
