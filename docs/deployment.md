# 部署与验收

## 服务器

1. 准备 Go 1.22+、PostgreSQL、Node.js 20.9+、pnpm 和启用 HTTPS 的 Nginx。运行 `go build -o agentrib-server ./cmd/agentrib-server`，并在 `web` 目录执行 `pnpm install --frozen-lockfile`、`pnpm build`。
2. 建立系统账户 `agentrib` 和由同名 PostgreSQL 角色拥有的数据库 `agentrib`。示例使用本机 Unix socket 和 peer 认证。
3. 将二进制安装到 `/opt/agentrib/bin/agentrib-server`。建立仅 root 可读的 `/etc/agentrib/server.env`，设置 `AGENTRIB_DATABASE_URL=postgresql:///agentrib?host=/var/run/postgresql&user=agentrib` 和随机的 `AGENTRIB_OPERATOR_TOKEN`（至少 32 个字符）。
4. 将 `deploy/agentrib.service` 安装为 systemd 服务并启动。Ubuntu 上也可以执行 `sudo bash deploy/install-linux.sh /absolute/path/agentrib-server` 完成账户、配置和服务安装。将 `web/out/` 的内容作为网站根目录下的 `console/` 发布，在 TLS 虚拟主机中包含 `deploy/nginx.conf`（安装脚本写到 `/etc/nginx/snippets/agentrib.conf`），执行 `nginx -t` 后重新加载。
5. 检查 `/healthz` 返回 `{"status":"ok"}`，打开 `/console/` 以操作员令牌登录。

## Mac 执行机器

1. 安装 Git 和项目所选的引擎。Godot 项目需要 Godot 4 和对应的 Web 导出模板。在执行机器上安装并登录工作流所用的 Agent CLI。Go Runner 可以在服务器用 `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o agentrib-runner ./cmd/agentrib-runner` 构建。
2. 在控制台登记执行机器，把一次性显示的 Runner 令牌保存到本机受限文件，权限 `0600`。将 `deploy/runner.example.json` 复制到本机并按实际路径调整。项目由本机配置映射到已有的 Godot Git 仓库。每个项目需提供一个可以用 Godot 无界面运行的 GDScript 测试入口；需要 Web 包时还应提交预设。
3. 在 `commands` 内填写各 CLI 的绝对路径，以便后台 Runner 找到它们。使用 `agentrib-runner -config /absolute/path/runner.json -check` 检查可用命令，再去掉 `-check` 启动并在网页观察心跳。常驻部署可将 `deploy/com.agentrib.runner.plist` 中的 `__STATE__` 替换为配置目录的绝对路径，放入用户的 `~/Library/LaunchAgents/`，从当前用户会话用 `launchctl bootstrap` 加载。
4. 从游戏模板创建工作流，为每个 Agent 步骤分配执行者，再指定机器、项目和简报。检查开发步骤从上游提交开始，且开发提交、引擎测试报告和 QA 记录指向同一版本。导出包可以从运行详情下载。

## 编写产品文档

新增步骤「编写产品文档」，角色填写「产品策划」，执行方式可选 DeepSeek Harness。任务说明写明产物路径，例如「根据简报编写 `docs/product.md`，覆盖用户场景、功能范围和可验证的验收标准」。将该步骤设为后续步骤的前置依赖和提交来源。Runner 提交文件后，后续 Agent 会在该提交上继续。

DeepSeek Harness 使用支持 `--profile headless` 的版本。使用 DeepSeek 官方 API 时，从 Runner 启动环境或受限的 `$DSH_HOME/.env` 配置 `DEEPSEEK_API_KEY`。OpenClaw 使用带 `agent exec` 和 `--cwd` 的版本，以便将工具工作目录绑定到本次项目。安装后用 Runner 的 `-check` 检查可发现的 CLI，再派一条任务验证模型访问权限。只有支持只读工具限制的适配器提供「只读验收」选项。

## 引擎适配

工作流版本保存 `engine`。测试与导出步骤使用通用的 `engine` 执行方式，派单时解析为该版本选定的引擎。Runner 上报项目及其引擎，控制台筛选匹配的项目。较早的工作流与 Runner 项目配置默认使用 Godot，`godot_dir` 仍可作为旧项目的目录字段。新项目使用 `engine` 和可选的 `engine_dir`。

接入新引擎时，注册引擎及其 Runner 适配器。适配器负责探测本机 CLI、校验项目配置和目录、在指定 Git 提交上运行测试和构建。测试输出使用 `test` 事件交给 QA，构建产物通过通用的产物接口回传。Godot 已实现这套接口。Unreal Engine、Unity 和 Cocos 的工作流可以预先保存，需等待各自的适配器后才能派单。

Git 提交保存在项目本机的 `refs/agentrib/runs/`，可以按需要推送到项目自己的远程。运行日志与导出包中的内容可能来自项目代码，下载与查看均需要操作员令牌。
