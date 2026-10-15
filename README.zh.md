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

### 查询 VM 监控

以下 JSON 可直接作为 MCP 工具参数，也可传给 `tools call <工具名> --config config.yaml --params '<JSON>'`。

1. 用 `{"kind":"vm","limit":100,"offset":0}` 调用 `vsphere_list_inventory`。每行包含 `type`、`id`、`name`、`path`。每次将 `offset` 增加实际返回行数，直到返回空页。默认覆盖有权访问的所有数据中心；可增加 `datacenter_id`（从 `{"kind":"datacenter"}` 获取）和精确匹配的 `name` 缩小范围。分页按路径、类型、ID 排序；两次调用间库存变化可能使偏移位置改变。
2. 用 `{"entity_type":"VirtualMachine","entity_id":"vm-123"}` 调用 `vsphere_get_resource`，ID 替换为上一步结果。选取的摘要包含 CPU、内存配置，电源状态、快速统计，以及目标提供的客户机信息。也可使用主机、存储的库存类型读取对应摘要。
3. 用 `{"entity_type":"VirtualMachine","entity_id":"vm-123","limit":1000}` 调用 `vsphere_query_metrics`，发现可用指标名称、单位和实例。这个旧工具返回指标目录，不含采样值和时间。默认仍最多返回 100 项，上限为 1000。
4. 调用 `vsphere_query_metric_samples` 读取实际数值：

   ```json
   {"entity_type":"VirtualMachine","entity_id":"vm-123","metrics":["cpu.usage.average","mem.usage.average"],"max_samples":10}
   ```

采样结果包含 `entity`、`sample_info`（时间和以秒计的采样间隔），以及 `metrics`（ID、名称、单位、实例、数值和比例）。数值与 `sample_info` 按下标对应，按时间升序排列。显示时将原始数值乘以 `scale`：百分比原始值 `1250`、比例 `0.01` 表示 `12.5%`。无读数时保留空数组，不改写目标返回的数值。

默认读取最近 1 个点，使用目标提供的实时刷新间隔和汇总实例（`""`）；`max_samples` 可设为 1–1000。设备级读数可选择目录中的实例，或传 `"instance":"*"`。磁盘、网络指标随目标和采集级别变化，先查目录中的名称与实例。历史查询需传 RFC3339 格式的 `start_time` / `end_time` 和已启用的 `interval_seconds`；是否有历史数据、保留多久取决于目标采集配置，扩大时间范围不会补出缺失的历史。

### 事件、任务与活动告警

`vsphere_list_events` 和 `vsphere_list_tasks` 支持可选且成对的 `entity_type` / `entity_id`、RFC3339 格式的 `start_time` / `end_time`，以及已有的 `limit`（默认 100，上限 1000）。先筛选，再限制条数。任务从目标的**近期任务缓存**中按排队时间从新到旧选取，时间筛选也使用排队时间；不提供任意历史任务查询。

```json
{"entity_type":"VirtualMachine","entity_id":"vm-123","start_time":"2026-10-14T00:00:00Z","end_time":"2026-10-14T01:00:00Z","limit":25}
```

支持告警的目标中，`vsphere_list_alarms` 继续返回告警定义。新增的 `vsphere_list_triggered_alarms` 使用同样的可选对象参数和 `limit`，查询当前告警状态、名称、对象引用、触发时间和确认信息；不传对象参数时查询有权访问的根范围。

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

默认监听地址为 `127.0.0.1`。如需监听所有网络接口：

```bash
./bin/vsphere-mcp-server mcp --config config.example.yaml --port 8080 --listen 0.0.0.0
```

客户端 URL 应使用服务器可访问的 IP 或主机名，不要使用 `0.0.0.0`。
HTTP / SSE **无鉴权、无 TLS**。若要对外暴露，请在前面放置带鉴权和 TLS 的反向代理。

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
docker run -i --rm -v /absolute/path/to/config.yaml:/etc/vsphere-mcp/config.yaml:ro ghcr.io/futuretea/vsphere-mcp-server:v0.1.0 --config /etc/vsphere-mcp/config.yaml

# 使用本地构建镜像运行 HTTP / SSE
docker run --rm -p 127.0.0.1:8080:8080 -v /absolute/path/to/config.yaml:/etc/vsphere-mcp/config.yaml:ro ghcr.io/futuretea/vsphere-mcp-server:dev --config /etc/vsphere-mcp/config.yaml --port 8080 --listen 0.0.0.0
```

HTTP 示例在容器内监听所有网络接口，并将端口发布到主机的 loopback 地址。

## 发布

`v0.1.0` 是首个版本。GitHub Release 提供带校验和的 macOS（amd64、arm64）、Linux（amd64、arm64）和 Windows（amd64）压缩包。容器镜像支持 Linux amd64 与 arm64，并同时发布到 `ghcr.io/futuretea/vsphere-mcp-server:v0.1.0` 和 `:latest`。

推送稳定的 `vX.Y.Z` tag 会触发发布工作流：先验证源码，再发布镜像，最后创建 GitHub Release。任一步失败时保留 tag，人工重试；工作流不会自动删除公开产物。1.0 前允许破坏 MCP 工具 schema 的变更，但必须在 release notes 中说明。

创建 tag 前，添加 `releases/vX.Y.Z.md`，其中必须有非空的 `Breaking MCP tool schema changes` 小节；没有破坏性变更时写 `None.`。

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

共同查询刻意只使用 vSphere 6.7 已具备的 API。vCenter 专属库存类型为 `datacenter`、`cluster`、`resource_pool`、`folder` 和 `distributed_portgroup`。MCP server 启动时探测事件查询与 `AlarmManager`，仅在目标支持时暴露事件和两个告警工具。明确的事件 API 不支持错误会禁用该工具；临时探测错误会携带原始错误让发现过程失败，不再静默隐藏工具。会话失效会报告认证错误，尚未实现自动重连。本地测试使用 govmomi 模拟器，不能代替对已授权真实 6.7、7.0.3 或 8.x 环境的验证。

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
