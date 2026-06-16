package main

import (
	"bufio"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
)

// hostInfo collects static host facts from the OS and /proc.
func hostInfo() wire.HostInfoResult {
	r := wire.HostInfoResult{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		CPUCount: runtime.NumCPU(),
		IP:       outboundIP(),
	}
	r.Hostname, _ = os.Hostname()
	r.Kernel = strings.TrimSpace(readFile("/proc/sys/kernel/osrelease"))
	r.CPUModel = cpuModel()
	r.MemTotal = meminfoKB("MemTotal") * 1024
	if f := strings.Fields(readFile("/proc/uptime")); len(f) > 0 {
		r.UptimeSec, _ = strconv.ParseFloat(f[0], 64)
	}
	return r
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// cpuTimes returns (idle, total) jiffies from /proc/stat aggregate line.
func cpuTimes() (idle, total uint64) {
	f := readFile("/proc/stat")
	for _, line := range strings.Split(f, "\n") {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)[1:]
			for i, v := range fields {
				n, _ := strconv.ParseUint(v, 10, 64)
				total += n
				if i == 3 || i == 4 { // idle + iowait
					idle += n
				}
			}
			return
		}
	}
	return
}

func netBytes() (rx, tx uint64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:i])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return
}

// sampleMetrics measures CPU over a short window plus mem/load/net.
func sampleMetrics() wire.Metrics {
	idle0, total0 := cpuTimes()
	time.Sleep(120 * time.Millisecond)
	idle1, total1 := cpuTimes()
	var cpu float64
	if total1 > total0 {
		cpu = (1 - float64(idle1-idle0)/float64(total1-total0)) * 100
	}
	memTotal := meminfoKB("MemTotal") * 1024
	memAvail := meminfoKB("MemAvailable") * 1024
	var memUsed uint64
	if memTotal > memAvail {
		memUsed = memTotal - memAvail
	}
	var load1 float64
	if f := strings.Fields(readFile("/proc/loadavg")); len(f) > 0 {
		load1, _ = strconv.ParseFloat(f[0], 64)
	}
	rx, tx := netBytes()
	return wire.Metrics{
		TS: time.Now().Unix(), CPUPct: round2(cpu),
		MemUsed: memUsed, MemTotal: memTotal, Load1: load1, NetRx: rx, NetTx: tx,
	}
}

func procList(filter string) []wire.ProcInfo {
	out := []wire.ProcInfo{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		status := readFile(filepath.Join("/proc", e.Name(), "status"))
		if status == "" {
			continue
		}
		p := wire.ProcInfo{PID: pid}
		for _, line := range strings.Split(status, "\n") {
			switch {
			case strings.HasPrefix(line, "Name:"):
				p.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			case strings.HasPrefix(line, "PPid:"):
				p.PPID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			case strings.HasPrefix(line, "VmRSS:"):
				f := strings.Fields(line)
				if len(f) >= 2 {
					kb, _ := strconv.ParseUint(f[1], 10, 64)
					p.RSS = kb * 1024
				}
			case strings.HasPrefix(line, "Uid:"):
				f := strings.Fields(line)
				if len(f) >= 2 {
					p.User = uidName(f[1])
				}
			}
		}
		if filter != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(filter)) {
			continue
		}
		out = append(out, p)
	}
	return out
}

var uidCache = map[string]string{}

func uidName(uid string) string {
	if n, ok := uidCache[uid]; ok {
		return n
	}
	name := uid
	if u, err := user.LookupId(uid); err == nil {
		name = u.Username
	}
	uidCache[uid] = name
	return name
}

func meminfoKB(key string) uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, key+":") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				n, _ := strconv.ParseUint(fields[1], 10, 64)
				return n
			}
		}
	}
	return 0
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

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
