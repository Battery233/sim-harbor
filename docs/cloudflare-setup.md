# Cloudflare Tunnel 与 Access

为管理网页配上 HTTPS 域名与身份登录，在本地或云端部署后都能使用。设备通过独立 TCP 入口接入，两条链路分别配置。

## 1. 域名和连接器

准备已托管到 Cloudflare 的域名，并在应用主机运行 `cloudflared` 连接器。可以新建 Tunnel，也可以在同一主机的健康 Tunnel 中新增本应用路由，沿用连接器即可。[官方路由说明](https://developers.cloudflare.com/tunnel/concepts/routing/)。

在 Cloudflare 控制台进入网络连接器的 Tunnels 页面，创建 Cloudflared Tunnel。为运行主机选择系统与架构，在该主机执行控制台提供的安装命令；命令包含连接凭据，请在自己的终端私密执行。连接器显示 Healthy 后再新增网页路由。已有健康连接器可跳过创建步骤。

## 2. 发布管理网页

在 Tunnel 的 Published application routes 中新增：

| 字段 | 示例 |
| --- | --- |
| Subdomain | `sim` |
| Domain | 自己的域名 |
| Path | 留空，覆盖整个站点及 API |
| Service Type | `HTTP` |
| URL | `127.0.0.1:18880`，仅当连接器与宿主共用网络时 |

独立容器中的 `127.0.0.1` 指向连接器自身。此时需把应用和连接器放在可互通的网络，并选择连接器可达的回源地址。

建议明确使用 IPv4 地址，避免 `localhost` 被解析成应用未监听的 IPv6 地址。遇到 502 时，结合连接器日志检查回源地址和端口。

## 3. 加入 Access 保护

创建 Self-hosted Access 应用，域名覆盖全站和 API，Allow 策略仅允许自己的身份。为该 Tunnel 路由启用 Protect with Access，并核对 Team 与 Application AUD；使用本地管理的配置可参考 [cloudflared 示例](../deploy/cloudflared.example.yml)，同时按所选宿主端口调整回源。

Access 与应用管理员 Token 共同保护入口。保持宿主 HTTP 回环监听，并验证登录身份、API 和回源规则，确保访问统一经过保护。[回源校验参数](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/origin-parameters/#access)。

## 4. 验证

- 使用无痕窗口核对网页和 API 的认证流程。
- 允许的身份可登录，其他身份被拒绝；核对 Access 到期后的重新登录。
- 应用内 `public_url` 使用正式 HTTPS 管理域名。
- Bark 连接测试及新业务的详情链接可在手机 WiFi、蜂窝网络打开。
- JSON/CSV/ZIP 下载能正常落盘，备份通过维护工具的完整性检查。

临时连通性测试可用 Quick Tunnel，长期使用则选择具名 Tunnel 和固定管理域名。连接器需能访问 Cloudflare；HTTP2 使用 TCP 7844，QUIC 使用 UDP 7844，按实际协议和网络核对。[连接要求](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/)。

## TCP 和 CIDR 的区别

板子接入使用云端部署指南中的独立原生 TCP 入口。Cloudflare 的普通 TCP 应用访问采用客户端 `cloudflared` 等转发方式，使用时需有相应客户端支持。[官方 TCP 说明](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/non-http/cloudflared-authentication/arbitrary-tcp/)。

Tunnel 中的 Private CIDR 为 Cloudflare 客户端提供指定 IP/网段的私网路由；应用的设备来源白名单则负责筛选接入连接，两者各自配置。维护 Private CIDR 时，结合使用该路由的客户端和网络策略核对。[CIDR 路由说明](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/private-net/cloudflared/connect-cidr/)。
