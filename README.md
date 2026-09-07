# sys0

一个 headless 的远程指令控制与监控平台（课程设计）。操作者通过浏览器登录中心控制台，
对一台或多台已上线的被控端下发指令、查看执行结果与运行状态。中心服务器同时提供面向
AI Agent 的机器接口（HTTP API + 内置 MCP Server）。

详细设计见 [docs/计划文档.md](docs/计划文档.md)。

## 组成

| 组件 | 目录 | 说明 |
| --- | --- | --- |
| 中心服务器 Hub | `sys0-hub/` | 接入被控端、为控制台提供 REST/WS、鉴权、SQLite 持久化、MCP |
| 被控端 Agent | `sys0-agent/` | headless，主动连 Hub，执行指令，断线重连 |
| Web 控制台 | `sys0-console/` | Vite + React，深色技术风；构建产物嵌入 Hub 单二进制 |
| 共享层 | `internal/` | JSON-RPC 2.0、可插拔传输（TCP/WS）、wire 协议类型 |

## 技术要点

- **传输可插拔**：被控端 ↔ 服务器同时支持 **TCP**（4 字节长度前缀分帧）与 **WebSocket**，统一 `transport.Conn` 抽象。
- **编码统一 JSON-RPC 2.0**：请求/响应/通知三态，`internal/rpc` 实现多路复用 Peer。
- **dispatch 寻址**：控制台/API 不直接寻址被控端，发 `{select, call}`，由 Hub 扇出 + 结果聚合。
- **AI Agent 友好**：`GET /api/v1/methods` 自描述（JSON Schema 可作 LLM tool）+ `/mcp` 内置 MCP Server。
- **账号与节点权限**：实例 owner 创建账号，已登录用户认领未归属节点；节点 owner 管理访问授权。
- **API Key 护栏**：账号密钥随账号当前节点权限变化，方法白名单进一步缩小范围；支持 dryRun 预检和审计。

## 快速开始

```bash
# 1. 构建控制台 + 两个二进制（控制台产物会嵌入 hub）
make build

# 2. 启动中心服务器（默认 http :8080，agent TCP :7000，密钥 devkey）
./bin/sys0-hub -http :8080 -agent-tcp :7000 -key devkey

# 3. 启动被控端（可混用 tcp / ws）
./bin/sys0-agent -hub 127.0.0.1:7000 -transport tcp -key devkey -label lab-a
./bin/sys0-agent -hub 127.0.0.1:8080 -transport ws  -key devkey -label lab-b

# 4. 浏览器打开 http://localhost:8080 ，首次访问创建实例 owner 账号
```

仅后端（无需 Node）：`go build -o bin/sys0-hub ./sys0-hub/`（仓库已包含构建好的 `sys0-hub/web`）。

## 多用户与节点归属

- 首次初始化的管理员即 **实例 owner**（内部角色仍为 `admin`），可创建、管理账号，查看并管理任何节点的访问权限。不开放公共注册。
- 未归属节点显示 **认领** 按钮。任何已登录账号均可认领；同时认领时只有一个账号成功。认领后成为该节点的 **节点 owner**。
- 节点 owner 通过 **访问权限** 允许或禁止其他账号访问。权限为二元授权：允许即可以操作节点；获授权用户不能再授权或修改节点归属。
- 节点 owner 和实例 owner 始终有访问权，不能通过访问列表取消。API key 不能认领节点或管理权限；账号密钥仅在账号当前可访问节点范围内生效，方法限制仍然生效。
- 未授权账号仅能发现未归属节点的必要信息，不能操作；已归属且未授权的节点不可见。撤权后，后续 API/WS 请求及实时事件不再提供该节点数据。
- 删除账号时，其持有的节点交给执行删除的实例 owner，不会变为公开可认领；同名重建账号不会恢复原账号的归属或权限。最后一个实例 owner 不能被删除或降级。

升级会保留现有账号、节点和明确的节点访问授权；已有节点初始为未归属状态。旧的“新节点默认授权用户”策略停止自动授权，新节点通过认领和节点访问权限管理。迁移只运行一次，已撤销权限不会在重启后复活。升级前签发的登录令牌需重新登录；账号密码和 API key 保留。

## 被控端数据目录与身份

每个被控端在本地维护一个**数据目录**（`-data-dir`），里面放两个文件：

| 文件 | 作用 |
| --- | --- |
| `sys0-agent.id` | 该机器的稳定身份指纹。首次启动时由 `crypto/rand` 生成 16 字节随机数（hex，权限 `0600`），之后每次启动复用。握手时上报给 Hub，Hub 据此把同一台机器认成同一个节点（节点 id 由指纹派生）。 |
| `sys0-agent.lock` | 单实例文件锁。保证同一个 data-dir 同时只跑一个 agent，重复启动会直接退出。 |

**默认目录**（未显式传 `-data-dir` 时，取 `os.UserConfigDir()` 下的 `sys0-agent/`）：

| 平台 | 默认路径 |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/sys0-agent/`，未设则 `~/.config/sys0-agent/` |
| macOS | `~/Library/Application Support/sys0-agent/` |
| Windows | `%AppData%\sys0-agent\` |

选这个位置是为了「双击零参数启动」也能稳定写入——工作目录可能只读或不确定。若连用户配置目录都无法解析，回退到当前目录 `.`。容器部署通常显式指定 `-data-dir`（例如挂一个持久卷），让身份随容器重建保留。

运维提示：

- **想让某台机器在 Hub 里重新登记** → 删掉它的 `sys0-agent.id`，下次启动会生成全新身份（旧节点变离线）。
- **迁移 / 重装但要保留身份** → 备份并还原 `sys0-agent.id` 即可。
- **不要把同一个 `sys0-agent.id` 复制到多台机器** → 会造成身份冲突，Hub 把它们当成同一个节点互相顶替。
- 节点的**别名（label）和标签（tags）是运维侧管理字段**，在控制台里设置后由 Hub 持久化，不会被 agent 重连时上报的主机名覆盖。

## 测试

```bash
make test   # Go 单元/集成测试
make e2e    # 端到端：启动 hub + 两个 agent，驱动 REST/MCP 并断言
make e2e-ownership # 多账号、并发认领、授权/撤权、密钥与同名账号隔离
```

## HTTP API 摘要

| 端点 | 说明 |
| --- | --- |
| `POST /api/v1/auth/login` | 登录，返回 JWT |
| `GET /api/v1/nodes` | 当前账号可访问的节点与可认领节点 |
| `POST /api/v1/nodes/:id/claim` | 已登录账号认领节点，JSON `{}` |
| `GET /api/v1/nodes/:id/access` | 节点 owner / 实例 owner 查看访问权限 |
| `POST /api/v1/nodes/:id/access` | 替换显式授权用户，JSON `{"users":["username"]}` |
| `POST /api/v1/dispatch` | 同步下发 `{select, call, dryRun}` |
| `GET /api/v1/methods` | 能力自描述（JSON Schema） |
| `GET /api/v1/metrics?node=` | 监控时序 |
| `GET /api/v1/events?topics=` | SSE 实时事件流 |
| `GET/POST/DELETE /api/v1/keys` | API Key 管理（admin） |
| `POST /mcp` | MCP Server（initialize / tools/list / tools/call） |
