# 开发与模拟

使用 [go.mod](../go.mod) 声明的 Go 版本和兼容的 Node.js/npm；Docker 构建版本见 [Dockerfile](../Dockerfile)。测试和构建命令见 [CONTRIBUTING.md](../CONTRIBUTING.md)。

## 合成数据体验

无需真实板子，不使用真实 Bark Key。先在项目根目录构建网页并启动回环服务：

```sh
npm --prefix web ci
npm --prefix web run build
env -u SIM_BARK_KEY -u SIM_ADMIN_TOKEN -u SIM_AES_KEY -u SIM_AES_IV GOTOOLCHAIN=local go run ./cmd/sim -config configs/local.example.json
```

打开 `http://127.0.0.1:8080`。保持服务运行，在另一个终端进入同一目录：

```sh
env -u SIM_BARK_KEY -u SIM_ADMIN_TOKEN -u SIM_AES_KEY -u SIM_AES_IV GOTOOLCHAIN=local go run ./cmd/simulate -config configs/local.example.json
```

模拟器仅连接本机测试设备 `SIM-TEST`，不会连接调制解调器或运营商。数据写入被忽略的 `data/local`；与真实实例分开，退出使用 Ctrl+C。端口冲突时复制配置到被忽略的私有文件，协调两端地址，不停止其他应用。

## 配置模板

- `local.example.json`：回环合成体验，明文测试设备，不用于真实网络部署。
- `lan.example.json`：原生局域网起点，来源和 AES 模式必须与自己的环境核对。
- `cloud.example.json`：原生云端起点，云端安全校验。
- `container.example.json`：容器内路径和监听地址；本地与云端共用，初始化时选择环境。

示例中的设备标识、号码和地址均为占位或合成数据，不是可用的现场配置。

## 验证范围

离线测试覆盖协议、持久化、通知、受控操作和网页流程，但不能替代实际固件兼容、蜂窝网络、Bark 手机到达或云端网络验证。需要实体操作时明确目标，避免自动触发收费、重启或清理动作。
