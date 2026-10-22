# 开发与模拟

使用 [go.mod](../go.mod) 声明的 Go 版本和兼容的 Node.js/npm；当前 Docker 构建基线为 Go 1.26.9、Node.js 26.7.0，完整版本见 [Dockerfile](../Dockerfile)。`GOTOOLCHAIN=local` 要求本机已有符合版本要求的 Go。测试和构建命令见 [CONTRIBUTING.md](../CONTRIBUTING.md)。

## 合成数据体验

用内置模拟器即可体验收件与卡片界面。示例使用本机合成设备和独立数据目录，先在项目根目录构建网页并启动服务：

```sh
npm --prefix web ci
npm --prefix web run build
env -u SIM_BARK_KEY -u SIM_ADMIN_TOKEN -u SIM_AES_KEY -u SIM_AES_IV GOTOOLCHAIN=local go run ./cmd/sim -config configs/local.example.json
```

打开 `http://127.0.0.1:8080`。保持服务运行，在另一个终端进入同一目录：

```sh
env -u SIM_BARK_KEY -u SIM_ADMIN_TOKEN -u SIM_AES_KEY -u SIM_AES_IV GOTOOLCHAIN=local go run ./cmd/simulate -config configs/local.example.json
```

模拟器连接本机测试设备 `SIM-TEST`，通过合成事件演示业务流程。数据写入被忽略的 `data/local`，与真实实例分别保存；退出使用 Ctrl+C。端口冲突时，可复制配置到私有文件并协调两端地址。

合成体验默认在回环地址免登录查看收件与状态。需要体验“系统配置”等受管理凭据保护的入口时，复制到私有配置，为服务和模拟器设置一致参数，并为服务准备至少 24 字符的管理员 Token。

## 配置模板

- `local.example.json`：回环合成体验，适合本机明文测试设备。
- `lan.example.json`：原生局域网起点，推荐端口 18880/39090 与 mode1，按自己的设备来源调整。
- `cloud.example.json`：原生云端起点，推荐端口 18880/39090 与 mode1，配置 HTTPS 与设备来源规则。
- `container.example.json`：容器内使用 8080/9000 与 `/data`；本地与云端共用，初始化时选择环境。

真实设备模板采用 mode1，合成回环体验使用 mode0；各模板的短信外发与持续异常提醒初始关闭，接入后按需启用。

示例使用占位或合成数据，部署时填入自己的设备标识、密钥和网络地址。

## 验证方法

自动化测试使用合成数据，覆盖协议、持久化、通知、受控操作和网页流程；这些检查不能替代实际固件、蜂窝网络与浏览器的兼容性验证。部署检查按[首次接入顺序](../使用指南.md#首次接入顺序)进行。涉及测试电话或短信时，先确认目标与资费，并使用专用测试实例。
