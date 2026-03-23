//go:build darwin

package metrics

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/IOKitLib.h>
#include <CoreFoundation/CoreFoundation.h>

typedef struct {
    int device_utilization;   // "Device Utilization %"
    int renderer_utilization; // "Renderer Utilization %"
    int tiler_utilization;    // "Tiler Utilization %"
    int valid;                // 1 if we got data, 0 otherwise
} gpu_stats_t;

static gpu_stats_t get_gpu_utilization() {
    gpu_stats_t stats = {0, 0, 0, 0};

    CFMutableDictionaryRef matching = IOServiceMatching("IOAccelerator");
    if (!matching) return stats;

    io_iterator_t iterator;
    kern_return_t kr = IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iterator);
    if (kr != KERN_SUCCESS) return stats;

    io_service_t service;
    while ((service = IOIteratorNext(iterator)) != IO_OBJECT_NULL) {
        CFMutableDictionaryRef props = NULL;
        kr = IORegistryEntryCreateCFProperties(service, &props, kCFAllocatorDefault, 0);
        if (kr != KERN_SUCCESS || !props) {
            IOObjectRelease(service);
            continue;
        }

        CFDictionaryRef perfStats = (CFDictionaryRef)CFDictionaryGetValue(props,
            CFSTR("PerformanceStatistics"));
        if (perfStats && CFGetTypeID(perfStats) == CFDictionaryGetTypeID()) {
            CFNumberRef val;

            val = (CFNumberRef)CFDictionaryGetValue(perfStats, CFSTR("Device Utilization %"));
            if (val && CFGetTypeID(val) == CFNumberGetTypeID()) {
                CFNumberGetValue(val, kCFNumberIntType, &stats.device_utilization);
            }

            val = (CFNumberRef)CFDictionaryGetValue(perfStats, CFSTR("Renderer Utilization %"));
            if (val && CFGetTypeID(val) == CFNumberGetTypeID()) {
                CFNumberGetValue(val, kCFNumberIntType, &stats.renderer_utilization);
            }

            val = (CFNumberRef)CFDictionaryGetValue(perfStats, CFSTR("Tiler Utilization %"));
            if (val && CFGetTypeID(val) == CFNumberGetTypeID()) {
                CFNumberGetValue(val, kCFNumberIntType, &stats.tiler_utilization);
            }

            stats.valid = 1;
        }

        CFRelease(props);
        IOObjectRelease(service);
        if (stats.valid) break; // take the first GPU
    }

    IOObjectRelease(iterator);
    return stats;
}
*/
import "C"

// GPUStats contains Apple Silicon GPU utilization metrics from IOKit.
type GPUStats struct {
	DeviceUtilization   int // overall GPU utilization %
	RendererUtilization int // renderer (shader) utilization %
	TilerUtilization    int // tiler utilization %
	Available           bool
}

// getGPUStats reads GPU utilization from the IOKit AGXAccelerator service.
func getGPUStats() GPUStats {
	stats := C.get_gpu_utilization()
	return GPUStats{
		DeviceUtilization:   int(stats.device_utilization),
		RendererUtilization: int(stats.renderer_utilization),
		TilerUtilization:    int(stats.tiler_utilization),
		Available:           stats.valid == 1,
	}
}
