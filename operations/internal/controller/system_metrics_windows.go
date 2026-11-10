package controller

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	systemKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	systemGetTimes             = systemKernel32.NewProc("GetSystemTimes")
	systemGlobalMemoryStatusEx = systemKernel32.NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	Length, MemoryLoad                                 uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

func systemRegistryText(path, name string) string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, _ := key.GetStringValue(name)
	return strings.TrimSpace(value)
}

func collectSystemMetrics(path string) (systemMetrics, cpuCounter) {
	info := newSystemMetrics(path)
	version := windows.RtlGetVersion()
	product := systemRegistryText(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName")
	if product == "" {
		product = "Windows"
	} else if version.ProductType == 1 && version.MajorVersion == 10 && version.BuildNumber >= 22000 {
		product = strings.Replace(product, "Windows 10", "Windows 11", 1)
	}
	info.PlatformVersion = fmt.Sprintf("%s · %d.%d.%d", product, version.MajorVersion, version.MinorVersion, version.BuildNumber)
	info.CPU.Model = systemRegistryText(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString")
	var memory memoryStatusEx
	memory.Length = uint32(unsafe.Sizeof(memory))
	if success, _, _ := systemGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memory))); success != 0 {
		info.Memory = memoryUsage(memory.TotalPhys, memory.AvailPhys)
	}
	if directory, err := windows.UTF16PtrFromString(path); err == nil {
		var available, total, free uint64
		if windows.GetDiskFreeSpaceEx(directory, &available, &total, &free) == nil {
			info.Disk = diskUsage(path, total, free, available)
		}
	}
	var idle, kernel, user windows.Filetime
	if success, _, _ := systemGetTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); success != 0 {
		ticks := func(value windows.Filetime) uint64 { return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime) }
		return info, cpuCounter{total: ticks(kernel) + ticks(user), idle: ticks(idle), valid: true}
	}
	return info, cpuCounter{}
}
