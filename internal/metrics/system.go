package metrics

import (
	"context"
	"math"
	"os"
	"runtime"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// SystemMetrics encapsulates all system-level observables.
//
// It maintains OpenTelemetry gauges and counters for every supported
// metric category, as well as internal state for delta calculation
// on counters (disk I/O and network).
type SystemMetrics struct {
	// CPU metrics
	cpuUsagePercent metric.Float64ObservableGauge
	cpuUsagePerCore metric.Float64ObservableGauge
	cpuLoadAvg1     metric.Float64ObservableGauge
	cpuLoadAvg5     metric.Float64ObservableGauge
	cpuLoadAvg15    metric.Float64ObservableGauge
	cpuCores        metric.Int64ObservableGauge
	cpuThreads      metric.Int64ObservableGauge

	// Memory metrics
	memTotal       metric.Int64ObservableGauge
	memAvailable   metric.Int64ObservableGauge
	memUsed        metric.Int64ObservableGauge
	memFree        metric.Int64ObservableGauge
	memUsedPercent metric.Float64ObservableGauge
	memCached      metric.Int64ObservableGauge
	memBuffers     metric.Int64ObservableGauge

	// Swap metrics
	swapTotal       metric.Int64ObservableGauge
	swapUsed        metric.Int64ObservableGauge
	swapFree        metric.Int64ObservableGauge
	swapUsedPercent metric.Float64ObservableGauge

	// Disk metrics
	diskUsagePercent metric.Float64ObservableGauge
	diskUsedBytes    metric.Int64ObservableGauge
	diskFreeBytes    metric.Int64ObservableGauge
	diskTotalBytes   metric.Int64ObservableGauge
	diskReadBytes    metric.Int64ObservableCounter
	diskWriteBytes   metric.Int64ObservableCounter
	diskReadOps      metric.Int64ObservableCounter
	diskWriteOps     metric.Int64ObservableCounter

	// Network metrics
	netBytesSent   metric.Int64ObservableCounter
	netBytesRecv   metric.Int64ObservableCounter
	netPacketsSent metric.Int64ObservableCounter
	netPacketsRecv metric.Int64ObservableCounter
	netErrorsIn    metric.Int64ObservableCounter
	netErrorsOut   metric.Int64ObservableCounter
	netDropsIn     metric.Int64ObservableCounter
	netDropsOut    metric.Int64ObservableCounter

	// Process metrics
	processCount metric.Int64ObservableGauge
	fdCount      metric.Int64ObservableGauge

	// System info
	uptimeSeconds metric.Int64ObservableGauge

	// Track previous values for counters (deltas)
	lastDiskStats map[string]disk.IOCountersStat
	lastNetStats  map[string]net.IOCountersStat

	// Configuration
	diskPath string
}

// SystemMetricsConfig configures which system metrics to monitor.
//
// DiskPath specifies the mount point for disk metrics (default: "/").
type SystemMetricsConfig struct {
	DiskPath string // Path to monitor disk usage (default: "/")
}

// NewSystemMetrics initializes a SystemMetrics instance and registers all
// OpenTelemetry observables with the provided meter.
//
// All metrics are automatically updated via a callback. This function
// returns a fully initialized SystemMetrics ready for integration with
// monitoring pipelines.
//
// Example usage:
//
//	meter := otel.Meter("example")
//	sm, err := NewSystemMetrics(meter, SystemMetricsConfig{DiskPath: "/"})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Notes:
// - Disk and network counters use delta calculations between observations.
// - Metrics are safe for concurrent collection.
func NewSystemMetrics(meter metric.Meter, config SystemMetricsConfig) (*SystemMetrics, error) {
	if config.DiskPath == "" {
		config.DiskPath = "/"
	}

	sm := &SystemMetrics{
		diskPath:      config.DiskPath,
		lastDiskStats: make(map[string]disk.IOCountersStat),
		lastNetStats:  make(map[string]net.IOCountersStat),
	}

	var err error

	// CPU metrics
	sm.cpuUsagePercent, err = meter.Float64ObservableGauge(
		"system.cpu.utilization",
		metric.WithDescription("CPU utilization percentage"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.cpuUsagePerCore, err = meter.Float64ObservableGauge(
		"system.cpu.utilization.per_core",
		metric.WithDescription("CPU utilization percentage per core"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.cpuLoadAvg1, err = meter.Float64ObservableGauge(
		"system.cpu.load_average.1m",
		metric.WithDescription("System load average over 1 minute"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.cpuLoadAvg5, err = meter.Float64ObservableGauge(
		"system.cpu.load_average.5m",
		metric.WithDescription("System load average over 5 minutes"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.cpuLoadAvg15, err = meter.Float64ObservableGauge(
		"system.cpu.load_average.15m",
		metric.WithDescription("System load average over 15 minutes"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.cpuCores, err = meter.Int64ObservableGauge(
		"system.cpu.physical_count",
		metric.WithDescription("Number of physical CPU cores"),
		metric.WithUnit("{core}"),
	)
	if err != nil {
		return nil, err
	}

	sm.cpuThreads, err = meter.Int64ObservableGauge(
		"system.cpu.logical_count",
		metric.WithDescription("Number of logical CPU cores"),
		metric.WithUnit("{thread}"),
	)
	if err != nil {
		return nil, err
	}

	// Memory metrics
	sm.memTotal, err = meter.Int64ObservableGauge(
		"system.memory.total",
		metric.WithDescription("Total system memory"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.memAvailable, err = meter.Int64ObservableGauge(
		"system.memory.available",
		metric.WithDescription("Available system memory"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.memUsed, err = meter.Int64ObservableGauge(
		"system.memory.used",
		metric.WithDescription("Used system memory"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.memFree, err = meter.Int64ObservableGauge(
		"system.memory.free",
		metric.WithDescription("Free system memory"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.memUsedPercent, err = meter.Float64ObservableGauge(
		"system.memory.utilization",
		metric.WithDescription("Memory utilization percentage"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.memCached, err = meter.Int64ObservableGauge(
		"system.memory.cached",
		metric.WithDescription("Cached memory"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.memBuffers, err = meter.Int64ObservableGauge(
		"system.memory.buffers",
		metric.WithDescription("Buffered memory"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	// Swap metrics
	sm.swapTotal, err = meter.Int64ObservableGauge(
		"system.swap.total",
		metric.WithDescription("Total swap space"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.swapUsed, err = meter.Int64ObservableGauge(
		"system.swap.used",
		metric.WithDescription("Used swap space"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.swapFree, err = meter.Int64ObservableGauge(
		"system.swap.free",
		metric.WithDescription("Free swap space"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.swapUsedPercent, err = meter.Float64ObservableGauge(
		"system.swap.utilization",
		metric.WithDescription("Swap utilization percentage"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	// Disk metrics
	sm.diskUsagePercent, err = meter.Float64ObservableGauge(
		"system.disk.utilization",
		metric.WithDescription("Disk utilization percentage"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskUsedBytes, err = meter.Int64ObservableGauge(
		"system.disk.used",
		metric.WithDescription("Used disk space"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskFreeBytes, err = meter.Int64ObservableGauge(
		"system.disk.free",
		metric.WithDescription("Free disk space"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskTotalBytes, err = meter.Int64ObservableGauge(
		"system.disk.total",
		metric.WithDescription("Total disk space"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskReadBytes, err = meter.Int64ObservableCounter(
		"system.disk.io.read",
		metric.WithDescription("Bytes read from disk"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskWriteBytes, err = meter.Int64ObservableCounter(
		"system.disk.io.write",
		metric.WithDescription("Bytes written to disk"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskReadOps, err = meter.Int64ObservableCounter(
		"system.disk.operations.read",
		metric.WithDescription("Number of disk read operations"),
		metric.WithUnit("{operation}"),
	)
	if err != nil {
		return nil, err
	}

	sm.diskWriteOps, err = meter.Int64ObservableCounter(
		"system.disk.operations.write",
		metric.WithDescription("Number of disk write operations"),
		metric.WithUnit("{operation}"),
	)
	if err != nil {
		return nil, err
	}

	// Network metrics
	sm.netBytesSent, err = meter.Int64ObservableCounter(
		"system.network.io.transmit",
		metric.WithDescription("Bytes sent over network"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.netBytesRecv, err = meter.Int64ObservableCounter(
		"system.network.io.receive",
		metric.WithDescription("Bytes received over network"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, err
	}

	sm.netPacketsSent, err = meter.Int64ObservableCounter(
		"system.network.packets.transmit",
		metric.WithDescription("Packets sent over network"),
		metric.WithUnit("{packet}"),
	)
	if err != nil {
		return nil, err
	}

	sm.netPacketsRecv, err = meter.Int64ObservableCounter(
		"system.network.packets.receive",
		metric.WithDescription("Packets received over network"),
		metric.WithUnit("{packet}"),
	)
	if err != nil {
		return nil, err
	}

	sm.netErrorsIn, err = meter.Int64ObservableCounter(
		"system.network.errors.receive",
		metric.WithDescription("Network receive errors"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	sm.netErrorsOut, err = meter.Int64ObservableCounter(
		"system.network.errors.transmit",
		metric.WithDescription("Network transmit errors"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	sm.netDropsIn, err = meter.Int64ObservableCounter(
		"system.network.drops.receive",
		metric.WithDescription("Network receive drops"),
		metric.WithUnit("{drop}"),
	)
	if err != nil {
		return nil, err
	}

	sm.netDropsOut, err = meter.Int64ObservableCounter(
		"system.network.drops.transmit",
		metric.WithDescription("Network transmit drops"),
		metric.WithUnit("{drop}"),
	)
	if err != nil {
		return nil, err
	}

	// Process metrics
	sm.processCount, err = meter.Int64ObservableGauge(
		"system.process.count",
		metric.WithDescription("Number of running processes"),
		metric.WithUnit("{process}"),
	)
	if err != nil {
		return nil, err
	}

	sm.fdCount, err = meter.Int64ObservableGauge(
		"system.process.file_descriptors",
		metric.WithDescription("Number of open file descriptors"),
		metric.WithUnit("{fd}"),
	)
	if err != nil {
		return nil, err
	}

	// System info
	sm.uptimeSeconds, err = meter.Int64ObservableGauge(
		"system.uptime",
		metric.WithDescription("System uptime in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	// Register callback per aggiornare tutte le metriche
	_, err = meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			sm.collect(ctx, o)
			return nil
		},
		sm.cpuUsagePercent,
		sm.cpuUsagePerCore,
		sm.cpuLoadAvg1,
		sm.cpuLoadAvg5,
		sm.cpuLoadAvg15,
		sm.cpuCores,
		sm.cpuThreads,
		sm.memTotal,
		sm.memAvailable,
		sm.memUsed,
		sm.memFree,
		sm.memUsedPercent,
		sm.memCached,
		sm.memBuffers,
		sm.swapTotal,
		sm.swapUsed,
		sm.swapFree,
		sm.swapUsedPercent,
		sm.diskUsagePercent,
		sm.diskUsedBytes,
		sm.diskFreeBytes,
		sm.diskTotalBytes,
		sm.diskReadBytes,
		sm.diskWriteBytes,
		sm.diskReadOps,
		sm.diskWriteOps,
		sm.netBytesSent,
		sm.netBytesRecv,
		sm.netPacketsSent,
		sm.netPacketsRecv,
		sm.netErrorsIn,
		sm.netErrorsOut,
		sm.netDropsIn,
		sm.netDropsOut,
		sm.processCount,
		sm.fdCount,
		sm.uptimeSeconds,
	)
	if err != nil {
		return nil, err
	}

	return sm, nil
}

// collect updates all system metrics by delegating to individual collectors.
//
// It is called internally by the OpenTelemetry callback registered
// during NewSystemMetrics.
func (sm *SystemMetrics) collect(_ context.Context, o metric.Observer) {
	sm.collectCPU(o)
	sm.collectMemory(o)
	sm.collectDisk(o)
	sm.collectNetwork(o)
	sm.collectProcess(o)
	sm.collectSystem(o)
}

// collectCPU observes CPU metrics: utilization, per-core usage, load averages,
// and physical/logical core counts.
//
// Utilization metrics are reported as percentages in the range [0, 1].
// Load averages are reported as unitless floats.
func (sm *SystemMetrics) collectCPU(o metric.Observer) {
	// CPU utilization (overall)
	if percent, err := cpu.Percent(0, false); err == nil && len(percent) > 0 {
		o.ObserveFloat64(sm.cpuUsagePercent, percent[0]/100.0)
	}

	// CPU utilization per core
	if percents, err := cpu.Percent(0, true); err == nil {
		for i, p := range percents {
			o.ObserveFloat64(sm.cpuUsagePerCore, p/100.0,
				metric.WithAttributes(attribute.Int("cpu", i)))
		}
	}

	// Load average
	if loadAvg, err := load.Avg(); err == nil {
		o.ObserveFloat64(sm.cpuLoadAvg1, loadAvg.Load1)
		o.ObserveFloat64(sm.cpuLoadAvg5, loadAvg.Load5)
		o.ObserveFloat64(sm.cpuLoadAvg15, loadAvg.Load15)
	}

	// CPU counts
	o.ObserveInt64(sm.cpuCores, int64(runtime.NumCPU()))
	if logicalCount, err := cpu.Counts(true); err == nil {
		o.ObserveInt64(sm.cpuThreads, int64(logicalCount))
	}
}

// collectMemory observes system memory and swap metrics.
//
// Metrics include total, used, free, available memory, cached and buffered memory,
// swap totals, and utilization percentages.
func (sm *SystemMetrics) collectMemory(o metric.Observer) {
	if v, err := mem.VirtualMemory(); err == nil {
		o.ObserveInt64(sm.memTotal, int64(v.Total))
		o.ObserveInt64(sm.memAvailable, int64(v.Available))
		o.ObserveInt64(sm.memUsed, int64(v.Used))
		o.ObserveInt64(sm.memFree, int64(v.Free))
		o.ObserveFloat64(sm.memUsedPercent, v.UsedPercent/100.0)
		o.ObserveInt64(sm.memCached, int64(v.Cached))
		o.ObserveInt64(sm.memBuffers, int64(v.Buffers))
	}

	if s, err := mem.SwapMemory(); err == nil {
		o.ObserveInt64(sm.swapTotal, int64(s.Total))
		o.ObserveInt64(sm.swapUsed, int64(s.Used))
		o.ObserveInt64(sm.swapFree, int64(s.Free))
		o.ObserveFloat64(sm.swapUsedPercent, s.UsedPercent/100.0)
	}
}

// collectDisk observes disk usage and I/O metrics.
//
// It reports usage percentages, total/used/free space, and I/O operations.
// Delta-based counters are used for read/write bytes and operation counts,
// ensuring that only increments since the last observation are reported.
func (sm *SystemMetrics) collectDisk(o metric.Observer) {
	// Disk usage
	if usage, err := disk.Usage(sm.diskPath); err == nil {
		o.ObserveFloat64(sm.diskUsagePercent, usage.UsedPercent/100.0,
			metric.WithAttributes(attribute.String("path", sm.diskPath)))
		o.ObserveInt64(sm.diskUsedBytes, int64(usage.Used),
			metric.WithAttributes(attribute.String("path", sm.diskPath)))
		o.ObserveInt64(sm.diskFreeBytes, int64(usage.Free),
			metric.WithAttributes(attribute.String("path", sm.diskPath)))
		o.ObserveInt64(sm.diskTotalBytes, int64(usage.Total),
			metric.WithAttributes(attribute.String("path", sm.diskPath)))
	}

	// Disk I/O
	if ioCounters, err := disk.IOCounters(); err == nil {
		for name, io := range ioCounters {
			attrs := metric.WithAttributes(attribute.String("device", name))

			// Calculate deltas for counters
			if last, ok := sm.lastDiskStats[name]; ok {
				readDelta := int64(io.ReadBytes - last.ReadBytes)
				writeDelta := int64(io.WriteBytes - last.WriteBytes)
				readOpsDelta := int64(io.ReadCount - last.ReadCount)
				writeOpsDelta := int64(io.WriteCount - last.WriteCount)

				if readDelta >= 0 {
					o.ObserveInt64(sm.diskReadBytes, readDelta, attrs)
				}
				if writeDelta >= 0 {
					o.ObserveInt64(sm.diskWriteBytes, writeDelta, attrs)
				}
				if readOpsDelta >= 0 {
					o.ObserveInt64(sm.diskReadOps, readOpsDelta, attrs)
				}
				if writeOpsDelta >= 0 {
					o.ObserveInt64(sm.diskWriteOps, writeOpsDelta, attrs)
				}
			}

			sm.lastDiskStats[name] = io
		}
	}
}

// collectNetwork observes network metrics per interface.
//
// Metrics include bytes and packets sent/received, errors, and drops.
// Delta-based counters report only changes since the previous observation.
func (sm *SystemMetrics) collectNetwork(o metric.Observer) {
	if ioCounters, err := net.IOCounters(true); err == nil {
		for _, io := range ioCounters {
			attrs := metric.WithAttributes(attribute.String("interface", io.Name))

			// Calculate deltas for counters
			if last, ok := sm.lastNetStats[io.Name]; ok {
				bytesSentDelta := int64(io.BytesSent - last.BytesSent)
				bytesRecvDelta := int64(io.BytesRecv - last.BytesRecv)
				packetsSentDelta := int64(io.PacketsSent - last.PacketsSent)
				packetsRecvDelta := int64(io.PacketsRecv - last.PacketsRecv)
				errInDelta := int64(io.Errin - last.Errin)
				errOutDelta := int64(io.Errout - last.Errout)
				dropInDelta := int64(io.Dropin - last.Dropin)
				dropOutDelta := int64(io.Dropout - last.Dropout)

				if bytesSentDelta >= 0 {
					o.ObserveInt64(sm.netBytesSent, bytesSentDelta, attrs)
				}
				if bytesRecvDelta >= 0 {
					o.ObserveInt64(sm.netBytesRecv, bytesRecvDelta, attrs)
				}
				if packetsSentDelta >= 0 {
					o.ObserveInt64(sm.netPacketsSent, packetsSentDelta, attrs)
				}
				if packetsRecvDelta >= 0 {
					o.ObserveInt64(sm.netPacketsRecv, packetsRecvDelta, attrs)
				}
				if errInDelta >= 0 {
					o.ObserveInt64(sm.netErrorsIn, errInDelta, attrs)
				}
				if errOutDelta >= 0 {
					o.ObserveInt64(sm.netErrorsOut, errOutDelta, attrs)
				}
				if dropInDelta >= 0 {
					o.ObserveInt64(sm.netDropsIn, dropInDelta, attrs)
				}
				if dropOutDelta >= 0 {
					o.ObserveInt64(sm.netDropsOut, dropOutDelta, attrs)
				}
			}

			sm.lastNetStats[io.Name] = io
		}
	}
}

// collectProcess observes running processes and open file descriptors.
//
// processCount reports the number of active processes.
// fdCount reports the number of open file descriptors for the current process.
func (sm *SystemMetrics) collectProcess(o metric.Observer) {
	// Process count
	if procs, err := process.Processes(); err == nil {
		o.ObserveInt64(sm.processCount, int64(len(procs)))
	}

	// File descriptors (current process)
	if currentProc, err := process.NewProcess(int32(os.Getpid())); err == nil {
		if numFDs, err := currentProc.NumFDs(); err == nil {
			o.ObserveInt64(sm.fdCount, int64(numFDs))
		}
	}
}

// collectSystem observes system-level metrics.
//
// Currently, this function reports system uptime in seconds.
func (sm *SystemMetrics) collectSystem(o metric.Observer) {
	if uptime, err := host.Uptime(); err == nil {
		if uptime > math.MaxInt64 {
			uptime = math.MaxInt64
		}
		// #nosec G115 -- uptime is in seconds and always fits in int64
		o.ObserveInt64(sm.uptimeSeconds, int64(uptime))
	}
}
