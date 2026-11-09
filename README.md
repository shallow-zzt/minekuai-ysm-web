# Minekuai YSM Web · 麦块面板服模型衣橱

**适用于 Minekuai（麦块）面板服的 YSM 上传管理工具。** 玩家通过网页提交 Yes Steve Model 模型包，Go 后端使用面板已有的 SFTP 通道上传文件，再由配套服务端模组触发模型重载。

适合能够使用 SFTP、但无法自行映射 RCON 等额外端口的 Minecraft 面板服。网页主机可以部署在自己的电脑或服务器上，并按需通过 frp 对外提供访问。

这是社区工具，与 Minekuai、Minecraft 和 Yes Steve Model 官方没有隶属关系。其他提供兼容 SFTP 的面板也可自行验证；本文的配置场景围绕麦块面板服。

## 两个独立项目

| 项目 | 职责 | 部署位置 |
| --- | --- | --- |
| **[minekuai-ysm-web](https://github.com/shallow-zzt/minekuai-ysm-web)**（本仓库） | Go API、SFTP 传输、React / Next.js 模型管理页面 | 网站主机 |
| **[minekuai-ysm-bridge](https://github.com/shallow-zzt/minekuai-ysm-bridge)** | NeoForge 模组，读取请求并执行固定的 `ysm model reload` | Minecraft 服务端 `mods` |

本仓库只保存 Web 应用源码；桥接 JAR 的源码和构建方式在另一个仓库。两者通过文件协议协作，不需要在面板容器内额外开放端口。

## 功能

- 中文响应式模型衣橱：目录导航、名称搜索、YSM / ZIP 筛选，以及按名称、大小和修改时间排序。
- 上传弹窗和拖放上传，显示上传进度、模型库容量及重载回执。
- SFTP 支持用户名密码或私钥；凭据只保存在后端。默认首次成功认证后自动记录服务器主机指纹，后续连接核对。
- 临时上传完成后再发布，随机文件名前缀避免直接覆盖原有文件。
- 默认单文件 64 MiB、模型库 2 GiB；ZIP 检查路径、符号链接、重复条目、CRC 和解压体积。
- 默认免网站口令；可选设置上传口令与管理员口令。重载请求在游戏服务器停服期间保留。

界面参考 OpenList 的文件浏览布局，采用冰蓝、雾蓝和淡紫配色。设计参考 [OpenList Frontend](https://github.com/OpenListTeam/OpenList-Frontend)；未复制其字体、图片或源码。

## 工作方式

```text
玩家浏览器 → Go Web 应用 → 面板 SFTP → YSM custom 模型目录
                              └→ 重载请求文件
Minecraft 服务端桥接模组 ← 读取请求文件
    └→ 执行 ysm model reload → 写入回执 → 网页显示结果
YSM → 通过游戏连接向玩家客户端同步模型
```

SFTP 使用面板已提供的地址和端口。网站对外访问入口需要由你部署；桥接模组通过服务器内部文件系统工作，不监听新端口，也不接受任意控制台命令。

## 环境

- Web 构建：Node.js 22.9+、npm；Go 1.26+。精确依赖见锁文件与 `backend/go.mod`。
- 游戏服务器：Minecraft Java 1.21.1、NeoForge 21.1.x、Yes Steve Model 2.6.5，以及配套桥接模组。
- 面板账户需要有模型目录及桥接目录的 SFTP 读写权限。

Node.js 仅用于前端构建。部署时 Go 程序同时提供静态页面和 API，一个 HTTP 监听端口即可运行网站。

## 构建与启动

克隆本仓库后，在仓库根目录执行：

```sh
cd web
npm ci
npm run build
cd ../backend
go test ./...
go build -trimpath -o ../dist/ysm-bridge .
cd ..
```

Windows 构建时，将 `go build` 命令中的输出路径改为 `../dist/ysm-bridge.exe`。也可在 PowerShell 运行 `./scripts/build.ps1`；Linux 使用 `sh scripts/build.sh`。这些脚本只构建 Web 应用，桥接模组请在配套仓库单独构建。

1. 复制 `config.example.json` 为 `config.json`，填写面板提供的 SFTP 地址、用户名、密码和根目录；与 Xftp / WinSCP 使用的连接信息一致。
2. 从配套桥接仓库构建 JAR，上传至面板服 `mods` 目录，并重启游戏服务端一次。
3. Windows 双击 `start.cmd` 并保持窗口打开；Linux 在仓库根目录先执行 `chmod +x dist/ysm-bridge`，再运行 `./dist/ysm-bridge -config config.json`。
4. 打开 **http://127.0.0.1:18081**，确认模型列表可读取、重载桥在线后上传模型。

仓库不包含预构建二进制、`web/out` 或个人配置。GitHub Actions 构建成功后，可从该次运行下载 `minekuai-ysm-web` 构建产物，保持其中 `dist/`、`web/out/` 与启动文件的目录关系。

### 配置

最小配置示例：

```json
{
  "listen": "127.0.0.1:18081",
  "sftp_address": "your-sftp-host:10001",
  "sftp_user": "your-sftp-username",
  "sftp_password": "your-sftp-password",
  "remote_root": "/"
}
```

地址和端口以面板实际显示为准，不是每个实例都相同。`remote_root` 是 SFTP 视角的游戏根目录，即包含 `mods`、`config` 和 `server.properties` 的目录；如果这些文件位于登录后的根目录，填 `/`。

| 可选字段 | 默认值 / 用途 |
| --- | --- |
| `web_dir` | `web/out`，相对于进程工作目录 |
| `upload_token` | 空；设置后限制查看和上传 |
| `admin_token` | 空；设置后单独限制手动重载，管理员也可查看和上传 |
| `sftp_key_file` | SSH 私钥文件路径，需要面板支持 |
| `host_key_sha256` | 空；可预先指定 `SHA256:...` 指纹 |
| `max_upload_mb` | `64`，可配置为 1–64 MiB |
| `max_library_mb` | `2048` MiB |

可用 `YSM_SFTP_PASSWORD`、`YSM_UPLOAD_TOKEN`、`YSM_ADMIN_TOKEN` 环境变量覆盖对应值。网站口令留空时，任何能访问网站的人都可查看、上传和申请重载；对外开放前按实际使用范围设置访问限制。

首次连接成功后，服务器身份记录保存在配置文件旁的 `data/sftp-host-keys` 中。后续身份不匹配会拒绝连接；正常更换主机密钥时，请先向面板方核实，再更新相应记录。

### 对外访问 / frp

将网站主机的 `127.0.0.1:18081` 转发到自己的公网入口，并配置 HTTPS。代理需要保留 `Authorization` 头，允许至少 65 MiB 请求体，并设置足够长的上传超时（建议至少 180 秒）。SFTP 由 Go 主机向面板已有地址发起连接。

如需直接监听其他网卡，可调整 `listen` 并自行配置防火墙及访问控制。修改前端后重新构建 `web/out` 即可；单独运行 `next dev` 时需要把 API 请求同源代理到 Go。

## 模型目录与重载

模型写入 `config/yes_steve_model/custom/<随机ID>_<原文件名>`；桥接工作目录为 `config/ysm_upload_bridge`，包含 `staging`、`requests`、`acks` 和 `heartbeat.json`。这些目录需要位于同一可写文件系统。

桥接模组每 5 秒检查请求，重载批次最短间隔 30 秒；通常约 5–35 秒收到执行回执，实际受服务端状态影响。网页最多自动轮询 5 分钟，之后可手动“检查回执”。

“重载命令已执行”表示命令处理器返回成功，不代表 YSM 异步解析、授权验证和所有客户端同步都已经结束。请在游戏模型列表和服务器日志中确认最终结果。上传同名文件不会覆盖旧文件，但模型内部 ID 冲突仍需自行处理。

网页管理 `.ysm` / `.zip` 模型包；目录形式的开发模型可继续手动通过 SFTP 管理。模型默认向全服玩家开放，请仅上传有权使用且适用于服务器 YSM 版本的模型。

## 排查问题

- **启动窗口闪退 / PowerShell 禁止脚本**：使用 `start.cmd`，无需更改系统执行策略。日志位于 `data/logs/startup.log`。
- **端口已占用**：调整 `listen`，再访问对应新端口。
- **SFTP 连接失败**：核对面板地址、端口、完整用户名、密码与 `remote_root`。
- **一直等待重载桥连接**：检查桥接模组是否安装并重启，确认 `heartbeat.json` 持续更新。
- **上传中断 / 回执迟迟未到**：先刷新模型列表核实文件状态，必要时申请重载，避免重复上传。

## 验证与范围

Go 测试覆盖 SSH/SFTP 传输、上传发布、容量限制、ZIP 校验、主机身份记录和可选访问口令。前端已通过生产构建、TypeScript 检查及桌面 / 手机浏览器交互检查。此前已完成一次真实麦块实例的 SFTP 只读连接验证；自动化上传与重载测试使用隔离测试环境。

桥接模组的 Minecraft 运行时加载、YSM 重载及客户端同步仍需要在目标服务器验收。项目不附带 YSM 本体、Minecraft 文件或玩家模型。

## 作者资料与许可证

[YSM 模型格式](https://yesstevemodel.github.io/en/wiki/type/) · [YSM 常见问题](https://yesstevemodel.github.io/en/wiki/faq/) · [YSM 命令](https://yesstevemodel.github.io/en/wiki/command/)

本项目采用 [MIT License](LICENSE)。
