# 本地部署

应用运行在自己的电脑、NAS 或局域网 Linux 主机，板子通过家庭 WiFi 主动连接它。无需云服务器，也无需为板子开放公网端口。

## Docker 方式

按[Docker 安装](deployment-docker.md)获取源码并生成私有文件，在首次启动前完成以下配置。需要持续运行的主机；电脑休眠或退出 Docker 时服务也会中断。

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

应用主机的防火墙允许板子访问所选 TCP 端口。Docker Desktop/NAS 的转发可能改变应用看到的来源，应结合连接诊断核对，不能仅凭板子的 WiFi 地址猜测。

板子目标填 `tcp://192.168.1.10:39090`，使用家庭 WiFi；AES 模式、Key/IV 与应用一致。事件上报、JSON 转义和其他建议设置见[板子配置指南](hardware-setup.md)。不要直接导入其他人的整套板子配置。

### 网页访问

Compose 默认只在运行主机的 `127.0.0.1:18880` 提供网页。需要局域网内其他设备访问时，在生成的 `deploy/site.private.yaml` 的同一个 `sim` 服务下加入端口覆盖，保留已有 `image` 字段：

```yaml
services:
  sim:
    image: sim:release
    ports: !override
      - "192.168.1.10:18880:8080"
      - "${SIM_DEVICE_BIND:-127.0.0.1}:${SIM_DEVICE_PORT:-39090}:9000"
```

`!override` 需要 Docker Compose 2.24.4 或更新版本；它用于替换整个端口列表，避免旧的回环映射与新映射重复绑定。此时将 `public_url` 设为 `http://192.168.1.10:18880`，核对主机防火墙只允许可信局域网，并保留管理员 Token。

HTTP 不加密，适合受信局域网；需要更强的访问隔离时使用 HTTPS 反向代理或受保护的远程入口。局域网详情链接在手机蜂窝网络下通常不可达；Bark 提醒到达不代表详情地址可访问。

按 Docker 文档启动，再按[使用指南](../使用指南.md#首次接入顺序)核对状态和通知。

## 源码方式：macOS / Linux

准备与 [go.mod](../go.mod) 一致的 Go 和兼容的 Node.js/npm。先按 Docker 安装页生成私有文件，只生成文件，不构建或启动容器。仍在项目根目录，将生成的 JSON 调整为：

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

正常停止使用 Ctrl+C。需要长期后台运行时，自行配置系统服务并保持项目工作目录、环境变量和数据目录一致；不要同时启动两个实例共用一个库。源码模式没有 Docker 端口映射，实际入口直接由 JSON 的监听地址决定。

## 本地与云端迁移

两者没有自动同步。使用[备份与恢复](data-maintenance.md)迁移历史，另行转移私有配置，核对最终新增记录后再切换板子。不要把本地旧库直接当作云端回退副本。
