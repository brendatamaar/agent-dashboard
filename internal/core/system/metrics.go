package system

type AgentUsage struct {
	AgentID     string  `json:"agent_id"`
	RepoID      string  `json:"repo_id"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
	MemoryCap   uint64  `json:"memory_cap"` // e.g. 700 MB for docker runtime
	State       string  `json:"state"`
}

// ResourceUsage contains system telemetry snapshot.
type ResourceUsage struct {
	CPUPercent       float64      `json:"cpu_percent"`
	MemoryUsedBytes  uint64       `json:"memory_used_bytes"`
	MemoryTotalBytes uint64       `json:"memory_total_bytes"`
	MemoryPercent    float64      `json:"memory_percent"`
	DiskUsedBytes    uint64       `json:"disk_used_bytes"`
	DiskTotalBytes   uint64       `json:"disk_total_bytes"`
	DiskPercent      float64      `json:"disk_percent"`
	SwapUsedBytes    uint64       `json:"swap_used_bytes"`
	SwapTotalBytes   uint64       `json:"swap_total_bytes"`
	SwapPercent      float64      `json:"swap_percent"`
	DiskWarning      bool         `json:"disk_warning"`
	Agents           []AgentUsage `json:"agents"`
}

const DiskWarningThresholdPercent = 80.0

// CalculateDiskWarning evaluates if disk usage exceeds the warning threshold (80%).
func CalculateDiskWarning(diskPercent float64) bool {
	return diskPercent >= DiskWarningThresholdPercent
}
