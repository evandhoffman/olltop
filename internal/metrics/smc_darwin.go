//go:build darwin

package metrics

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/IOKitLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <string.h>

// SMC data types and structures
#define KERNEL_INDEX_SMC 2

#define SMC_CMD_READ_BYTES  5
#define SMC_CMD_READ_KEYINFO 9

typedef struct {
    char major;
    char minor;
    char build;
    char reserved;
    unsigned short release;
} SMCKeyData_vers_t;

typedef struct {
    unsigned short version;
    unsigned short length;
    unsigned int cpuPLimit;
    unsigned int gpuPLimit;
    unsigned int memPLimit;
} SMCKeyData_pLimitData_t;

typedef struct {
    uint32_t dataSize;
    uint32_t dataType;
    char dataAttributes;
} SMCKeyData_keyInfo_t;

typedef unsigned char SMCBytes_t[32];

typedef struct {
    uint32_t key;
    SMCKeyData_vers_t vers;
    SMCKeyData_pLimitData_t pLimitData;
    SMCKeyData_keyInfo_t keyInfo;
    char result;
    char status;
    char data8;
    uint32_t data32;
    SMCBytes_t bytes;
} SMCKeyData_t;

typedef struct {
    char key[5];
    uint32_t dataSize;
    char dataType[5];
    SMCBytes_t bytes;
} SMCVal_t;

static uint32_t str_to_uint32(const char *str) {
    uint32_t ans = 0;
    ans += (unsigned char)str[0] << 24;
    ans += (unsigned char)str[1] << 16;
    ans += (unsigned char)str[2] << 8;
    ans += (unsigned char)str[3];
    return ans;
}

static io_connect_t conn = 0;

static int smc_open(void) {
    if (conn != 0) return 0;

    io_service_t service = IOServiceGetMatchingService(
        kIOMainPortDefault,
        IOServiceMatching("AppleSMCKeysEndpoint"));
    if (service == 0) {
        // Fallback to older name
        service = IOServiceGetMatchingService(
            kIOMainPortDefault,
            IOServiceMatching("AppleSMC"));
    }
    if (service == 0) return -1;

    kern_return_t kr = IOServiceOpen(service, mach_task_self(), 0, &conn);
    IOObjectRelease(service);
    if (kr != KERN_SUCCESS) return -1;
    return 0;
}

static int smc_read_key(const char *key, SMCVal_t *val) {
    if (smc_open() != 0) return -1;

    SMCKeyData_t inputStruct = {0};
    SMCKeyData_t outputStruct = {0};
    size_t structSize = sizeof(SMCKeyData_t);

    inputStruct.key = str_to_uint32(key);
    inputStruct.data8 = SMC_CMD_READ_KEYINFO;

    kern_return_t kr = IOConnectCallStructMethod(conn,
        KERNEL_INDEX_SMC, &inputStruct, structSize, &outputStruct, &structSize);
    if (kr != KERN_SUCCESS) return -1;

    val->dataSize = outputStruct.keyInfo.dataSize;

    // Capture data type from keyinfo response BEFORE the read call overwrites outputStruct
    val->dataType[0] = (outputStruct.keyInfo.dataType >> 24) & 0xff;
    val->dataType[1] = (outputStruct.keyInfo.dataType >> 16) & 0xff;
    val->dataType[2] = (outputStruct.keyInfo.dataType >> 8) & 0xff;
    val->dataType[3] = outputStruct.keyInfo.dataType & 0xff;
    val->dataType[4] = 0;

    inputStruct.keyInfo.dataSize = val->dataSize;
    inputStruct.data8 = SMC_CMD_READ_BYTES;

    kr = IOConnectCallStructMethod(conn,
        KERNEL_INDEX_SMC, &inputStruct, structSize, &outputStruct, &structSize);
    if (kr != KERN_SUCCESS) return -1;

    memcpy(val->bytes, outputStruct.bytes, sizeof(val->bytes));
    strncpy(val->key, key, 4);
    val->key[4] = 0;

    return 0;
}

