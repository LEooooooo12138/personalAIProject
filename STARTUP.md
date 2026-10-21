# Go Agent 本地启动

从仓库根目录运行以下 PowerShell 命令。首次使用先准备 Go、Node.js（版本见 `go-agent/web/package.json`）与项目所需的本地模型服务。

```powershell
Set-Location ./go-agent
if (-not (Test-Path -LiteralPath ./config/agent.yaml)) {
    Copy-Item -LiteralPath ./config/agent.example.yaml -Destination ./config/agent.yaml
}
```

编辑 `config/agent.yaml`，填写当前主机的服务监听地址、模型名称和知识库目录。密钥使用进程环境或受保护的本机配置；程序不会自动加载 `config/.env`。启用 Console 时，将 `console.enabled` 设为 `true`，并按访问方式配置 `server.listen_address`、`console.public_origin` 与 HTTP/HTTPS 选项。管理员初始化及 OAuth 设置见[本地配置说明](go-agent/docs/console-local-setup.md)。

构建前端与后端，再以前面准备的配置启动：

```powershell
Push-Location ./web
npm ci
npm run build
Pop-Location
go build -o agentd.exe ./cmd/agentd
.\agentd.exe -config ./config/agent.yaml
```

示例配置的端口为 `8080`；按实际监听地址访问 `/app/`。已有运行实例应沿用其服务管理和回滚流程，避免重复启动或覆盖其账号、OAuth 与配置。以上是启动说明，不代表已在当前主机重新部署或验收。
