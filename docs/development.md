# 开发与模拟

使用 [go.mod](../go.mod) 声明的 Go 版本和兼容的 Node.js/npm；Docker 构建版本见 [Dockerfile](../Dockerfile)。测试和构建命令见 [CONTRIBUTING.md](../CONTRIBUTING.md)。

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

## 配置模板

- `local.example.json`：回环合成体验，适合本机明文测试设备。
- `lan.example.json`：原生局域网起点，来源和 AES 模式必须与自己的环境核对。
- `cloud.example.json`：原生云端起点，云端安全校验。
- `container.example.json`：容器内路径和监听地址；本地与云端共用，初始化时选择环境。

示例使用占位或合成数据，部署时填入自己的设备标识、密钥和网络地址。

## 验证方法

离线测试覆盖协议、持久化、通知、受控操作和网页流程。部署后再按[首次接入顺序](../使用指南.md#首次接入顺序)，验证实际固件、蜂窝网络、手机通知和部署网络，形成从软件检查到设备验收的完整记录。实体操作先确认目标与资费，按计划执行。