// Read a temperature value (flt or sp78 type) and return as float
static float smc_read_temp(const char *key) {
    SMCVal_t val = {0};
    if (smc_read_key(key, &val) != 0) return -1.0f;

    if (val.dataSize == 0) return -1.0f;

    // "flt " type — 32-bit float
    if (val.dataType[0] == 'f' && val.dataType[1] == 'l' &&
        val.dataType[2] == 't' && val.dataType[3] == ' ') {
        float f;
        memcpy(&f, val.bytes, sizeof(float));
        return f;
    }

    // "sp78" type — signed 8.8 fixed point
    if (val.dataType[0] == 's' && val.dataType[1] == 'p' &&
        val.dataType[2] == '7' && val.dataType[3] == '8') {
        int16_t raw = ((int16_t)val.bytes[0] << 8) | (uint8_t)val.bytes[1];
        return (float)raw / 256.0f;
    }

    // "ui16" — some temps use this (value in 10ths of degree or raw)
    if (val.dataType[0] == 'u' && val.dataType[1] == 'i' &&
        val.dataType[2] == '1' && val.dataType[3] == '6') {
        uint16_t raw = ((uint16_t)val.bytes[0] << 8) | (uint8_t)val.bytes[1];
        return (float)raw;
    }

    return -1.0f;
}

// Read fan speed (flt or fpe2 type) and return as float RPM
static float smc_read_fan(const char *key) {
    SMCVal_t val = {0};
    if (smc_read_key(key, &val) != 0) return -1.0f;

    if (val.dataSize == 0) return -1.0f;

    // "flt " type
    if (val.dataType[0] == 'f' && val.dataType[1] == 'l' &&
        val.dataType[2] == 't' && val.dataType[3] == ' ') {
        float f;
        memcpy(&f, val.bytes, sizeof(float));
        return f;
    }

    // "fpe2" type — unsigned 14.2 fixed point
    if (val.dataType[0] == 'f' && val.dataType[1] == 'p' &&
        val.dataType[2] == 'e' && val.dataType[3] == '2') {
        uint16_t raw = ((uint16_t)val.bytes[0] << 8) | (uint8_t)val.bytes[1];
        return (float)raw / 4.0f;
    }

    return -1.0f;
}

typedef struct {
    float cpu_temp;
    float gpu_temp;
    float fan_rpm[4];
    int fan_count;
    int valid;
} sensor_data_t;

static sensor_data_t read_sensors() {
    sensor_data_t data = {0};
    data.valid = 0;

    if (smc_open() != 0) return data;

    // CPU temperature — try common Apple Silicon keys
    const char *cpu_keys[] = {"Tp0C", "Tp09", "Tp01", "TC0P", "Tc0a", NULL};
    for (int i = 0; cpu_keys[i] != NULL; i++) {
        float t = smc_read_temp(cpu_keys[i]);
        if (t > 0 && t < 150) {
            data.cpu_temp = t;
            data.valid = 1;
            break;
        }
    }

    // GPU temperature — try common Apple Silicon keys (ordered newer-first).
    // Threshold > 5 skips stale/wrong sensors that return near-zero on M4.
    const char *gpu_keys[] = {
        "Tg05", "Tg0D", "Tg0L", "Tg0T",  // M3/M4 era
        "Tg0b", "Tg0f", "Tg0j", "Tg1d",  // other Apple Silicon variants
        "Tg1b", "Tg0P", "TG0P", "Tg0a",  // older / M1/M2
        NULL
    };
    for (int i = 0; gpu_keys[i] != NULL; i++) {
        float t = smc_read_temp(gpu_keys[i]);
        if (t > 5 && t < 150) {
            data.gpu_temp = t;
            data.valid = 1;
            break;
        }
    }

    // Fan speeds — try F0Ac, F1Ac, etc. (actual fan speed)
    data.fan_count = 0;
    for (int i = 0; i < 4; i++) {
        char key[5];
        snprintf(key, sizeof(key), "F%dAc", i);
        float rpm = smc_read_fan(key);
        if (rpm >= 0) {
            data.fan_rpm[i] = rpm;
            data.fan_count = i + 1;
            data.valid = 1;
        } else {
            break;
        }
    }

    return data;
}
*/
import "C"

// SensorData contains temperature and fan speed readings from SMC.
type SensorData struct {
	CPUTemp   float64 // CPU temperature in °C
	GPUTemp   float64 // GPU temperature in °C
	FanSpeeds []float64 // fan speeds in RPM
	Available bool
}

// getSensorData reads temperature and fan data from the SMC.
func getSensorData() SensorData {
	data := C.read_sensors()
	if data.valid == 0 {
		return SensorData{}
	}

	sd := SensorData{
		CPUTemp:   float64(data.cpu_temp),
		GPUTemp:   float64(data.gpu_temp),
		Available: true,
	}

	for i := 0; i < int(data.fan_count); i++ {
		sd.FanSpeeds = append(sd.FanSpeeds, float64(data.fan_rpm[i]))
	}

	return sd
}
