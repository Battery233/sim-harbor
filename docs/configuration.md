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

应用会先校验初始化配置，再加载固定数据目录内的 `system.private.json`。初始化文件及必需环境变量继续用于启动校验；首次运行后的配置变更以统一配置为准。

后续优先在“系统配置”保存业务参数和凭据，再通过页面上的“确认重启应用”使其生效。凭据留空即可保留原值，清空 Bark 使用专门选项；界面以配置状态展示凭据，保护密钥隐私。按卡通知开关单独即时保存。

数据目录、静态目录和运行环境通过部署配置管理，数据库迁移按[恢复流程](data-maintenance.md)进行。首次选择新目录时可由初始化配置生成统一配置；移动已有目录时，初始化 JSON 与目录内 `system.private.json` 的 `data_directory` 必须指向同一路径。私有文件使用受限权限保存在自己的主机。

### 保存与生效

| 改动 | 生效方式 |
| --- | --- |
| 网页中的系统配置、Bark、外发开关等 | 保存后勾选重启确认，点击“确认重启应用” |
| 按卡通知开关 | 即时保存、生效 |
| `.env` 中的端口、镜像或容器环境变量 | 沿用原 Compose 项目与文件重新执行 `up -d` |
| 版本覆盖文件中的镜像 | 按[版本维护](releases.md)切换并核对运行版本 |

网页重启会重新加载应用配置，保留业务历史和已完成的迁移确认；设备连接随后重连。修改管理 Token 后，用新 Token 重新登录。

## 核心字段

| 字段 | 含义 |
| --- | --- |
| `environment` | `local` / `cloud`，为同一应用选择部署环境与对应校验 |
| `http_address` | 应用网页监听；容器中通常为 `0.0.0.0:8080` |
| `tcp_address` | 板子监听；容器中通常为 `0.0.0.0:9000` |
| `data_directory` | SQLite、统一配置与日志所在目录；容器全新部署 `/data` |
| `static_directory` | 前端产物目录；容器 `/app/web/dist`，源码 `web/dist` |
| `device_id` | 允许接入的板子编号，配合来源规则与加密参数使用 |
| `device_cidrs` | TCP 来源网段；单 IPv4 用 `/32`，IPv6 用 `/128` |
| `device_allowlist_enabled` | 启用后按 `device_cidrs` 筛选 TCP 连接来源 |
| `aes_mode` | 两端一致，Key 与 IV 各 16 字节，见[安全说明](../SECURITY.md) |
| `public_url` | 手机能访问的管理入口；用于通知详情链接 |
| `bark_base_url` | Bark 服务，例如 `https://api.day.app` |
| `device_timezone` | 板子时间解释与限额时区，默认 `Asia/Shanghai` |
| `stale_seconds` | 最近上报超过此时限视为不新鲜，示例 345 秒 |
| `incoming_text_escapes` | 可选的正文转义显示兼容；原文与导出保留原始内容 |
| `status_alerts_enabled` | 持续异常与恢复提醒，需 Bark 可用 |
| `outbound_enabled` | 短信外发开关，默认关闭；恢复保护优先 |
| `send_interval_seconds` | 每卡发送间隔，默认 60 秒，可配置 10～86400 秒 |
| `send_daily_limit` | 每卡每日尝试次数上限，默认 5 次，可配置 1～100 次 |
| `send_max_segments` | 单条短信的估算段数上限，默认 1 段，可配置 1～10 段 |

初始化环境变量名为 `SIM_ADMIN_TOKEN`、`SIM_AES_KEY`、`SIM_AES_IV`、`SIM_BARK_KEY`，JSON 通过对应 `*_env` 字段引用。管理员 Token 至少 24 字符。

外发限额按发送尝试记录计算，未知结果也计入，日期按 `device_timezone` 划分；实际值以当前配置为准。可根据套餐与使用习惯设置限额，详见[短信外发](features.md#短信外发)。

## 端口与来源

Docker 宿主端口由 `.env` 和 Compose 设置，容器监听由 JSON 设置。安装指南与 `deploy/.env.example` 推荐宿主 HTTP/TCP 为 `18880/39090`；容器内部仍为 `8080/9000`。基础 Compose 在未指定端口变量时回退到宿主 `8080/9000`，部署时沿用自己的 `.env` 即可。例如 `127.0.0.1:18880 → 8080` 与 `0.0.0.0:39090 → 9000` 可同时使用；后者是否可达还取决于防火墙和安全组。

设备来源规则依据原生 TCP 对端地址。设备规则与 Cloudflare 网页访问规则分别配置；可结合来源限制、传输模式和网络策略管理设备入口，见[安全说明](../SECURITY.md#设备通道)。

## 日志保留

默认应用诊断最多 5000 条，磁盘日志保留 7 天，每文件最多 16 MiB、备份 3 份。达到保留容量时会限制新增磁盘记录，以控制空间占用。Compose 标准输出另按 2 MiB × 3 轮转。保留策略可在允许范围内配置；需长期归档的日志单独保存，业务备份集中保存数据库。
