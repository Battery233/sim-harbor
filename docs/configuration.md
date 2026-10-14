# 配置说明

本页说明应用端配置；WiFi、板子上报事件和来电策略等原厂设置见[板子配置指南](hardware-setup.md)。

## 文件与优先级

| 位置 | 用途 |
| --- | --- |
| `configs/*.example.json` | 不含真实凭据的示例 |
| `configs/container.private.json` | Docker 或自定义实例的初始化配置 |
| `.env` | Compose 端口、初始化凭据及配置路径 |
| 数据目录 `system.private.json` | 首次启动生成的完整私有配置，后续优先读取 |
| `deploy/site.private.yaml` | 实例镜像与可选端口覆盖 |

应用会先校验初始化配置，再加载固定数据目录内的 `system.private.json`。因此初始化文件及必需环境变量仍需合法，但修改旧 `.env` 的凭据不会覆盖已保存的统一配置。

后续优先在“系统配置”保存业务参数和凭据，正常重启应用生效。凭据留空表示保留，清空 Bark 使用专门选项；界面只返回是否配置，不回显密钥。按卡通知开关单独即时保存。

网页不能切换数据目录、静态目录或运行环境。迁移数据库需按[恢复流程](data-maintenance.md)单独进行。私有文件保持受限权限，不上传 Issue。

## 核心字段

| 字段 | 含义 |
| --- | --- |
| `environment` | `local` / `cloud`，影响安全校验，不是不同产品 |
| `http_address` | 应用网页监听；容器中通常为 `0.0.0.0:8080` |
| `tcp_address` | 板子监听；容器中通常为 `0.0.0.0:9000` |
| `data_directory` | SQLite、统一配置与日志所在目录；容器全新部署 `/data` |
| `static_directory` | 前端产物目录；容器 `/app/web/dist`，源码 `web/dist` |
| `device_id` | 允许的设备编号，不是认证密钥 |
| `device_cidrs` | TCP 来源网段；单 IPv4 用 `/32`，IPv6 用 `/128` |
| `device_allowlist_enabled` | 是否启用来源规则；关闭会扩大连接范围 |
| `aes_mode` | 两端一致，Key 与 IV 各 16 字节，见[安全说明](../SECURITY.md) |
| `public_url` | 手机能访问的管理入口；用于通知详情链接 |
| `bark_base_url` | Bark 服务，例如 `https://api.day.app` |
| `device_timezone` | 板子时间解释与限额时区，默认 `Asia/Shanghai` |
| `stale_seconds` | 最近上报超过此时限视为不新鲜，示例 345 秒 |
| `incoming_text_escapes` | 可选的正文转义显示兼容；不修改原始正文/导出 |
| `status_alerts_enabled` | 持续异常与恢复提醒，需 Bark 可用 |
| `outbound_enabled` | 短信外发开关，默认关闭；恢复保护优先 |

初始化环境变量名为 `SIM_ADMIN_TOKEN`、`SIM_AES_KEY`、`SIM_AES_IV`、`SIM_BARK_KEY`，JSON 通过对应 `*_env` 字段引用。管理员 Token 至少 24 字符。

外发默认间隔 60 秒、每卡每日 5 次、最多 1 段；实际以配置为准。限额不能代替资费确认，详见[外发边界](features.md#受控外发)。

## 端口与来源

Docker 宿主端口由 `.env` 和 Compose 设置，容器监听由 JSON 设置。例如 `127.0.0.1:18880 → 8080` 与 `0.0.0.0:39090 → 9000` 可同时使用；后者是否可达还取决于防火墙和安全组。

设备来源规则依据原生 TCP 对端地址。Cloudflare 管理网页的来源地址、请求头与家庭公网 IP 不是同一条链路。来源规则无法代替协议身份认证。

## 日志保留

默认应用诊断最多 5000 条，磁盘日志保留 7 天，每文件最多 16 MiB、备份 3 份。达到保留容量时会限制新增磁盘记录，不承诺永久留存。Compose 标准输出另按 2 MiB × 3 轮转。保留策略可在允许范围内配置，备份 ZIP 不包含日志。
