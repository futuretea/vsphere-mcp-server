# vSphere MCP Server

[中文文档](README.zh.md)

Read-only MCP (Model Context Protocol) server for one configured ESXi or vCenter target. It supports ESXi/vCenter 6.7, 7.0.3, and 8.x through their common API baseline, plus vCenter-only inventory and alarm capabilities when advertised by the target.

## Quick start

```bash
# Requires Go 1.25+ (see .tool-versions)
make build
./bin/vsphere-mcp-server version

# Start MCP server (stdio, default)
./bin/vsphere-mcp-server mcp --config config.example.yaml

# List / call tools from the CLI (no MCP client required)
./bin/vsphere-mcp-server tools list
./bin/vsphere-mcp-server tools describe vsphere_list_inventory
./bin/vsphere-mcp-server tools call vsphere_list_inventory --config config.example.yaml --params '{"kind":"vm"}'
```

## Features

- **Subcommand CLI**: `mcp` starts the server; `tools` / `version` / `completion` are separate commands
- **Multi-transport**: stdio, Streamable HTTP, and SSE
- **Tool filtering**: `--enabled-tools`, `--disabled-tools`, `--enable-domains`, `--disable-domains`
- **Read-only target access**: inventory, metrics, and tasks; events and alarms are capability-gated

## CLI commands

| Command | Purpose |
|---------|---------|
| `vsphere-mcp-server mcp` | Start the MCP server (stdio or HTTP) |
| `vsphere-mcp-server tools list` | List enabled tools |
| `vsphere-mcp-server tools describe <name>` | Show tool schema |
| `vsphere-mcp-server tools call <name>` | Invoke a tool with JSON params |
| `vsphere-mcp-server version` | Print build metadata |
| `vsphere-mcp-server completion <shell>` | Generate shell completion |

### Tools examples

```bash
./bin/vsphere-mcp-server tools list
./bin/vsphere-mcp-server tools list --json
./bin/vsphere-mcp-server tools describe vsphere_list_inventory
./bin/vsphere-mcp-server tools describe vsphere_query_metrics --json
./bin/vsphere-mcp-server tools call vsphere_list_events --config config.example.yaml --params '{"limit":25}'
```

## MCP transports

### Stdio (default)

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml
```

Cursor / Claude Desktop style config:

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

Client config example:

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

Same process as HTTP mode. Endpoints:

| Path | Purpose |
|------|---------|
| `/healthz` | Health check (`GET`/`HEAD`) |
| `/mcp` | Streamable HTTP |
| `/sse` | SSE connection |
| `/message` | SSE message endpoint |

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml --port 8080 --sse-base-url http://127.0.0.1:8080
```

### Docker

```bash
# Build
make docker

# Stdio (default ENTRYPOINT is `vsphere-mcp-server mcp`)
docker run -i --rm -v /absolute/path/to/config.yaml:/etc/vsphere-mcp/config.yaml:ro ghcr.io/futuretea/vsphere-mcp-server:dev --config /etc/vsphere-mcp/config.yaml

```

HTTP / SSE have **no auth and no TLS** and are restricted to loopback. Put an authenticated TLS reverse proxy in front if you expose the port.
The Docker image currently supports stdio only; run HTTP/SSE from the host loopback until an authenticated container proxy boundary is configured.

## Configuration

Priority: **flags > environment variables > config file > defaults**.

### Environment variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MCP_LOG_LEVEL` | Log level | `info` |
| `MCP_PORT` | HTTP port (`0` = stdio) | `0` |
| `MCP_LISTEN` | HTTP listen host | `127.0.0.1` |
| `MCP_SSE_BASE_URL` | Public SSE base URL | `""` |
| `MCP_VSPHERE_ENDPOINT` | ESXi or vCenter HTTPS endpoint | unset |
| `MCP_VSPHERE_USERNAME` | Read-only target username | unset |
| `MCP_VSPHERE_PASSWORD` | Target password | unset |
| `MCP_VSPHERE_INSECURE` | Skip TLS certificate verification | `false` |

### Config file

See `config.example.yaml`:

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

## Project layout

```
cmd/mcp-server/          # binary entrypoint
internal/cmd/             # cobra CLI (mcp / tools / version / completion)
pkg/core/                 # config / logging / version
pkg/server/mcp/           # MCP registration and transports
pkg/server/http/          # HTTP / SSE / healthz
pkg/toolset/              # Toolset interface and filters
pkg/toolset/vsphere/      # public read-only vSphere tool schemas
pkg/vsphere/              # govmomi-backed read-only queries
.github/workflows/        # CI
scripts/init-template.sh  # derived-project initializer
templates/                # CI and release scaffolding copied by the initializer
```

## Compatibility and limits

The common queries intentionally use APIs available in vSphere 6.7. vCenter-only inventory kinds are `datacenter`, `cluster`, `resource_pool`, `folder`, and `distributed_portgroup`. The MCP server probes event queries and `AlarmManager` at startup; it exposes `vsphere_list_events` and `vsphere_list_alarms` only when the target supports them. Local tests use govmomi simulators; they do not replace validation against an authorized real 6.7, 7.0.3, or 8.x environment.

## Development

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

Generated build CI (`.github/workflows/build.yaml`) runs lint, tests, a multi-OS
build with CLI smoke checks, and a Docker image build.

## Local reference clones

`third-party-projects/` can hold local reference checkouts. It is listed in `.gitignore` and is not part of the template deliverable.

## License

[MIT](LICENSE)
