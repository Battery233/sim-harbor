# Cloudflare Tunnel 与 Access

此方案为管理网页提供 HTTPS 与身份限制，可用于云端或本地部署。设备原生 TCP 是独立入口，不属于本页的 HTTP 路由。

## 1. 域名和连接器

准备已托管到 Cloudflare 的域名，并在应用主机运行 `cloudflared` 连接器。可以新建 Tunnel，也可以复用同一主机上已有的健康 Tunnel；复用时只新增本应用路由，不改其他应用的 Token 或配置。[官方路由说明](https://developers.cloudflare.com/tunnel/concepts/routing/)。

在 Cloudflare 控制台进入网络连接器的 Tunnels 页面，创建 Cloudflared Tunnel。为运行主机选择系统与架构，在该主机执行控制台提供的安装命令；命令包含连接凭据，仅在自己的终端使用，不放入仓库。连接器显示 Healthy 后再新增网页路由。已有健康连接器可跳过创建步骤。

## 2. 发布管理网页

在 Tunnel 的 Published application routes 中新增：

| 字段 | 示例 |
| --- | --- |
| Subdomain | `sim` |
| Domain | 自己的域名 |
| Path | 留空，覆盖整个站点及 API |
| Service Type | `HTTP` |
| URL | `127.0.0.1:18880`，仅当连接器与宿主共用网络时 |

独立容器中的 `127.0.0.1` 指向连接器自身。此时需把应用和连接器放在可互通的网络，或选择实际可达的回源地址，不直接套用宿主回环地址。

建议明确使用 IPv4 地址，避免 `localhost` 被解析成应用未监听的 IPv6 地址。502 常表示回源失败，不能仅凭状态码确定原因。

## 3. 加入 Access 保护

创建 Self-hosted Access 应用，域名覆盖全站和 API，Allow 策略仅允许自己的身份。为该 Tunnel 路由启用 Protect with Access，并核对 Team 与 Application AUD；使用本地管理的配置可参考 [cloudflared 示例](../deploy/cloudflared.example.yml)，同时按所选宿主端口调整回源。

Access 验证与应用管理员 Token 分别保留。只有域名经过登录，不足以证明源站不能被绕过；保持宿主 HTTP 仅回环，并验证错误身份、未登录 API 和其他入口。[回源校验参数](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/origin-parameters/#access)。

## 4. 验证

- 无痕窗口未登录时，网页和 API 都要求认证；不允许直接获取业务数据。
- 允许的身份可登录，其他身份被拒绝；核对 Access 到期后的重新登录。
- 应用内 `public_url` 使用正式 HTTPS 管理域名。
- Bark 连接测试及新业务的详情链接可在手机 WiFi、蜂窝网络打开。
- JSON/CSV/ZIP 下载能正常落盘，备份通过维护工具的完整性检查。

Quick Tunnel 适合临时连通性测试，不能作为正式稳定入口。连接器需能访问 Cloudflare；HTTP2 使用 TCP 7844，QUIC 使用 UDP 7844，按实际协议和网络核对。[连接要求](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/)。

## TCP 和 CIDR 的区别

普通 Tunnel TCP 访问需要客户端 `cloudflared` 等配合，不能因发布了一个 TCP 主机名就认为板子能原生使用。[官方 TCP 说明](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/non-http/cloudflared-authentication/arbitrary-tcp/)。

Tunnel 中的 Private CIDR 是经 Cloudflare 客户端与路由策略使用的 IP 路由，不是应用设备来源白名单，也不自动改变服务器所有出站连接。新增或删除前应确认使用它的客户端和网络策略。[CIDR 路由说明](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/private-net/cloudflared/connect-cidr/)。
