# 部署与验收

## 服务器

1. 准备 Go 1.22+、PostgreSQL、Node.js 20.9+、pnpm 和启用 HTTPS 的 Nginx。运行 `go build -o agentrib-server ./cmd/agentrib-server`，并在 `web` 目录执行 `pnpm install --frozen-lockfile`、`pnpm build`。
2. 建立系统账户 `agentrib` 和由同名 PostgreSQL 角色拥有的数据库 `agentrib`。示例使用本机 Unix socket 和 peer 认证。
3. 将二进制安装到 `/opt/agentrib/bin/agentrib-server`。建立仅 root 可读的 `/etc/agentrib/server.env`，设置 `AGENTRIB_DATABASE_URL=postgresql:///agentrib?host=/var/run/postgresql&user=agentrib` 和随机的 `AGENTRIB_OPERATOR_TOKEN`（至少 32 个字符）。
4. 将 `deploy/agentrib.service` 安装为 systemd 服务并启动。Ubuntu 上也可以执行 `sudo bash deploy/install-linux.sh /absolute/path/agentrib-server` 完成账户、配置和服务安装。将 `web/out/` 的内容作为网站根目录下的 `console/` 发布，在 TLS 虚拟主机中包含 `deploy/nginx.conf`（安装脚本写到 `/etc/nginx/snippets/agentrib.conf`），执行 `nginx -t` 后重新加载。
5. 检查 `/healthz` 返回 `{"status":"ok"}`，打开 `/console/` 以操作员令牌登录。

## Mac 执行机器

1. 安装 Git 和项目所选的引擎。Godot 项目需要 Godot 4 和对应的 Web 导出模板。在执行机器上安装并登录工作流所用的 Agent CLI。Go Runner 可以在服务器用 `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o agentrib-runner ./cmd/agentrib-runner` 构建。
2. 制作工作台使用一台执行机器。首次部署时，用操作员令牌向 `POST /api/v1/machines` 提交 `{"name":"Mac mini"}`，将响应中的 Runner 令牌 `token` 保存到执行机器上的受限文件，权限 `0600`。已有 Runner 可沿用原令牌。将 `deploy/runner.example.json` 复制到执行机器并按实际路径调整。已有 Godot Git 仓库通过配置中的 `projects` 登记，使用各自的无界面 GDScript 测试入口。需要 Web 包时还应提交预设。升级时先启动新版服务端，再替换 Runner。
3. 在 `commands` 内填写各 CLI 的绝对路径，以便后台 Runner 找到它们。需要通过本机代理访问控制面时，在 Runner 配置中设置 `https_proxy`。使用 `agentrib-runner -config /absolute/path/runner.json -check` 检查可用命令，再去掉 `-check` 启动并在网页观察心跳。常驻部署可将 `deploy/com.agentrib.runner.plist` 中的 `__STATE__` 替换为配置目录的绝对路径，放入用户的 `~/Library/LaunchAgents/`，从当前用户会话用 `launchctl bootstrap` 加载。
4. 在「游戏项目」中选择已有项目，或创建新的 Godot 项目。Runner 会在 `<state>/projects/<项目标识>` 初始化独立的 Git 仓库，写入初始提交和基础 Godot 测试。该目录应随 Runner 状态一起备份。
5. 从游戏模板创建工作流，为每个 Agent 步骤分配执行者并填写简报。Runner 执行首个步骤时记录选中项目当前的 `HEAD`，并从该提交创建独立的 Git worktree。本地未提交的改动保留在原工作目录。后续步骤可以接续工作流中指定的上游提交。导出包可以从运行详情下载。

也可以在「发起制作」展开产品 Agent 对话，选择执行机器上支持只读执行的 Agent。Codex 可使用项目或执行机器上已安装的技能。发送需求并讨论后，点击「生成简报」，再点击「使用这份简报」填入工作流表单。

默认游戏模板从已确认的简报开始。制作人将需求及分工写入 `docs/production/`。美术和音效分别接续制作人的提交，`develop` 等两边完成后合并它们的 Git 产出并接入资源。`ci` 运行引擎测试并生成试玩包。QA 提交通过结论后，交付通知出现在「运行记录」，并提供试玩包和验收报告入口。

游戏工作流内由 Runner 执行的每个步骤上限为 2 小时。工作流外的任务仍为 20 分钟。达到上限后，工作台会在运行记录中标出「执行超时」。

在工作台上方切换到「运行记录」，先选择游戏项目，再查看该项目的状态、步骤和日志。执行停止后，可以点击「从失败处继续」。工作台保留已通过的步骤、审批、Git 提交和产物，以原输入重新执行失败、失联或已取消的步骤，并保留之前的日志。重试使用新的独立工作区。

## 编写产品文档

新增步骤「编写产品文档」，角色填写「产品策划」，执行方式可选 DeepSeek Harness。任务说明写明产物路径，例如「根据简报编写 `docs/product.md`，覆盖用户场景、功能范围和可验证的验收标准」。将该步骤设为后续步骤的前置依赖和提交来源。Runner 提交文件后，后续 Agent 会在该提交上继续。

DeepSeek Harness 使用支持 `--profile headless` 的版本。使用 DeepSeek 官方 API 时，从 Runner 启动环境或受限的 `$DSH_HOME/.env` 配置 `DEEPSEEK_API_KEY`。OpenClaw 使用带 `agent exec` 和 `--cwd` 的版本，以便将工具工作目录绑定到本次项目。安装后用 Runner 的 `-check` 检查可发现的 CLI，再派一条任务验证模型访问权限。只有支持只读工具限制的适配器提供「只读验收」选项。

执行机器卡片显示已安装的 Agent、CLI 状态和可查询的额度。Codex 通过本机 App Server 读取 ChatGPT 用量窗口。DeepSeek Harness 配置了 Runner 环境变量 `DEEPSEEK_API_KEY` 时，可通过 DeepSeek 官方 API 读取余额。Qoder CN 使用 CLI 的 JSON 状态查询登录情况，Credits 可在 Qoder CN 交互界面查看。

## 引擎适配

工作流版本保存 `engine`。测试与导出步骤使用通用的 `engine` 执行方式，派单时解析为该版本选定的引擎。Runner 上报项目、引擎和 Git 工作区路径，项目引擎和工作流引擎一致时可派单。较早的工作流与 Runner 项目配置默认使用 Godot，`godot_dir` 仍可作为旧项目的目录字段。手动登记的项目使用 `engine` 和可选的 `engine_dir`。

接入新引擎时，注册引擎及其 Runner 适配器。适配器负责探测本机 CLI、校验项目配置和目录、在指定 Git 提交上运行测试和构建。测试输出使用 `test` 事件交给 QA，构建产物通过通用的产物接口回传。Godot 已实现这套接口。Unreal Engine、Unity 和 Cocos 的工作流可以预先保存，需等待各自的适配器后才能派单。

Git 提交保存在项目本机的 `refs/agentrib/runs/`，可以按需要推送到项目自己的远程。要让下一次制作沿用某次运行的成果，先将该提交合并到项目当前分支。运行日志与导出包中的内容可能来自项目代码，下载与查看均需要操作员令牌。
