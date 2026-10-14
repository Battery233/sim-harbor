# 架构

SIMHarbor 是单实例 Go 服务，提供设备 TCP、管理 HTTP API、Vue 静态网页与后台任务。SQLite 保存业务记录和任务状态，不需要外部数据库或消息队列。

```mermaid
flowchart TB
    Board[双卡智能板] -->|TCP 上报 / 命令响应| TCP[协议会话]
    TCP --> Store[(SQLite / WAL)]
    Web[Vue 管理网页] --> API[HTTP API / 管理员认证]
    API --> Store
    API -->|状态查询与确认操作| TCP
    Store --> Worker[通知与状态观察任务]
    Worker --> Bark[Bark 服务]
```

## 运行模型

- 一块板、两个当前卡槽；固定卡身份可保留换槽与历史卡记录。
- 协议解析、持久化、通知和网页在本地/云端共用，通过监听地址、来源与路径配置部署环境。
- 收件持久化与通知任务分离，通知失败不丢弃已经入库的短信。
- 外发先持久记录认领再写入设备连接，未知结果不自动重发；完整操作历史是防重复的重要前提。
- 统一配置保存在固定数据目录；进程锁避免同目录被应用和停服维护同时占用。

## 部署与数据管理

本地可直接运行或使用 Docker；云端可在网页前增加 HTTPS、Tunnel 与 Access。HTTP 管理入口与设备 TCP 分别设置访问策略。

数据库备份、运行配置和日志分别保存，方便独立维护。恢复写入新目录，保留原实例以便核对，并通过外发保护与迁移确认接续发送管理；实例间的数据转移使用一致备份流程。

## 目录

| 路径 | 内容 |
| --- | --- |
| `cmd/sim` | 应用入口 |
| `cmd/simulate` | 本机合成设备模拟器 |
| `cmd/check` | 容器健康检查 |
| `cmd/maintain` | 备份、检查、恢复工具 |
| `internal/protocol` | 帧及字段解析 |
| `internal/server` | 会话、API、通知与控制 |
| `internal/store` | 数据模型、事务与迁移 |
| `internal/config`、`internal/diagnostics` | 配置与脱敏诊断 |
| `web` | Vue 网页、请求流程和测试 |
| `configs`、`deploy` | 合成配置、部署模板 |

协议实现见[协议说明](protocol-notes.md)，配置优先级见[配置说明](configuration.md)，检查方法见[开发指南](../CONTRIBUTING.md)。
