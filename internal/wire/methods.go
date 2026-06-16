package wire

// MethodSpec describes a callable method for capability discovery. ParamsSchema
// and ResultSchema are JSON Schema objects (as Go maps) and double as LLM tool
// definitions.
type MethodSpec struct {
	Name         string         `json:"name"`
	Scope        string         `json:"scope"` // "node" or "hub"
	Description  string         `json:"description"`
	Dangerous    bool           `json:"dangerous"`
	Interactive  bool           `json:"interactive"` // streaming/PTY; not for the generic form
	ParamsSchema map[string]any `json:"paramsSchema"`
	ResultSchema map[string]any `json:"resultSchema,omitempty"`
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str() map[string]any   { return map[string]any{"type": "string"} }
func intg() map[string]any  { return map[string]any{"type": "integer"} }
func boolt() map[string]any { return map[string]any{"type": "boolean"} }

// NodeMethods is the registry of node-scoped methods exposed via dispatch.
var NodeMethods = []MethodSpec{
	{
		Name: MethodShellRun, Scope: "node", Dangerous: true,
		Description: "在被控端执行一条 shell 命令，返回 stdout/stderr/退出码。",
		ParamsSchema: obj(map[string]any{
			"cmd":     str(),
			"cwd":     str(),
			"timeout": intg(),
		}, "cmd"),
		ResultSchema: obj(map[string]any{"stdout": str(), "stderr": str(), "exit": intg()}),
	},
	{
		Name: MethodShellOpen, Scope: "node", Dangerous: true, Interactive: true,
		Description:  "打开一个交互式 PTY shell 会话（透传系统控制台），输出经 emit 流式推送。",
		ParamsSchema: obj(map[string]any{"shell": str(), "cols": intg(), "rows": intg()}),
	},
	{
		Name: MethodShellInput, Scope: "node", Dangerous: true, Interactive: true,
		Description:  "向交互式 shell 会话写入输入（base64）。",
		ParamsSchema: obj(map[string]any{"session": str(), "data": str()}, "session", "data"),
	},
	{
		Name: MethodShellResize, Scope: "node", Interactive: true,
		Description:  "调整交互式 shell 会话的终端尺寸。",
		ParamsSchema: obj(map[string]any{"session": str(), "cols": intg(), "rows": intg()}, "session"),
	},
	{
		Name: MethodShellClose, Scope: "node", Interactive: true,
		Description:  "关闭交互式 shell 会话。",
		ParamsSchema: obj(map[string]any{"session": str()}, "session"),
	},
	{
		Name: MethodHostInfo, Scope: "node",
		Description:  "采集被控端主机静态信息（OS、内核、CPU、内存、启动时长）。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodHostMetrics, Scope: "node",
		Description:  "采集一次资源指标（CPU%、内存、负载、网络）。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodHostWatch, Scope: "node",
		Description:  "开启/关闭周期性资源指标推送（监控时序）。",
		ParamsSchema: obj(map[string]any{"interval": intg(), "enable": boolt()}, "enable"),
	},
	{
		Name: MethodProcList, Scope: "node",
		Description:  "列出被控端进程，可按名称过滤。",
		ParamsSchema: obj(map[string]any{"filter": str()}),
	},
	{
		Name: MethodProcSignal, Scope: "node", Dangerous: true,
		Description:  "向被控端进程发送信号（TERM/KILL/INT/HUP）。",
		ParamsSchema: obj(map[string]any{"pid": intg(), "sig": str()}, "pid"),
	},
	{
		Name: MethodFsLs, Scope: "node",
		Description:  "列出被控端目录内容。",
		ParamsSchema: obj(map[string]any{"path": str()}, "path"),
	},
	{
		Name: MethodFsStat, Scope: "node",
		Description:  "获取被控端文件/目录元信息。",
		ParamsSchema: obj(map[string]any{"path": str()}, "path"),
	},
	{
		Name: MethodFsGet, Scope: "node",
		Description:  "读取被控端上的小文件（base64 内联返回）。",
		ParamsSchema: obj(map[string]any{"path": str()}, "path"),
	},
	{
		Name: MethodFsPut, Scope: "node", Dangerous: true,
		Description:  "向被控端写入文件（base64 内联）。",
		ParamsSchema: obj(map[string]any{"path": str(), "data": str(), "mode": intg()}, "path", "data"),
	},
	{
		Name: MethodFsRm, Scope: "node", Dangerous: true,
		Description:  "删除被控端文件/目录。",
		ParamsSchema: obj(map[string]any{"path": str(), "recursive": boolt()}, "path"),
	},
	{
		Name: MethodConfig, Scope: "node",
		Description:  "热更新被控端配置（如别名、心跳间隔）。",
		ParamsSchema: obj(map[string]any{"label": str(), "heartbeat": intg()}),
	},
	{
		Name: MethodReconnect, Scope: "node",
		Description:  "令被控端重建连接。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodShutdown, Scope: "node", Dangerous: true,
		Description:  "令被控端进程退出。",
		ParamsSchema: obj(map[string]any{}),
	},
}

// MethodIndex maps method name to spec.
var MethodIndex = func() map[string]MethodSpec {
	m := map[string]MethodSpec{}
	for _, s := range NodeMethods {
		m[s.Name] = s
	}
	return m
}()
