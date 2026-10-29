package engine

import "time"

// Fields says which parts of a Sample are present: not every source can
// read every counter (Incus instance state has no disk I/O, VMs without
// incus-agent report no memory…).
type Fields uint8

// Sample fields.
const (
	HasCPU Fields = 1 << iota
	HasMem
	HasNet
	HasBlk
	HasPIDs
)

// Sample is one reading of an instance's resource counters. CPU, network
// and block counters are cumulative (since the instance started); package
// metrics turns two samples into rates and handles counter resets.
type Sample struct {
	InstanceID string // engine-local ID (Instance.ID)
	Time       time.Time
	Has        Fields

	CPU      time.Duration // cumulative CPU time
	CPULimit float64       // cores; 0 = unlimited or unknown
	MemUsed  uint64        // bytes, page cache excluded
	MemLimit uint64        // bytes; 0 = unlimited or unknown

	NetRx, NetTx      uint64 // cumulative bytes
	BlkRead, BlkWrite uint64 // cumulative bytes
	PIDs              uint64

	// Ifaces and Disks are filled by WatchStats only (detail view).
	Ifaces []IfaceCounters // per network interface, sorted by name
	Disks  []DiskUsage     // per disk device (Incus), sorted by name
}

// IfaceCounters are the cumulative counters of one network interface.
type IfaceCounters struct {
	Name   string
	Rx, Tx uint64 // bytes
}

// DiskUsage is the space a disk device uses.
type DiskUsage struct {
	Name string // device name ("root" for the root disk)
	Used uint64 // bytes
}
