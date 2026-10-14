# Docker 安装

本地与云端共用本页的初始化、构建和运行方法。先阅读所选的[本地部署](deployment-local.md)或[云端部署](deployment-cloud.md)，在首次启动前完成对应配置。

以下使用 Compose、Python 3 和本机构建镜像，不依赖项目预发布镜像。Linux 需要 Docker Engine 与 Compose 插件；macOS 可使用 Docker Desktop。安装方法见 [Docker 官方文档](https://docs.docker.com/get-started/get-docker/)，安装后先用 `docker version` 和 `docker compose version` 确认可用。示例不要求固定安装目录。

## 1. 获取源码

```sh
git clone https://github.com/Battery233/sim.git
cd sim
```

已有实例不要重新初始化；升级前先[备份](data-maintenance.md)。

## 2. 生成自己的私有配置

在项目根目录执行。生成 `.env`、`configs/container.private.json` 和 `deploy/site.private.yaml`；任意目标已存在即拒绝覆盖。凭据只写入权限 0600 的文件，不输出到终端。

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
    'services:\n  sim:\n    image: sim:release\n',
]
for path, content in zip(paths, contents):
    with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as f:
        f.write(content)
print('三个私有文件已创建，凭据未输出。')
PY
```

管理员 Token 为随机字符串；AES Key 和 IV 各 16 个 ASCII 字符。板子已有密钥时，应私密地协调两端配置，不能只替换其中一端。Bark 初始为空，短信外发和持续异常提醒均关闭。

## 3. 编辑与权限

用本机编辑器修改 `.env` 与 JSON，占位设备编号和来源必须替换。具体值见[本地](deployment-local.md#配置网络)或[云端](deployment-cloud.md#配置应用与入口)说明。

宿主机端口与容器端口不同：本例宿主 HTTP 为 18880、设备 TCP 为 39090；容器内部保持 8080/9000。容器内的 `0.0.0.0` 是正常监听方式；是否发布到外部由宿主端口映射和防火墙决定。

Linux 下，容器以 UID/GID 65532 运行，需要让它读取绑定的私有 JSON：

```sh
chmod 600 .env configs/container.private.json deploy/site.private.yaml
sudo chown 65532:65532 configs/container.private.json
```

`.env` 仍归执行 Compose 的用户所有。使用 rootless Docker、用户命名空间或 NAS 权限映射时，按实际映射调整，不能假定宿主 UID 始终是 65532。macOS 绑定挂载权限受 Docker Desktop 共享机制影响；遇到读取失败见[排障](troubleshooting.md)。

## 4. 构建与启动

所有操作使用同一项目名和同一组文件，避免意外建立另一份数据卷：

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml config --quiet
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml build
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml up -d --no-build --pull never --wait --wait-timeout 60
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml ps
```

`config --quiet` 避免把展开后的密钥打印出来。`build` 按当前 Docker 平台构建；需要跨架构时显式指定目标，并在目标主机验证。

预期应用健康、网页监听 `127.0.0.1:18880`，命名卷 `sim_sim-data` 挂载到容器 `/data`。健康检查只说明应用和数据库可用，不代表板子在线。新卷由镜像提供私有目录权限，不要手动清空已有卷。

在运行主机打开 `http://127.0.0.1:18880`，使用 `.env` 中的管理员 Token 登录。远程服务器可先通过 SSH 转发：

```sh
ssh -N -L 18880:127.0.0.1:18880 your-user@your-server
```

## 5. 重启、停服与升级

重启应用：

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml restart sim
```

停服并保留数据：

```sh
docker compose --env-file .env -p sim -f compose.yaml -f deploy/site.private.yaml stop
```

修改 `.env` 的端口映射或环境变量后，应重新执行上面的 `up`，单纯 `restart` 不更新容器环境。业务配置首次运行后由数据目录中的统一配置管理，见[配置说明](configuration.md)。

升级前保存数据库备份、私有配置和旧镜像版本，再构建新镜像并执行 `up`。数据库升级可能不兼容旧程序，不能仅换回旧镜像就直接打开新库。

`unless-stopped` 会在进程退出后按策略恢复，但 `unhealthy` 本身不会自动重启容器。不要使用 `down -v`、卷清理或手删 Docker 存储目录排障；命名卷本身也不是备份。
