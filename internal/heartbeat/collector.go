package heartbeat

import (
	"os"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// SystemMetrics contains host-level resource usage.
type SystemMetrics struct {
	CPUPercent      float64 `json:"cpu_percent"`
	MemoryPercent   float64 `json:"memory_percent"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes"`
	MemoryUsedBytes  uint64 `json:"memory_used_bytes"`
	DiskPercent     float64 `json:"disk_percent"`
	DiskTotalBytes  uint64  `json:"disk_total_bytes"`
	DiskUsedBytes   uint64  `json:"disk_used_bytes"`
	OS              string  `json:"os"`
	Arch            string  `json:"arch"`
	Hostname        string  `json:"hostname"`
}

// CollectSystemMetrics gathers current CPU, memory, and disk stats.
func CollectSystemMetrics() SystemMetrics {
	m := SystemMetrics{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	if h, err := os.Hostname(); err == nil {
		m.Hostname = h
	}

	// CPU (average across all cores, 0-duration = non-blocking snapshot)
	if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
		m.CPUPercent = percents[0]
	}

	// Memory
	if vm, err := mem.VirtualMemory(); err == nil {
		m.MemoryPercent = vm.UsedPercent
		m.MemoryTotalBytes = vm.Total
		m.MemoryUsedBytes = vm.Used
	}

	// Disk (root partition)
	if d, err := disk.Usage("/"); err == nil {
		m.DiskPercent = d.UsedPercent
		m.DiskTotalBytes = d.Total
		m.DiskUsedBytes = d.Used
	}

	return m
}
