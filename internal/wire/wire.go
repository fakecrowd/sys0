// Package wire defines the on-the-wire method names, parameter/result types and
// the dispatch envelope shared by the agent, hub and clients.
package wire

import "encoding/json"

// Method names.
const (
	// node lifecycle (agent <-> hub)
	MethodHello     = "node.hello"
	MethodPing      = "node.ping"
	MethodEmit      = "emit" // notification: streaming output from a node
	MethodConfig    = "node.config"
	MethodReconnect = "node.reconnect"
	MethodShutdown  = "node.shutdown"

	// node capabilities (invoked via dispatch)
	MethodShellRun    = "shell.run"
	MethodHostInfo    = "host.info"
	MethodHostMetrics = "host.metrics"
	MethodHostWatch   = "host.watch"
	MethodProcList    = "proc.list"
	MethodProcSignal  = "proc.signal"
	MethodFsLs        = "fs.ls"
	MethodFsStat      = "fs.stat"
	MethodFsGet       = "fs.get"
	MethodFsPut       = "fs.put"
	MethodFsRm        = "fs.rm"
)

// ---- handshake ----

type HostSummary struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Kernel string `json:"kernel"`
	IP     string `json:"ip"`
}

type Hello struct {
	Key          string      `json:"key"`
	Fingerprint  string      `json:"fingerprint"`
	Label        string      `json:"label"`
	Host         HostSummary `json:"host"`
	AgentVersion string      `json:"agentVersion"`
	Capabilities []string    `json:"capabilities"`
}

type Welcome struct {
	NodeID    string `json:"nodeId"`
	Heartbeat int    `json:"heartbeat"` // seconds
}

// ---- shell ----

type ShellRunParams struct {
	Cmd     string `json:"cmd"`
	Cwd     string `json:"cwd,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // seconds, 0 = default
}

type ShellRunResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Exit   int    `json:"exit"`
}

// ---- host ----

type HostInfoResult struct {
	Hostname  string  `json:"hostname"`
	OS        string  `json:"os"`
	Arch      string  `json:"arch"`
	Kernel    string  `json:"kernel"`
	CPUModel  string  `json:"cpuModel"`
	CPUCount  int     `json:"cpuCount"`
	MemTotal  uint64  `json:"memTotal"`
	UptimeSec float64 `json:"uptimeSec"`
	IP        string  `json:"ip"`
}

type Metrics struct {
	TS       int64   `json:"ts"`
	CPUPct   float64 `json:"cpuPct"`
	MemUsed  uint64  `json:"memUsed"`
	MemTotal uint64  `json:"memTotal"`
	Load1    float64 `json:"load1"`
	NetRx    uint64  `json:"netRx"`
	NetTx    uint64  `json:"netTx"`
}

type HostWatchParams struct {
	Interval int  `json:"interval"` // seconds
	Enable   bool `json:"enable"`
}

// ---- proc ----

type ProcListParams struct {
	Filter string `json:"filter,omitempty"`
}

type ProcInfo struct {
	PID  int    `json:"pid"`
	PPID int    `json:"ppid"`
	User string `json:"user"`
	RSS  uint64 `json:"rss"`
	Name string `json:"name"`
}

type ProcListResult struct {
	Procs []ProcInfo `json:"procs"`
}

type ProcSignalParams struct {
	PID int    `json:"pid"`
	Sig string `json:"sig"` // TERM, KILL, INT, HUP
}

// ---- fs ----

type FsLsParams struct {
	Path string `json:"path"`
}

type FsEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	IsDir bool   `json:"isDir"`
	MTime int64  `json:"mtime"`
}

type FsLsResult struct {
	Path    string    `json:"path"`
	Entries []FsEntry `json:"entries"`
}

type FsStatResult struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	IsDir bool   `json:"isDir"`
	MTime int64  `json:"mtime"`
}

type FsGetParams struct {
	Path string `json:"path"`
}

// FsGetResult carries small files inline (base64). Large transfers would use a
// dedicated chunked channel; the inline form keeps the demo self-contained.
type FsGetResult struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Data string `json:"data"` // base64
}

type FsPutParams struct {
	Path string `json:"path"`
	Data string `json:"data"` // base64
	Mode uint32 `json:"mode,omitempty"`
}

type FsRmParams struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

type OKResult struct {
	OK bool `json:"ok"`
}

// ---- emit (streaming) ----

type EmitParams struct {
	Ref  string          `json:"ref"`  // request id this stream belongs to
	Chan string          `json:"chan"` // stdout, stderr, metrics
	Seq  int             `json:"seq"`
	Data json.RawMessage `json:"data"`
}

// ---- dispatch envelope (client/api -> hub) ----

type Select struct {
	Nodes []string `json:"nodes,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	All   bool     `json:"all,omitempty"`
}

type Call struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type DispatchParams struct {
	Select  Select `json:"select"`
	Call    Call   `json:"call"`
	DryRun  bool   `json:"dryRun,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type DispatchItem struct {
	Node  string          `json:"node"`
	OK    bool            `json:"ok"`
	Value json.RawMessage `json:"value,omitempty"`
	Error *DispatchError  `json:"error,omitempty"`
}

type DispatchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type DispatchResult struct {
	Items []DispatchItem `json:"items"`
}
