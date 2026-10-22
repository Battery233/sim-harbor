# 本地部署

把自己的电脑、NAS 或局域网 Linux 主机变成双卡管理中心。板子通过家庭 WiFi 主动连接应用，短信、来电和卡记录保存在自己的本地存储中，整个设备链路可在局域网内完成。

## Docker 方式

按[Docker 安装](deployment-docker.md)获取源码并生成私有文件，在首次启动前完成以下配置。选择持续在线的主机，并保持 Docker 运行，即可持续接收板子上报。

### 配置网络

示例地址 `192.168.1.10` 表示应用主机，`192.168.1.50` 表示板子，均需换成自己的局域网地址，建议在路由器预留地址。

| 位置 | 设置 |
| --- | --- |
| JSON `environment` | `local` |
| JSON `device_id` | 自己的设备编号 |
| JSON `device_cidrs` | 如 `["192.168.1.50/32"]`，以应用实际看到的 TCP 来源为准 |
| JSON `device_allowlist_enabled` | `true` |
| JSON `aes_mode` | 与板子一致；初始化示例为 mode1 |
| JSON `public_url` | 仅本机使用可设 `http://127.0.0.1:18880`；手机访问需要可达的管理地址 |
| JSON `data_directory` | `/data` |
| `.env` 的 `SIM_DEVICE_BIND` | 如 `192.168.1.10`，仅绑定需要的局域网接口 |
| `.env` 的 `SIM_DEVICE_PORT` | `39090`，或自选空闲端口 |

应用主机的防火墙允许板子访问所选 TCP 端口。Docker Desktop/NAS 的转发可能改变应用看到的来源，可在连接诊断中确认应用实际看到的 TCP 来源，并据此设置规则。

板子目标填 `tcp://192.168.1.10:39090`，使用家庭 WiFi；AES 模式、Key/IV 与应用一致。事件上报、JSON 转义和其他建议设置见[板子配置指南](hardware-setup.md)。使用自己的设备编号、密钥和网络地址完成配置。

### 网页访问

Compose 默认在运行主机的 `127.0.0.1:18880` 提供网页。要让局域网内的手机或电脑访问，在生成的 `deploy/site.private.yaml` 的 `sim` 服务下加入端口覆盖；已有自定义镜像字段时一并保留：

```yaml
services:
  sim:
    image: ${SIM_IMAGE:-sim-harbor:1.1.1}
    ports: !override
      - "192.168.1.10:18880:8080"
      - "${SIM_DEVICE_BIND:-127.0.0.1}:${SIM_DEVICE_PORT:-39090}:9000"
```

`!override` 需要 Docker Compose 2.24.4 或更新版本；它用于替换整个端口列表，避免旧的回环映射与新映射重复绑定。此时将 `public_url` 设为 `http://192.168.1.10:18880`，核对主机防火墙只允许可信局域网，并保留管理员 Token。

HTTP 适合受信局域网内访问；需要加密或外出访问时，可配置 HTTPS 反向代理或受保护的远程入口，并把 `public_url` 设置为手机可达的地址，让 Bark 通知直达详情。

按 Docker 文档启动，再按[使用指南](../使用指南.md#首次接入顺序)核对状态和通知。

## 源码方式：macOS / Linux

准备满足 [go.mod](../go.mod) 要求的 Go 工具链和兼容的 Node.js/npm；当前 Docker 构建基线为 Go 1.26.9、Node.js 26.7.0。文中的 `GOTOOLCHAIN=local` 使用本机已安装工具链，先用 `go version` 确认可用。

按 Docker 安装页生成私有文件后，直接将 JSON 调整为下表值；接下来使用本节的源码构建与运行命令：

| 字段 | 原生运行值 |
| --- | --- |
| `environment` | `local` |
| `http_address` | 本机网页 `127.0.0.1:18880`；局域网网页可用主机 LAN IP:18880 |
| `tcp_address` | 主机 LAN IP:39090，例如 `192.168.1.10:39090` |
| `static_directory` | `web/dist` |
| `data_directory` | `data/instance`，与合成体验库分开 |
| `device_id`、AES、来源和 `public_url` | 与上方网络规则一致 |

源码方式不执行容器 UID 的 `chown`，JSON 与 `.env` 保持自己所有、0600。`.env` 是自己创建的简单赋值文件，在当前终端导入后运行：

```sh
npm --prefix web ci
npm --prefix web run build
set -a
. ./.env
set +a
GOTOOLCHAIN=local go run ./cmd/sim -config configs/container.private.json
```

网页“系统配置”可保存参数并确认重启；终端前台运行仍可用 Ctrl+C 停止。长期后台运行可配置系统服务，固定工作目录、环境变量和专用数据目录，由一个实例管理该库。源码运行的网络入口直接使用 JSON 中的监听地址。

## 本地与云端迁移

可通过[一致备份与恢复](data-maintenance.md)携带历史迁移到云端，或从云端迁回本地。转移私有配置、核对最终新增记录后切换板子，以目标实例接续管理。两端数据分别保存，迁移时按备份流程核对。
