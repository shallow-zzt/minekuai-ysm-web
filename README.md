# 衣橱 · 麦块 YSM 上传管理（API 版）

**适用于 Minekuai 麦块面板服的 YSM 上传管理工具。** 玩家通过网页上传 `.ysm` 或 `.zip` 模型包，Go 后端使用 SFTP 将文件发送到服务器，再调用麦块客户端 API，向指定实例提交固定的 `ysm model reload` 指令。

本版本直接通过面板 API 申请重载，不需要安装额外的桥接模组，也不需要 RCON 或新增面板容器端口。游戏服务器继续使用原有的 YSM。Web 应用使用 Go + React / Next.js，默认网站端口为 `18081`。

> 面板接受命令，只表示提交成功；YSM 是否完成重载、模型是否有效以及客户端是否同步，仍需查看游戏服务器日志和游戏内结果。本项目是社区工具，与 Minekuai 麦块及 Yes Steve Model 官方没有隶属关系。

## 功能

- 中文响应式模型衣橱：目录导航、名称搜索、YSM / ZIP 筛选，以及按名称、大小和时间排序。
- 上传弹窗和拖放上传，显示上传进度、模型库容量与命令提交状态。
- 通过 SFTP 完成临时上传，再通过重命名发布；随机文件名前缀避免直接覆盖原有文件。
- 通过面板 API 发送固定的 `ysm model reload`，支持手动申请重载。
- 提交记录保存在网站主机的 `data/jobs` 中，网站重启后仍可查询。提交结果不明确时不自动重发。
- SFTP 支持用户名密码或未加密私钥；首次成功认证后自动记录服务器主机指纹，之后自动核对。
- 默认单文件 64 MiB、模型库 2 GiB；ZIP 检查路径、符号链接、重复条目、CRC、条目数量及解压体积。
- 默认免网站口令；可选设置上传口令和管理员口令。面板 API Key 与 SFTP 凭据仅由后端使用。

