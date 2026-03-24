package heartbeat

import (
	"runtime"
	"testing"
)

func TestCollectSystemMetrics(t *testing.T) {
	m := CollectSystemMetrics()

	if m.OS != runtime.GOOS {
		t.Errorf("expected OS %s, got %s", runtime.GOOS, m.OS)
	}
	if m.Arch != runtime.GOARCH {
		t.Errorf("expected Arch %s, got %s", runtime.GOARCH, m.Arch)
	}
	if m.Hostname == "" {
		t.Error("hostname should not be empty")
	}

	// CPU percent should be in valid range (0 may happen on first call)
	if m.CPUPercent < 0 || m.CPUPercent > 100 {
		t.Errorf("cpu percent out of range: %f", m.CPUPercent)
	}

	// Memory should be non-zero on any real system
	if m.MemoryTotalBytes == 0 {
		t.Error("memory total should be non-zero")
	}
	if m.MemoryUsedBytes == 0 {
		t.Error("memory used should be non-zero")
	}
	if m.MemoryPercent < 0 || m.MemoryPercent > 100 {
		t.Errorf("memory percent out of range: %f", m.MemoryPercent)
	}

	// Disk should be non-zero
	if m.DiskTotalBytes == 0 {
		t.Error("disk total should be non-zero")
	}
	if m.DiskPercent < 0 || m.DiskPercent > 100 {
		t.Errorf("disk percent out of range: %f", m.DiskPercent)
	}
}
