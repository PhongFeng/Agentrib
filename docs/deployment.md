# 部署与验收

## 服务器

1. 准备 Go 1.22+、PostgreSQL、Node.js 20.9+、pnpm 和启用 HTTPS 的 Nginx。运行 `go build -o agentrib-server ./cmd/agentrib-server`，并在 `web` 目录执行 `pnpm install --frozen-lockfile`、`pnpm build`。
2. 建立系统账户 `agentrib` 和由同名 PostgreSQL 角色拥有的数据库 `agentrib`。示例使用本机 Unix socket 和 peer 认证。
3. 将二进制安装到 `/opt/agentrib/bin/agentrib-server`。建立仅 root 可读的 `/etc/agentrib/server.env`，设置 `AGENTRIB_DATABASE_URL=postgresql:///agentrib?host=/var/run/postgresql&user=agentrib` 和随机的 `AGENTRIB_OPERATOR_TOKEN`（至少 32 个字符）。
4. 将 `deploy/agentrib.service` 安装为 systemd 服务并启动。Ubuntu 上也可以执行 `sudo bash deploy/install-linux.sh /absolute/path/agentrib-server` 完成账户、配置和服务安装。将 `web/out/` 的内容作为网站根目录下的 `console/` 发布，在 TLS 虚拟主机中包含 `deploy/nginx.conf`（安装脚本写到 `/etc/nginx/snippets/agentrib.conf`），执行 `nginx -t` 后重新加载。
5. 检查 `/healthz` 返回 `{"status":"ok"}`，打开 `/console/` 以操作员令牌登录。

## Mac 执行机器

1. 安装 Git、Godot 4 和对应的 Web 导出模板，在 Mac 上安装并登录 Codex。需要使用 Qoder CN 时可以另外安装并登录。Go Runner 可以在服务器用 `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o agentrib-runner ./cmd/agentrib-runner` 构建。
2. 在控制台登记执行机器，把一次性显示的 Runner 令牌保存到本机受限文件，权限 `0600`。将 `deploy/runner.example.json` 复制到本机并按实际路径调整。项目由本机配置映射到已有的 Godot Git 仓库。每个项目需提供一个可以用 Godot 无界面运行的 GDScript 测试入口；需要 Web 包时还应提交预设。
3. 使用 `agentrib-runner -config /absolute/path/runner.json -check` 检查可用命令，再去掉 `-check` 启动并在网页观察心跳。常驻部署可将 `deploy/com.agentrib.runner.plist` 中的 `__STATE__` 替换为配置目录的绝对路径，放入用户的 `~/Library/LaunchAgents/`，从当前用户会话用 `launchctl bootstrap` 加载。
4. 从 Godot 模板创建工作流，指定机器、项目和简报并确认。核对开发 Git SHA、Godot 报告和只读 QA 记录指向相同提交。导出包从运行详情下载，人工确认后可将其发布到独立试玩站点。

Git 提交保存在项目本机的 `refs/agentrib/runs/`，可以按需要推送到项目自己的远程。运行日志与导出包中的内容可能来自项目代码，下载与查看均需要操作员令牌。