界面参考 OpenList 的文件列表布局，配色沿用冰蓝、雾蓝和淡紫。前端参考 [OpenList Frontend](https://github.com/OpenListTeam/OpenList-Frontend)，未复制其源码、字体或图片资源。

## 工作方式

```text
玩家浏览器 → Go 网站服务 → SFTP → YSM custom 模型目录
                       └→ 麦块客户端 API → 提交 ysm model reload
                       └→ 网站主机 data/jobs → 网页查询提交状态
Minecraft / YSM → 处理重载与模型解析 → 通过游戏连接向客户端同步
```

SFTP 负责传送模型，面板 API 负责提交命令，两者使用各自的登录信息。网站只允许提交固定的 YSM 重载指令，不提供任意控制台命令输入功能。

## 配置与启动

已有构建包时，保持 `dist/`、`web/out/` 和启动文件的目录关系，在项目根目录进行配置。

1. 复制 `config.example.json` 为 `config.json`。
2. 填写面板显示的 SFTP 地址、端口、完整用户名和密码，与 Xftp / WinSCP 使用的信息一致。
3. 填写 `minekuai_instance_id`，使用需要接收模型与重载指令的同一个麦块实例。
4. 将面板 API Key 保存为配置文件旁的 `APIKEY.txt`，推荐文件中只放一条完整 API Key。`minekuai_api_key_file` 指向这个文件。
5. Windows 双击 `start.cmd` 并保持窗口打开；Linux 按下文启动对应程序。
6. 打开 **http://127.0.0.1:18081**。先确认模型列表可读取，再使用一个已知可用的小模型验证上传与重载。

`config.json` 与 `APIKEY.txt` 都属于本地配置，不随源码、部署包或 GitHub 仓库分发。

示例配置：

```json
{
  "listen": "127.0.0.1:18081",
  "sftp_address": "your-sftp-host:10001",
  "sftp_user": "your-sftp-username",
  "sftp_password": "your-sftp-password",
  "remote_root": "/",
  "minekuai_instance_id": "1234abcd",
  "minekuai_api_key_file": "APIKEY.txt"
}
```

示例中的 `1234abcd` 是占位值，必须换成自己的实例 ID。SFTP 地址和端口以面板显示为准。API Key 需要有权访问该实例并提交命令。

| 字段 | 用途 |
| --- | --- |
| `listen` | 网站监听地址，默认 `127.0.0.1:18081` |
| `web_dir` | 静态页面目录，默认 `web/out`，相对于配置文件所在目录 |
| `sftp_address` | 面板提供的 SFTP 主机和端口 |
| `sftp_user` | 面板显示的完整 SFTP 用户名 |
| `sftp_password` | SFTP 密码，可用 `YSM_SFTP_PASSWORD` 环境变量覆盖 |
| `sftp_key_file` | 可选，SSH 私钥文件路径，需要面板支持 |
| `host_key_sha256` | 可选，预先指定 `SHA256:...` 指纹；省略时首次成功认证后自动记录 |
| `remote_root` | SFTP 视角下包含 `mods`、`config`、`server.properties` 的游戏根目录 |
| `minekuai_instance_id` | 接收重载指令的麦块实例 ID，可用 `YSM_MINEKUAI_INSTANCE_ID` 环境变量覆盖 |
| `minekuai_api_key_file` | 保存 API Key 的本地文件；相对路径以 `config.json` 所在目录为基准 |
| `minekuai_api_key` | 可选，直接在配置中填写 API Key；推荐使用独立文件或环境变量 |
| `upload_token` | 可选，限制查看和上传；仅设置这一项时，手动重载也使用此口令 |
| `admin_token` | 可选，单独限制手动重载；管理员也可查看和上传 |
| `max_upload_mb` | 默认 `64`，支持 1–64 MiB |
| `max_library_mb` | 默认 `2048` MiB |

也可使用 `YSM_MINEKUAI_API_KEY` 提供面板 API Key，使用 `YSM_UPLOAD_TOKEN` / `YSM_ADMIN_TOKEN` 覆盖网站口令。采用环境变量或直接配置 API Key 时，无需另外创建密钥文件。API Key 不是 SFTP 密码，也不是玩家使用的网站口令。

默认免网站口令，能访问网页的人可以查看、上传模型及申请重载。需要限制访问时再配置网站口令。配置文件、密钥文件及 `data/` 目录应仅供网站服务账户读写；`.gitignore` 已排除 `APIKEY*.txt`、本地配置、密钥、运行记录和日志。

首次成功登录 SFTP 后，主机身份记录保存在配置文件旁的 `data/sftp-host-keys`。后续身份变化会拒绝连接；正常更换服务器密钥时，应先向面板方核实，再更新对应记录。很多面板把游戏目录直接作为 SFTP 的 `/`，此时 `remote_root` 填 `/`。

### 启动命令

Windows：

```powershell
.\dist\ysm-bridge.exe -config config.json
```

也可以直接双击 `start.cmd`。如果 PowerShell 执行策略禁止运行 `.ps1`，使用 `start.cmd` 即可。启动失败会保留错误提示，日志位于配置文件旁的 `data/logs/startup.log`。

Linux 源码构建产物：

```sh
chmod +x dist/ysm-bridge
./dist/ysm-bridge -config config.json
```

如果部署包提供的是 `dist/ysm-bridge-linux-amd64`，把上面两条命令中的文件名改成它即可。程序名 `ysm-bridge` 沿用旧版命名，API 版自身不需要桥接 JAR。

### 对外访问 / frp

网站运行时由 Go 同时提供静态页面和 API，只需要一个 HTTP 监听端口。可通过 frpc 将同机 `127.0.0.1:18081` 转发到公网入口，并为公网入口配置 HTTPS。代理需保留 `Authorization` 头，允许至少 65 MiB 请求体，并设置足够长的上传超时。

网站主机需要能够连接面板 SFTP 和麦块 API。SFTP 上传成功并不能证明 API Key 有效；二者应分别确认。

## 提交状态与重试

| 状态 | 含义 |
| --- | --- |
| `pending` | 请求正在提交，或曾中断而没有最终记录；先检查控制台，不会自动重发 |
| `submitted` | 麦块面板 API 已接受命令；游戏执行与客户端同步仍需确认 |
| `failed` | 请求明确失败，查看提示并修正配置或权限后再重试 |
| `unknown` | 连接中断、超时或响应异常导致提交结果不明确，命令可能已被接受 |
| `uploaded` | 模型已上传，但本地记录保存失败，尚未发送重载指令；修复后手动申请重载 |

提交记录位于配置文件旁的 `data/jobs`。网页查询的是本地记录与面板提交结果，不是游戏内命令执行回执。记录可以在网站重新启动后查询。

手动重载与上传触发的重载共用 10 秒提交间隔；间隔内的新上传仍保留文件，并提示稍后手动重载。

对于 `unknown` 和中断后留下的 `pending`，程序不会自动重新发送命令。先查看面板控制台或服务端日志，确认是否已经执行，再由管理员手动申请重载。重复刷新页面或查询记录不会自动补发；服务器停机期间也不承诺开服后自动重放命令。

如果上传中断，先刷新模型列表核实文件是否已存在。文件已上传时只需按实际情况申请重载，无需重复上传。模型内部 ID 冲突无法靠随机文件名前缀解决，需要修改模型或由服主移除冲突文件。

## 模型目录与验收

模型写入 `config/yes_steve_model/custom/<随机ID>_<原文件名>`。临时传输使用 SFTP 下的 staging 目录，完整写入后再发布到模型目录，两者需位于同一可写文件系统。

API 版的重载不依赖旧桥接模组的 `requests`、`acks` 或 `heartbeat.json`。从旧版迁移时，保留现有模型与 SFTP 配置，新增面板实例 ID 和 API Key，再启动 API 版网站。旧桥接项目的源码和安装文件独立保留，不包含在 API 版构建流程中。

建议按以下顺序验收：

1. 确认网页能读取当前服务器的模型列表。
2. 上传一个已知适用于服务器 YSM 版本的小模型，确认目标目录中出现完整文件。
3. 确认网页显示面板已接受命令，随后在面板控制台或服务端日志中检查 YSM 重载结果。
4. 进入游戏的模型选择界面，确认模型已正常解析并同步到客户端。

原有目标环境为 Minecraft Java 1.21.1、NeoForge 21.1.x、YSM 2.6.5。Web 应用只负责传输和命令提交，模型兼容性、授权校验及客户端同步由原有 YSM 环境处理。`.ysm` 文件仅检查扩展名和大小；ZIP 另外检查路径与内容结构。模型默认向全服玩家开放，请只上传有权使用的模型。

## 从源码构建

需要 Go 1.26+、Node.js 22.9+ 和 npm。只构建 Web 前端与 Go 后端，不需要 JDK、Gradle 或桥接模组源码。

Windows 在 PowerShell 执行 `./scripts/build.ps1`；Linux 执行 `sh scripts/build.sh`。两个脚本都会构建前端、检查 Go 代码并生成网站程序。

也可以分开执行：

```sh
cd web
npm ci
npm run build
cd ../backend
go test ./...
go vet ./...
go build -trimpath -o ../dist/ysm-bridge .
cd ..
```

Windows 的 `go build` 输出路径改为 `../dist/ysm-bridge.exe`。前端输出在 `web/out`；部署时无需 Node.js。修改前端后重新构建 `web/out` 即可，独立运行 `next dev` 则需要把 API 请求同源代理到 Go。

源码或部署包均不包含真实 API Key、SFTP 凭据、个人配置、运行记录或玩家模型。创建部署包时使用 `config.example.json` 作为模板，排除 `config.json`、`APIKEY*.txt`、`.env*`、私钥及 `data/`。

## 排查问题

- **SFTP 连接失败**：核对面板地址、端口、完整用户名、密码及游戏根目录。
- **模型已上传但重载失败**：检查 API Key 是否有效、是否有该实例的命令权限，以及实例 ID 是否与上传目标一致。
- **密钥文件读取失败**：检查 `minekuai_api_key_file` 指向配置文件旁的正确文件，或改用 `YSM_MINEKUAI_API_KEY`。
- **提交结果未知**：先查看面板日志确认命令结果，再决定是否手动重试。
- **面板已接受但游戏模型未出现**：检查服务器是否正常运行、YSM 加载日志、模型兼容性和客户端同步情况。
- **端口被占用 / 启动失败**：检查 `data/logs/startup.log`，必要时修改 `listen`，用新端口访问。

## 许可与资料

本项目采用 [MIT License](LICENSE)。不包含 YSM 本体、Minecraft 文件或玩家模型；这些内容遵循各自许可。

[YSM 模型格式](https://yesstevemodel.github.io/en/wiki/type/) · [YSM 常见问题](https://yesstevemodel.github.io/en/wiki/faq/) · [YSM 命令说明](https://yesstevemodel.github.io/en/wiki/command/)
