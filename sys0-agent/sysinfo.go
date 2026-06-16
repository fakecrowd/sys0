package main

import (
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/shirou/gopsutil/v4/cpu"
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
	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemTotal = vm.Total
		m.MemUsed = vm.Used
	}
	if la, err := load.Avg(); err == nil {
		m.Load1 = round2(la.Load1)
	}
	if io, err := gnet.IOCounters(false); err == nil && len(io) > 0 {
		m.NetRx = io[0].BytesRecv
		m.NetTx = io[0].BytesSent
	}
	return m
}

// procList enumerates processes (cross-platform via gopsutil).
func procList(filter string) []wire.ProcInfo {
	out := []wire.ProcInfo{}
	procs, err := process.Processes()
	if err != nil {
		return out
	}
	lf := strings.ToLower(filter)
	for _, p := range procs {
		name, _ := p.Name()
		if filter != "" && !strings.Contains(strings.ToLower(name), lf) {
			continue
		}
		pi := wire.ProcInfo{PID: int(p.Pid), Name: name}
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
