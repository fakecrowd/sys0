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
	MethodShellOpen   = "shell.open" // interactive PTY session
	MethodShellInput  = "shell.input"
	MethodShellResize = "shell.resize"
	MethodShellClose  = "shell.close"
	MethodShellList   = "shell.list"   // list persistent PTY shells on the node
	MethodShellOutput = "shell.output" // fetch a shell's buffered scrollback
	MethodHostInfo    = "host.info"
	MethodHostMetrics = "host.metrics"
	MethodHostWatch   = "host.watch"
	MethodHostScreenshot = "host.screenshot"
	MethodProcList    = "proc.list"
	MethodProcSignal  = "proc.signal"
	MethodFsLs        = "fs.ls"
	MethodFsStat      = "fs.stat"
	MethodFsGet       = "fs.get"
	MethodFsPut       = "fs.put"
	MethodFsRm        = "fs.rm"

	// managed processes (long-running supervised child processes)
	MethodTaskStart   = "task.start"
	MethodTaskInput   = "task.input"
	MethodTaskResize  = "task.resize"
	MethodTaskSignal  = "task.signal"
	MethodTaskList    = "task.list"
	MethodTaskOutput  = "task.output"
	MethodTaskRestart = "task.restart"
	MethodTaskRemove  = "task.remove"
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
	Cwd          string      `json:"cwd"` // agent's working directory
	Pid          int         `json:"pid"` // agent's own pid
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

// interactive PTY shell

type ShellOpenParams struct {
	Shell string `json:"shell,omitempty"` // bash, sh; default auto
	Name  string `json:"name,omitempty"`  // optional friendly label for the tab
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

type ShellOpenResult struct {
	Session string `json:"session"`
}

// ShellInfo describes a persistent interactive shell living on the agent.
type ShellInfo struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	Shell   string `json:"shell"`
	State   string `json:"state"` // running | exited
	Exit    int    `json:"exit"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Started int64  `json:"started"`
}

type ShellListResult struct {
	Sessions []ShellInfo `json:"sessions"`
}

type ShellRefParams struct {
	Session string `json:"session"`
}

type ShellOutputResult struct {
	Session string `json:"session"`
	Data    string `json:"data"` // base64 of the recent output ring buffer
	State   string `json:"state"`
	Exit    int    `json:"exit"`
}

type ShellInputParams struct {
	Session string `json:"session"`
	Data    string `json:"data"` // base64 of raw bytes to stdin
}

type ShellResizeParams struct {
	Session string `json:"session"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

type ShellCloseParams struct {
	Session string `json:"session"`
}

// ---- managed processes ----

type TaskStartParams struct {
	Name string `json:"name,omitempty"`
	Cmd  string `json:"cmd"`
	Cwd  string `json:"cwd,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type TaskStartResult struct {
	Task string `json:"task"`
}

type TaskInputParams struct {
	Task string `json:"task"`
	Data string `json:"data"` // base64 to stdin
}

type TaskResizeParams struct {
	Task string `json:"task"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type TaskSignalParams struct {
	Task string `json:"task"`
	Sig  string `json:"sig"` // TERM | KILL
}

type TaskRefParams struct {
	Task string `json:"task"`
}

// TaskOutputResult returns the buffered terminal output (history) of a task.
type TaskOutputResult struct {
	Task  string `json:"task"`
	Data  string `json:"data"` // base64 of recent output buffer
	State string `json:"state"`
	Exit  int    `json:"exit"`
}

type TaskInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Cmd      string `json:"cmd"`
	State    string `json:"state"` // running | exited
	PID      int    `json:"pid"`
	Exit     int    `json:"exit"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished"`
}

type TaskListResult struct {
	Tasks []TaskInfo `json:"tasks"`
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
	Cwd       string  `json:"cwd"`
	Pid       int     `json:"pid"`
}

type Metrics struct {
	TS        int64     `json:"ts"`
	CPUPct    float64   `json:"cpuPct"`
	CPUCores  []float64 `json:"cpuCores,omitempty"` // per-core utilisation %
	MemUsed   uint64    `json:"memUsed"`
	MemTotal  uint64    `json:"memTotal"`
	SwapUsed  uint64    `json:"swapUsed"`
	SwapTotal uint64    `json:"swapTotal"`
	Load1     float64   `json:"load1"`
	Load5     float64   `json:"load5"`
	Load15    float64   `json:"load15"`
	NetRx     uint64    `json:"netRx"` // cumulative bytes
	NetTx     uint64    `json:"netTx"`
	DiskUsed  uint64    `json:"diskUsed"`  // root/system volume
	DiskTotal uint64    `json:"diskTotal"`
	Procs     int       `json:"procs"`     // process count
	UptimeSec uint64    `json:"uptimeSec"`
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
	Self bool   `json:"self,omitempty"` // the sys0-agent's own process (survives disguise rename)
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

// ScreenshotParams requests a screen capture from the node. MaxWidth scales the
// image down preserving aspect ratio (0 = native resolution). Format is
// "jpeg" (default) or "png"; Quality is the JPEG quality 1..100 (color
// compression, default 80) and is ignored for png. Display selects which
// monitor (0-based; <0 or out-of-range = primary/full virtual desktop).
type ScreenshotParams struct {
	Display  int    `json:"display,omitempty"`
	MaxWidth int    `json:"maxWidth,omitempty"`
	Format   string `json:"format,omitempty"`
	Quality  int    `json:"quality,omitempty"`
}

// ScreenshotResult carries the encoded image inline (base64). Width/Height are
// the FINAL (possibly downscaled) pixel dimensions; Format echoes what was
// encoded so the console can build the right data: URL.
type ScreenshotResult struct {
	Format string `json:"format"` // jpeg | png
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"size"`    // encoded byte length
	Data   string `json:"data"`    // base64 of the encoded image
	Tool   string `json:"tool"`    // capture backend used (for diagnostics)
}

// FsPutParams writes Data (base64) to Path at byte Offset. Chunked uploads send
// the file as a sequence of FsPut calls with increasing Offset so the console
// can show progress; the first chunk (Offset 0) creates/truncates the file and
// later chunks append at their offset. A single-shot upload simply uses Offset 0
// with the whole file (backward compatible). Mode applies on the first chunk.
type FsPutParams struct {
	Path   string `json:"path"`
	Data   string `json:"data"` // base64 (this chunk)
	Mode   uint32 `json:"mode,omitempty"`
	Offset int64  `json:"offset,omitempty"` // byte offset to write this chunk at
}

// FsPutResult reports the cumulative file size after this chunk landed, so the
// console can verify the upload and drive a progress indicator.
type FsPutResult struct {
	OK      bool  `json:"ok"`
	Path    string `json:"path,omitempty"`
	Written int   `json:"written"` // bytes written by this chunk
	Size    int64 `json:"size"`    // total file size on disk after this chunk
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
