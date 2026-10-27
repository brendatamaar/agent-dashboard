package api

import (
	"context"
	"net/http"
	"runtime"

	"agent-dashboard/internal/core/agent"
	"agent-dashboard/internal/core/system"
	rt "agent-dashboard/internal/runtime"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

type AgentStatsProvider interface {
	ListAgents() []agent.Agent
	Stats(ctx context.Context, agentID string) (rt.Usage, error)
}

type TelemetryHandler struct {
	dataDir string
	stats   AgentStatsProvider
}

func NewTelemetryHandler(dataDir string, stats ...AgentStatsProvider) *TelemetryHandler {
	var sp AgentStatsProvider
	if len(stats) > 0 {
		sp = stats[0]
	}
	return &TelemetryHandler{dataDir: dataDir, stats: sp}
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

	usage.Agents = make([]system.AgentUsage, 0)
	if t.stats != nil {
		for _, a := range t.stats.ListAgents() {
			au := system.AgentUsage{
				AgentID:   a.ID,
				RepoID:    a.RepoID,
				State:     string(a.State),
				MemoryCap: 700 * 1024 * 1024,
			}
			if agent.IsActive(a.State) {
				if st, err := t.stats.Stats(r.Context(), a.ID); err == nil {
					au.CPUPercent = st.CPUPercent
					au.MemoryBytes = st.MemoryBytes
				}
			}
			usage.Agents = append(usage.Agents, au)
		}
	}

	writeJSON(w, http.StatusOK, usage)
}
