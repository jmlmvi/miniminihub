package worker

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
)

// V002 P1 — remontée de charge de l'agent (léger, sans dépendance : /proc + statfs).

// ---- jauge des connexions d'égress ACTIVES (mémoire, partagée proxy<->tunnel,
//
//	même package `worker`) ----
var (
	activeProxy    int64
	activeProxyTor int64
)

// egressBegin/egressEnd encadrent une connexion d'égress pour tenir la jauge par service.
func egressBegin(viaTor bool) {
	if viaTor {
		atomic.AddInt64(&activeProxyTor, 1)
	} else {
		atomic.AddInt64(&activeProxy, 1)
	}
}

func egressEnd(viaTor bool) {
	if viaTor {
		atomic.AddInt64(&activeProxyTor, -1)
	} else {
		atomic.AddInt64(&activeProxy, -1)
	}
}

// connsByService renvoie les conns actives par service (n'inclut que les non-nuls).
func connsByService() map[string]int32 {
	m := map[string]int32{}
	if v := atomic.LoadInt64(&activeProxy); v > 0 {
		m["proxy"] = int32(v)
	}
	if v := atomic.LoadInt64(&activeProxyTor); v > 0 {
		m["proxy-tor"] = int32(v)
	}
	return m
}

// ---- charge système ----

type cpuSample struct{ total, idle uint64 }

// readCPUSample lit la ligne agrégée "cpu" de /proc/stat.
func readCPUSample() (cpuSample, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return cpuSample{}, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, false
	}
	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			continue
		}
		total += v
		if i == 4 || i == 5 { // idle + iowait
			idle += v
		}
	}
	return cpuSample{total: total, idle: idle}, true
}

// cpuPct calcule le %CPU entre deux échantillons /proc/stat.
func cpuPct(prev, cur cpuSample) (int32, bool) {
	dt := cur.total - prev.total
	di := cur.idle - prev.idle
	if dt == 0 || cur.total < prev.total {
		return 0, false
	}
	return clampPct(float64(dt-di) / float64(dt) * 100), true
}

// memPct : 1 - MemAvailable/MemTotal (/proc/meminfo).
func memPct() int32 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	var total, avail uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = parseMeminfo(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			avail = parseMeminfo(line)
		}
	}
	if total == 0 {
		return 0
	}
	return clampPct(float64(total-avail) / float64(total) * 100)
}

func parseMeminfo(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

// diskPct : part utilisée du système de fichiers contenant path (statfs).
func diskPct(path string) int32 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return 0
	}
	used := st.Blocks - st.Bavail
	return clampPct(float64(used) / float64(st.Blocks) * 100)
}

func clampPct(p float64) int32 {
	switch {
	case p < 0:
		return 0
	case p > 100:
		return 100
	default:
		return int32(p + 0.5)
	}
}
