package wire

// MethodSpec describes a callable method for capability discovery. ParamsSchema
// and ResultSchema are JSON Schema objects (as Go maps) and double as LLM tool
// definitions.
type MethodSpec struct {
	Name         string         `json:"name"`
	Scope        string         `json:"scope"`  // "node" or "hub"
	Module       string         `json:"module"` // which agent module serves it: core|shell|fs|screen
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
		Name: MethodShellRun, Scope: "node", Module: "shell", Dangerous: true,
		Description: "在被控端执行一条 shell 命令，返回 stdout/stderr/退出码。",
		ParamsSchema: obj(map[string]any{
			"cmd":     str(),
			"cwd":     str(),
			"timeout": intg(),
		}, "cmd"),
		ResultSchema: obj(map[string]any{"stdout": str(), "stderr": str(), "exit": intg()}),
	},
	{
		Name: MethodShellOpen, Scope: "node", Module: "shell", Dangerous: true, Interactive: true,
		Description:  "打开一个交互式 PTY shell 会话（透传系统控制台），输出经 emit 流式推送。",
		ParamsSchema: obj(map[string]any{"shell": str(), "name": str(), "cols": intg(), "rows": intg()}),
	},
	{
		Name: MethodShellInput, Scope: "node", Module: "shell", Dangerous: true, Interactive: true,
		Description:  "向交互式 shell 会话写入输入（base64）。",
		ParamsSchema: obj(map[string]any{"session": str(), "data": str()}, "session", "data"),
	},
	{
		Name: MethodShellResize, Scope: "node", Module: "shell", Interactive: true,
		Description:  "调整交互式 shell 会话的终端尺寸。",
		ParamsSchema: obj(map[string]any{"session": str(), "cols": intg(), "rows": intg()}, "session"),
	},
	{
		Name: MethodShellClose, Scope: "node", Module: "shell", Interactive: true,
		Description:  "关闭交互式 shell 会话。",
		ParamsSchema: obj(map[string]any{"session": str()}, "session"),
	},
	{
		Name: MethodShellList, Scope: "node", Module: "shell",
		Description:  "列出本节点上常驻的交互式 shell 会话（重连后可复用）。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodShellOutput, Scope: "node", Module: "shell",
		Description:  "获取某个常驻 shell 会话的历史输出缓冲（base64，用于重连后回放）。",
		ParamsSchema: obj(map[string]any{"session": str()}, "session"),
	},
	{
		Name: MethodHostInfo, Scope: "node", Module: "core",
		Description:  "采集被控端主机静态信息（OS、内核、CPU、内存、启动时长）。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodHostMetrics, Scope: "node", Module: "core",
		Description:  "采集一次资源指标（CPU%、内存、负载、网络）。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodHostWatch, Scope: "node", Module: "core",
		Description:  "开启/关闭周期性资源指标推送（监控时序）。",
		ParamsSchema: obj(map[string]any{"interval": intg(), "enable": boolt()}, "enable"),
	},
	{
		Name: MethodHostScreenshot, Scope: "node", Module: "screen",
		Description:  "截取被控端屏幕，返回图片（base64）。支持 display 选屏、maxWidth 分辨率缩放、format(jpeg/png) 与 quality 色彩压缩。",
		ParamsSchema: obj(map[string]any{"display": intg(), "maxWidth": intg(), "format": str(), "quality": intg()}),
		ResultSchema: obj(map[string]any{"format": str(), "width": intg(), "height": intg(), "size": intg(), "data": str(), "tool": str()}),
	},
	{
		Name: MethodProcList, Scope: "node", Module: "core",
		Description:  "列出被控端进程，可按名称过滤。",
		ParamsSchema: obj(map[string]any{"filter": str()}),
	},
	{
		Name: MethodProcSignal, Scope: "node", Module: "core", Dangerous: true,
		Description:  "向被控端进程发送信号（TERM/KILL/INT/HUP）。",
		ParamsSchema: obj(map[string]any{"pid": intg(), "sig": str()}, "pid"),
	},
	{
		Name: MethodFsLs, Scope: "node", Module: "fs",
		Description:  "列出被控端目录内容。",
		ParamsSchema: obj(map[string]any{"path": str()}, "path"),
	},
	{
		Name: MethodFsStat, Scope: "node", Module: "fs",
		Description:  "获取被控端文件/目录元信息。",
		ParamsSchema: obj(map[string]any{"path": str()}, "path"),
	},
	{
		Name: MethodFsGet, Scope: "node", Module: "fs",
		Description:  "读取被控端上的小文件（base64 内联返回）。",
		ParamsSchema: obj(map[string]any{"path": str()}, "path"),
	},
	{
		Name: MethodFsPut, Scope: "node", Module: "fs", Dangerous: true,
		Description:  "向被控端写入文件（base64 内联；支持分片 offset 上传以显示进度）。",
		ParamsSchema: obj(map[string]any{"path": str(), "data": str(), "mode": intg(), "offset": intg()}, "path", "data"),
	},
	{
		Name: MethodFsRm, Scope: "node", Module: "fs", Dangerous: true,
		Description:  "删除被控端文件/目录。",
		ParamsSchema: obj(map[string]any{"path": str(), "recursive": boolt()}, "path"),
	},
	{
		Name: MethodConfig, Scope: "node", Module: "core",
		Description:  "热更新被控端配置（如别名、心跳间隔）。",
		ParamsSchema: obj(map[string]any{"label": str(), "heartbeat": intg()}),
	},
	{
		Name: MethodReconnect, Scope: "node", Module: "core",
		Description:  "令被控端重建连接。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodShutdown, Scope: "node", Module: "core", Dangerous: true,
		Description:  "令被控端进程退出。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodTaskStart, Scope: "node", Module: "shell", Dangerous: true, Interactive: true,
		Description:  "拉起一个长期托管子进程（PTY，ANSI 输出 + 可交互），输出经 emit 实时推送。",
		ParamsSchema: obj(map[string]any{"name": str(), "cmd": str(), "cwd": str(), "cols": intg(), "rows": intg()}, "cmd"),
	},
	{
		Name: MethodTaskInput, Scope: "node", Module: "shell", Dangerous: true, Interactive: true,
		Description:  "向托管子进程写入 stdin（base64）。",
		ParamsSchema: obj(map[string]any{"task": str(), "data": str()}, "task", "data"),
	},
	{
		Name: MethodTaskResize, Scope: "node", Module: "shell", Interactive: true,
		Description:  "调整托管子进程的 PTY 尺寸。",
		ParamsSchema: obj(map[string]any{"task": str(), "cols": intg(), "rows": intg()}, "task"),
	},
	{
		Name: MethodTaskSignal, Scope: "node", Module: "shell", Dangerous: true,
		Description:  "停止托管子进程（TERM/KILL）。",
		ParamsSchema: obj(map[string]any{"task": str(), "sig": str()}, "task"),
	},
	{
		Name: MethodTaskList, Scope: "node", Module: "shell",
		Description:  "列出本节点的托管子进程（含运行中与已退出的历史）。",
		ParamsSchema: obj(map[string]any{}),
	},
	{
		Name: MethodTaskOutput, Scope: "node", Module: "shell",
		Description:  "获取托管子进程的历史输出缓冲（base64）。",
		ParamsSchema: obj(map[string]any{"task": str()}, "task"),
	},
	{
		Name: MethodTaskRestart, Scope: "node", Module: "shell", Dangerous: true,
		Description:  "用相同命令重启托管子进程。",
		ParamsSchema: obj(map[string]any{"task": str()}, "task"),
	},
	{
		Name: MethodTaskRemove, Scope: "node", Module: "shell",
		Description:  "移除一个托管子进程记录（运行中会先停止）。",
		ParamsSchema: obj(map[string]any{"task": str()}, "task"),
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

// MethodModule maps a method name to its owning module (core|shell|fs|screen).
// Used by the hub to route a dispatched call to the connection that serves it.
func MethodModule(method string) string {
	if sp, ok := MethodIndex[method]; ok && sp.Module != "" {
		return sp.Module
	}
	return "core"
}

// Modules is the canonical ordered list of agent modules.
var Modules = []string{"core", "shell", "fs", "screen"}

// ModuleMethods returns the method names a given module serves.
func ModuleMethods(module string) []string {
	out := []string{}
	for _, sp := range NodeMethods {
		if sp.Module == module {
			out = append(out, sp.Name)
		}
	}
	return out
}
