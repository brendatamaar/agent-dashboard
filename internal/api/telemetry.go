package api

import (
	"net/http"
	"runtime"

	"agent-dashboard/internal/core/system"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

type TelemetryHandler struct {
	dataDir string
}

func NewTelemetryHandler(dataDir string) *TelemetryHandler {
	return &TelemetryHandler{dataDir: dataDir}
}

func (t *TelemetryHandler) HandleGetSystem(w http.ResponseWriter, r *http.Request) {
	var usage system.ResourceUsage

	// CPU
	cpuPercentages, err := cpu.Percent(0, false)
	if err == nil && len(cpuPercentages) > 0 {
		usage.CPUPercent = cpuPercentages[0]
	}

	// Memory & Swap
	vMem, err := mem.VirtualMemory()
	if err == nil {
		usage.MemoryUsedBytes = vMem.Used
		usage.MemoryTotalBytes = vMem.Total
		usage.MemoryPercent = vMem.UsedPercent
	}

	swapMem, err := mem.SwapMemory()
	if err == nil {
		usage.SwapUsedBytes = swapMem.Used
		usage.SwapTotalBytes = swapMem.Total
		usage.SwapPercent = swapMem.UsedPercent
	}

	// Disk
	targetPath := "/"
	if runtime.GOOS == "windows" {
		targetPath = "."
	}
	dUsage, err := disk.Usage(targetPath)
	if err == nil {
		usage.DiskUsedBytes = dUsage.Used
		usage.DiskTotalBytes = dUsage.Total
		usage.DiskPercent = dUsage.UsedPercent
	}

	usage.DiskWarning = system.CalculateDiskWarning(usage.DiskPercent)

	writeJSON(w, http.StatusOK, usage)
}
