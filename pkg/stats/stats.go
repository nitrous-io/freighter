package stats

import (
	"time"

	"github.com/shirou/gopsutil"
)

type Stats struct {
	// Uptime in seconds.
	Uptime uint64 `json:"uptime"`

	// MemoryUsage in bytes.
	MemoryUsage uint64 `json:"memory_usage"`
	// MemoryTotal in bytes.
	MemoryTotal uint64 `json:"memory_total"`

	// StorageUsage in bytes.
	StorageUsage uint64 `json:"storage_usage"`
	// StorageTotal in bytes.
	StorageTotal uint64 `json:"storage_total"`

	NumCPU int `json:"num_cpu"`
	// Percent usage per CPU.
	PerCPUUsage []float64 `json:"per_cpu_usage"`
}

var Sample = func(period time.Duration) (*Stats, error) {
	stats := &Stats{}

	if h, err := gopsutil.HostInfo(); err == nil {
		stats.Uptime = h.Uptime
	}

	if mem, err := gopsutil.VirtualMemory(); err == nil {
		stats.MemoryUsage = mem.Used
		stats.MemoryTotal = mem.Total
	}

	// Assume root volume is the volume made available to users.
	if d, err := gopsutil.DiskUsage("/"); err == nil {
		stats.StorageUsage = d.Used * 1024 // Convert kb to bytes.
		stats.StorageTotal = d.Total * 1024
	}

	errch := make(chan error)
	usagech := make(chan []float64)
	go func() {
		usage, err := cpuUsage(period)
		if err != nil {
			errch <- err
			return
		}
		usagech <- usage
	}()

	select {
	case usage := <-usagech:
		stats.NumCPU = len(usage)
		stats.PerCPUUsage = usage
	case err := <-errch:
		return nil, err
	}

	return stats, nil
}

type cpuSample struct {
	idle  float64
	total float64
}

// sampleCPU gets the idle and total ticks for each CPU.
// This parses the /proc/stat file (documented here: https://www.kernel.org/doc/Documentation/filesystems/proc.txt).
func sampleCPU() ([]cpuSample, error) {
	cputimes, err := gopsutil.CPUTimes(true)
	if err != nil {
		return nil, err
	}
	samples := []cpuSample{}
	for _, c := range cputimes {
		if c.CPU == "cpu-total" { // Linux-only: ignore combined CPU times.
			continue
		}
		s := cpuSample{}
		s.total = float64(c.User) +
			float64(c.System) +
			float64(c.Idle) +
			float64(c.Nice) +
			float64(c.Iowait) +
			float64(c.Irq) +
			float64(c.Softirq) +
			float64(c.Steal) +
			float64(c.Guest) +
			float64(c.GuestNice) +
			float64(c.Stolen)
		s.idle = float64(c.Idle) + float64(c.Iowait)
		samples = append(samples, s)
	}
	return samples, nil
}

// cpuUsage computes per-CPU usage over the given period as percentages.
func cpuUsage(period time.Duration) ([]float64, error) {
	start, err := sampleCPU()
	if err != nil {
		return nil, err
	}
	time.Sleep(period)
	end, err := sampleCPU()
	if err != nil {
		return nil, err
	}
	usage := make([]float64, len(start))
	for i := range start {
		idleTicks := float64(end[i].idle - start[i].idle)
		totalTicks := float64(end[i].total - start[i].total)
		usage[i] = (totalTicks - idleTicks) * 100 / totalTicks
	}
	return usage, nil
}
