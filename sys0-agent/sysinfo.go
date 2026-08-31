package main

import (
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// hostInfo collects static host facts (cross-platform via gopsutil).
func hostInfo() wire.HostInfoResult {
	r := wire.HostInfoResult{
		OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPUCount: runtime.NumCPU(), IP: outboundIP(),
	}
	r.Hostname, _ = os.Hostname()
	r.Cwd, _ = os.Getwd()
	r.Pid = os.Getpid()
	if h, err := host.Info(); err == nil {
		r.Kernel = h.KernelVersion
		r.UptimeSec = float64(h.Uptime)
		if r.Hostname == "" {
			r.Hostname = h.Hostname
		}
	}
	if ci, err := cpu.Info(); err == nil && len(ci) > 0 {
		r.CPUModel = ci[0].ModelName
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		r.MemTotal = vm.Total
	}
	return r
}

// sampleMetrics measures one resource snapshot (cross-platform via gopsutil).
func sampleMetrics() wire.Metrics {
	m := wire.Metrics{TS: time.Now().Unix()}
	if pct, err := cpu.Percent(120*time.Millisecond, false); err == nil && len(pct) > 0 {
		m.CPUPct = round2(pct[0])
	}
	if pc, err := cpu.Percent(0, true); err == nil {
		m.CPUCores = make([]float64, len(pc))
		for i, v := range pc {
			m.CPUCores[i] = round2(v)
		}
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemTotal = vm.Total
		m.MemUsed = vm.Used
	}
	if sw, err := mem.SwapMemory(); err == nil {
		m.SwapTotal = sw.Total
		m.SwapUsed = sw.Used
	}
	if la, err := load.Avg(); err == nil {
		m.Load1 = round2(la.Load1)
		m.Load5 = round2(la.Load5)
		m.Load15 = round2(la.Load15)
	}
	if io, err := gnet.IOCounters(false); err == nil && len(io) > 0 {
		m.NetRx = io[0].BytesRecv
		m.NetTx = io[0].BytesSent
	}
	if du, err := disk.Usage(rootMount()); err == nil {
		m.DiskUsed = du.Used
		m.DiskTotal = du.Total
	}
	if procs, err := process.Pids(); err == nil {
		m.Procs = len(procs)
	}
	if h, err := host.Uptime(); err == nil {
		m.UptimeSec = h
	}
	return m
}

// rootMount returns the path whose disk usage best represents the system
// volume: C:\ on Windows, / elsewhere.
func rootMount() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	}
	return "/"
}

// procList enumerates processes (cross-platform via gopsutil).
func procList(filter string) []wire.ProcInfo {
	out := []wire.ProcInfo{}
	procs, err := process.Processes()
	if err != nil {
		return out
	}
	lf := strings.ToLower(filter)
	self := os.Getpid()
	for _, p := range procs {
		name, _ := p.Name()
		if filter != "" && !strings.Contains(strings.ToLower(name), lf) {
			continue
		}
		pi := wire.ProcInfo{PID: int(p.Pid), Name: name}
		if int(p.Pid) == self {
			pi.Self = true
		}
		if ppid, err := p.Ppid(); err == nil {
			pi.PPID = int(ppid)
		}
		if u, err := p.Username(); err == nil {
			pi.User = u
		}
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			pi.RSS = mi.RSS
		}
		out = append(out, pi)
	}
	return out
}

func outboundIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
