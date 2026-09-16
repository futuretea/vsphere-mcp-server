# vSphere MCP Server

[English](README.md)

面向单个已配置 ESXi 或 vCenter 目标的只读 MCP（Model Context Protocol）server。共同 API 基线支持 ESXi/vCenter 6.7、7.0.3 与 8.x；目标广告相应能力时提供 vCenter 专属库存与告警查询。

## 快速开始

```bash
# 需要 Go 1.25+（见 .tool-versions）
make build
./bin/vsphere-mcp-server version

# 启动 MCP server（默认 stdio）
./bin/vsphere-mcp-server mcp --config config.example.yaml

# 不经过 MCP 客户端，直接用 CLI 列工具 / 调工具
./bin/vsphere-mcp-server tools list
./bin/vsphere-mcp-server tools describe vsphere_list_inventory
./bin/vsphere-mcp-server tools call vsphere_list_inventory --config config.example.yaml --params '{"kind":"vm"}'
```

## 功能

- **子命令 CLI**：`mcp` 才启动 server；`tools` / `version` / `completion` 独立
- **多传输**：stdio、Streamable HTTP、SSE
- **工具过滤**：`--enabled-tools`、`--disabled-tools`、`--enable-domains`、`--disable-domains`
- **只读目标访问**：库存、指标、任务，以及按能力注册的事件和告警

## CLI 命令

| 命令 | 作用 |
|------|------|
| `vsphere-mcp-server mcp` | 启动 MCP server（stdio 或 HTTP） |
| `vsphere-mcp-server tools list` | 列出已启用工具 |
| `vsphere-mcp-server tools describe <name>` | 查看工具 schema |
| `vsphere-mcp-server tools call <name>` | 用 JSON 参数调用工具 |
| `vsphere-mcp-server version` | 打印构建信息 |
| `vsphere-mcp-server completion <shell>` | 生成 shell 补全脚本 |

### tools 示例

```bash
./bin/vsphere-mcp-server tools list
./bin/vsphere-mcp-server tools list --json
./bin/vsphere-mcp-server tools describe vsphere_list_inventory
./bin/vsphere-mcp-server tools describe vsphere_query_metrics --json
./bin/vsphere-mcp-server tools call vsphere_list_events --config config.example.yaml --params '{"limit":25}'
```

## MCP 传输模式

### Stdio（默认）

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml
```

Cursor / Claude Desktop 配置示例：

```json
{
  "mcpServers": {
    "vsphere-mcp-server": {
      "command": "/absolute/path/to/bin/vsphere-mcp-server",
      "args": ["mcp", "--config", "/absolute/path/to/config.yaml"]
    }
  }
}
```

### Streamable HTTP

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml --port 8080
curl -s http://127.0.0.1:8080/healthz
```

客户端配置示例：

```json
{
  "mcpServers": {
    "vsphere-mcp-server": {
      "url": "http://127.0.0.1:8080/mcp"
    }
  }
}
```

### SSE

与 HTTP 模式同一进程。端点：

| 路径 | 用途 |
|------|------|
| `/healthz` | 健康检查（`GET`/`HEAD`） |
| `/mcp` | Streamable HTTP |
| `/sse` | SSE 连接 |
| `/message` | SSE message endpoint |

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml --port 8080 --sse-base-url http://127.0.0.1:8080
```

### Docker

```bash
# 构建
make docker

# Stdio（默认 ENTRYPOINT 为 vsphere-mcp-server mcp）
docker run -i --rm -v /absolute/path/to/config.yaml:/etc/vsphere-mcp/config.yaml:ro ghcr.io/futuretea/vsphere-mcp-server:dev --config /etc/vsphere-mcp/config.yaml

```

HTTP / SSE **无鉴权、无 TLS**，并且仅允许监听 loopback。若要对外暴露，请在前面放置带鉴权和 TLS 的反向代理。
Docker 镜像当前仅支持 stdio；在配置好容器认证代理边界前，请从主机 loopback 运行 HTTP/SSE。

## 配置

优先级：**flags > 环境变量 > 配置文件 > 默认值**。

### 环境变量

| 变量 | 说明 | 默认 |
|------|------|------|
| `MCP_LOG_LEVEL` | 日志级别 | `info` |
| `MCP_PORT` | HTTP 端口（`0` = stdio） | `0` |
| `MCP_LISTEN` | HTTP 监听地址 | `127.0.0.1` |
| `MCP_SSE_BASE_URL` | 对外 SSE base URL | `""` |
| `MCP_VSPHERE_ENDPOINT` | ESXi 或 vCenter HTTPS 地址 | 未设置 |
| `MCP_VSPHERE_USERNAME` | 只读目标用户名 | 未设置 |
| `MCP_VSPHERE_PASSWORD` | 目标密码 | 未设置 |
| `MCP_VSPHERE_INSECURE` | 跳过 TLS 证书校验 | `false` |

### 配置文件

见 `config.example.yaml`：

```yaml
port: 0
listen: 127.0.0.1
sse_base_url: ""
log_level: info
enabled_tools: []
disabled_tools: []
enabled_domains: []
disabled_domains: []
vsphere:
  endpoint: "https://vcenter.example.invalid/sdk"
  username: "readonly-user"
  password: ""
  insecure: false
```

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml --port 8080
```

## 目录结构

```
cmd/mcp-server/          # 入口
internal/cmd/            # cobra CLI（mcp / tools / version / completion）
pkg/core/                # config / logging / version
pkg/server/mcp/          # MCP 注册与 transport
pkg/server/http/         # HTTP / SSE / healthz
pkg/toolset/             # Toolset 接口与过滤
pkg/toolset/vsphere/     # 公开的只读 vSphere tool schema
pkg/vsphere/             # 基于 govmomi 的只读查询
.github/workflows/       # CI
scripts/init-template.sh # 派生仓库初始化脚本
templates/               # 由初始化脚本复制的 CI 与发布骨架
```

## 兼容性与限制

共同查询刻意只使用 vSphere 6.7 已具备的 API。vCenter 专属库存类型为 `datacenter`、`cluster`、`resource_pool`、`folder` 和 `distributed_portgroup`。MCP server 启动时探测事件查询与 `AlarmManager`，仅在目标支持时暴露 `vsphere_list_events` 和 `vsphere_list_alarms`。本地测试使用 govmomi 模拟器，不能代替对已授权真实 6.7、7.0.3 或 8.x 环境的验证。

## 开发

```bash
make tidy
make format
make lint
make test
make coverage
make ci
make build
make docker
```

生成的构建 CI（`.github/workflows/build.yaml`）会跑 lint、测试、多 OS 构建与 CLI smoke（`version` / `tools list`），以及 Docker 镜像构建。

## 本地对照样例

`third-party-projects/` 可放置本地对照代码，已在 `.gitignore` 中，不属于模板交付物。

## License

[MIT](LICENSE)
