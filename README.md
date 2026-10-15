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

### Query VM monitoring

Use these JSON objects as MCP tool arguments, or pass them to `tools call <name> --config config.yaml --params '<JSON>'`.

1. Call `vsphere_list_inventory` with `{"kind":"vm","limit":100,"offset":0}`. Each row contains `type`, `id`, `name` and `path`. Increase `offset` by the number of returned rows until a page is empty. Results span accessible data centers; optionally add `datacenter_id` (from `{"kind":"datacenter"}`) and an exact `name` to narrow the query. Pages use path/type/id order; inventory changes between calls can shift offsets.
2. Call `vsphere_get_resource` with `{"entity_type":"VirtualMachine","entity_id":"vm-123"}` using the returned ID. The selected summary includes CPU and memory configuration, power state, quick statistics, and guest information when the target provides it. Host and datastore summaries are available through their inventory types too.
3. Call `vsphere_query_metrics` with `{"entity_type":"VirtualMachine","entity_id":"vm-123","limit":1000}` to discover available counter names, units and instances. This existing tool returns a counter directory, without readings or timestamps. Its default limit remains 100; the maximum is 1000.
4. Call `vsphere_query_metric_samples` to read values:

   ```json
   {"entity_type":"VirtualMachine","entity_id":"vm-123","metrics":["cpu.usage.average","mem.usage.average"],"max_samples":10}
   ```

The sample response contains `entity`, `sample_info` (timestamp and interval in seconds), and `metrics` (id, name, unit, instance, values and scale). Values align by index with `sample_info` in ascending time order. Multiply each raw value by `scale` to display its unit: a percent reading of `1250` with scale `0.01` means `12.5%`. Empty readings remain empty; provider values are preserved.

Sampling defaults to one recent point at the provider's real-time refresh rate and the aggregate instance (`""`). `max_samples` accepts 1–1000. To inspect device readings, select a discovered instance or use `"instance":"*"`. Disk/network counters vary by target and collection level; discover their names and instances first. For historical data, pass RFC3339 `start_time` / `end_time` and an enabled `interval_seconds`. Availability and retention depend on the target's collection settings; a wider time range does not create missing history.

### Events, tasks and active alarms

`vsphere_list_events` and `vsphere_list_tasks` accept optional paired `entity_type` / `entity_id`, RFC3339 `start_time` / `end_time`, and the existing `limit` (default 100, maximum 1000). Filters apply before the limit. Tasks are selected from the target's **recent-task cache**, ordered by newest queue time; the time filter also uses queue time. This does not query arbitrary task history.

```json
{"entity_type":"VirtualMachine","entity_id":"vm-123","start_time":"2026-10-14T00:00:00Z","end_time":"2026-10-14T01:00:00Z","limit":25}
```

On targets with alarm support, `vsphere_list_alarms` continues to return alarm definitions. Use `vsphere_list_triggered_alarms` with the same optional entity pair and `limit` to read current alarm states, names, entity references, trigger times and acknowledgement information. Omit the entity pair to query the accessible root scope.

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

The default listen host is `127.0.0.1`. To listen on all interfaces:

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml --port 8080 --listen 0.0.0.0
```

Clients should use the server's reachable IP or hostname in the URL, not `0.0.0.0`.
HTTP / SSE have **no auth and no TLS**. Put an authenticated TLS reverse proxy in front if you expose the port.

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
docker run -i --rm -v /absolute/path/to/config.yaml:/etc/vsphere-mcp/config.yaml:ro ghcr.io/futuretea/vsphere-mcp-server:v0.1.0 --config /etc/vsphere-mcp/config.yaml

# HTTP / SSE using the locally built image
docker run --rm -p 127.0.0.1:8080:8080 -v /absolute/path/to/config.yaml:/etc/vsphere-mcp/config.yaml:ro ghcr.io/futuretea/vsphere-mcp-server:dev --config /etc/vsphere-mcp/config.yaml --port 8080 --listen 0.0.0.0
```

The HTTP example listens on all interfaces inside the container and publishes the port on the host loopback address.

## Releases

`v0.1.0` is the first release. GitHub Releases provide checksummed archives for
macOS (amd64, arm64), Linux (amd64, arm64), and Windows (amd64). The container
image is published for Linux amd64 and arm64 at both
`ghcr.io/futuretea/vsphere-mcp-server:v0.1.0` and `:latest`.

Pushing a stable `vX.Y.Z` tag runs the release workflow. It validates the
source, publishes the image, then creates the GitHub Release. If any step
fails, the tag remains for an explicit retry; the workflow does not delete
public artifacts automatically. Before 1.0, breaking MCP tool-schema changes
are allowed only when called out in the release notes. Before creating a tag,
add `releases/vX.Y.Z.md` with a non-empty `Breaking MCP tool schema changes`
section; write `None.` when there is no breaking change.

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

The common queries intentionally use APIs available in vSphere 6.7. vCenter-only inventory kinds are `datacenter`, `cluster`, `resource_pool`, `folder`, and `distributed_portgroup`. The MCP server probes event queries and `AlarmManager` at startup; it exposes events and the two alarm tools only when the target supports them. Definite unsupported event API faults disable that tool; temporary probe errors fail discovery with the original error instead of silently hiding it. Session expiration is reported as an authentication error; automatic reconnection is not implemented. Local tests use govmomi simulators; they do not replace validation against an authorized real 6.7, 7.0.3, or 8.x environment.

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
