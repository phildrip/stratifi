package main

/*
#cgo LDFLAGS: -lvulkan
#include <vulkan/vulkan.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    char deviceName[256];
    uint32_t apiVersion;
    uint32_t driverVersion;
    uint32_t vendorID;
    uint32_t deviceID;
    int hasKhrDisplay;
    int hasDrmFormatModifier;
    int hasExternalMemoryFd;
} VkProbeResult;

static int probe_vulkan(VkProbeResult* result) {
    memset(result, 0, sizeof(VkProbeResult));

    VkApplicationInfo appInfo = {
        .sType = VK_STRUCTURE_TYPE_APPLICATION_INFO,
        .pApplicationName = "bloxwm-probe",
        .applicationVersion = VK_MAKE_VERSION(0, 1, 0),
        .pEngineName = "bloxwm",
        .engineVersion = VK_MAKE_VERSION(0, 1, 0),
        .apiVersion = VK_API_VERSION_1_3,
    };

    VkInstanceCreateInfo createInfo = {
        .sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO,
        .pApplicationInfo = &appInfo,
        .enabledExtensionCount = 0,
        .ppEnabledExtensionNames = NULL,
    };

    VkInstance instance;
    VkResult res = vkCreateInstance(&createInfo, NULL, &instance);
    if (res != VK_SUCCESS) {
        return (int)res;
    }

    uint32_t deviceCount = 0;
    vkEnumeratePhysicalDevices(instance, &deviceCount, NULL);
    if (deviceCount == 0) {
        vkDestroyInstance(instance, NULL);
        return -999;
    }

    VkPhysicalDevice devices[4];
    if (deviceCount > 4) deviceCount = 4;
    vkEnumeratePhysicalDevices(instance, &deviceCount, devices);

    VkPhysicalDeviceProperties props;
    vkGetPhysicalDeviceProperties(devices[0], &props);

    strncpy(result->deviceName, props.deviceName, sizeof(result->deviceName) - 1);
    result->apiVersion = props.apiVersion;
    result->driverVersion = props.driverVersion;
    result->vendorID = props.vendorID;
    result->deviceID = props.deviceID;

    // Check device extensions
    uint32_t extCount = 0;
    vkEnumerateDeviceExtensionProperties(devices[0], NULL, &extCount, NULL);
    if (extCount > 0) {
        VkExtensionProperties* extensions = malloc(sizeof(VkExtensionProperties) * extCount);
        if (extensions) {
            vkEnumerateDeviceExtensionProperties(devices[0], NULL, &extCount, extensions);
            for (uint32_t i = 0; i < extCount; i++) {
                if (strcmp(extensions[i].extensionName, "VK_KHR_display") == 0) {
                    result->hasKhrDisplay = 1;
                }
                if (strcmp(extensions[i].extensionName, "VK_EXT_image_drm_format_modifier") == 0) {
                    result->hasDrmFormatModifier = 1;
                }
                if (strcmp(extensions[i].extensionName, "VK_KHR_external_memory_fd") == 0) {
                    result->hasExternalMemoryFd = 1;
                }
            }
            free(extensions);
        }
    }

    vkDestroyInstance(instance, NULL);
    return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux DRM ioctls definition
// DRM_IOCTL_VERSION: _IOWR('d', 0x00, struct drm_version)
// DRM_IOCTL_MODE_GETRESOURCES: _IOWR('d', 0xA0, struct drm_mode_card_res)
const (
	drmIoctlVersion      = 0xc0406400
	drmIoctlGetResources = 0xc04064a0
)

type drmVersion struct {
	versionMajor      int32
	versionMinor      int32
	versionPatchlevel int32
	nameLen           uint64
	name              uint64
	dateLen           uint64
	date              uint64
	descLen           uint64
	desc              uint64
}

type drmCardRes struct {
	fbIdPtr         uint64
	crtcIdPtr       uint64
	connectorIdPtr  uint64
	encoderIdPtr    uint64
	countFbs        uint32
	countCrtcs      uint32
	countConnectors uint32
	countEncoders   uint32
	minWidth        uint32
	maxWidth        uint32
	minHeight       uint32
	maxHeight       uint32
}

func probeDRM(cardPath string) error {
	fmt.Printf("[+] Probing DRM device: %s\n", cardPath)
	fd, err := unix.Open(cardPath, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", cardPath, err)
	}
	defer unix.Close(fd)

	// 1. Query DRM Driver Version
	nameBuf := make([]byte, 64)
	descBuf := make([]byte, 128)
	dateBuf := make([]byte, 32)

	v := drmVersion{
		nameLen: uint64(len(nameBuf)),
		name:    uint64(uintptr(unsafe.Pointer(&nameBuf[0]))),
		descLen: uint64(len(descBuf)),
		desc:    uint64(uintptr(unsafe.Pointer(&descBuf[0]))),
		dateLen: uint64(len(dateBuf)),
		date:    uint64(uintptr(unsafe.Pointer(&dateBuf[0]))),
	}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), drmIoctlVersion, uintptr(unsafe.Pointer(&v)))
	if errno != 0 {
		return fmt.Errorf("DRM_IOCTL_VERSION failed: %w", errno)
	}

	fmt.Printf("    DRM Driver: %s (%s, v%d.%d.%d)\n",
		string(nameBuf[:v.nameLen]),
		string(descBuf[:v.descLen]),
		v.versionMajor, v.versionMinor, v.versionPatchlevel,
	)

	// 2. Query Card Resources (CRTCs, Connectors)
	res := drmCardRes{}
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(fd), drmIoctlGetResources, uintptr(unsafe.Pointer(&res)))
	if errno != 0 {
		return fmt.Errorf("DRM_IOCTL_MODE_GETRESOURCES failed: %w", errno)
	}

	fmt.Printf("    Display Outputs: %d connectors, %d CRTCs, %d encoders\n",
		res.countConnectors, res.countCrtcs, res.countEncoders)
	fmt.Printf("    Resolution Range: %dx%d to %dx%d\n",
		res.minWidth, res.minHeight, res.maxWidth, res.maxHeight)

	return nil
}

func probeVulkan() error {
	fmt.Printf("\n[+] Probing Vulkan Hardware via direct Cgo loader:\n")
	var res C.VkProbeResult
	rc := C.probe_vulkan(&res)
	if rc != 0 {
		return fmt.Errorf("probe_vulkan failed with code %d", rc)
	}

	name := C.GoString(&res.deviceName[0])
	apiMajor := (uint32(res.apiVersion) >> 22) & 0x7f
	apiMinor := (uint32(res.apiVersion) >> 12) & 0x3ff
	apiPatch := uint32(res.apiVersion) & 0xfff

	fmt.Printf("    Device Name: %s\n", name)
	fmt.Printf("    Vulkan API:  v%d.%d.%d\n", apiMajor, apiMinor, apiPatch)
	fmt.Printf("    Vendor/Dev:  0x%04x / 0x%04x\n", uint32(res.vendorID), uint32(res.deviceID))
	fmt.Println("    Wayland Compositor Extensions:")
	fmt.Printf("      - VK_EXT_image_drm_format_modifier (DMA-BUF Import): %v\n", res.hasDrmFormatModifier == 1)
	fmt.Printf("      - VK_KHR_external_memory_fd (Zero-Copy Interop):    %v\n", res.hasExternalMemoryFd == 1)
	fmt.Printf("      - VK_KHR_display (Direct KMS Presentation):         %v\n", res.hasKhrDisplay == 1)

	return nil
}

func main() {
	fmt.Println("=== bloxwm Hardware & Graphics Probe ===")

	cardPath := "/dev/dri/card1"
	if _, err := os.Stat(cardPath); os.IsNotExist(err) {
		cardPath = "/dev/dri/card0"
	}

	if err := probeDRM(cardPath); err != nil {
		fmt.Printf("[-] DRM probe error: %v\n", err)
	}

	if err := probeVulkan(); err != nil {
		fmt.Printf("[-] Vulkan probe error: %v\n", err)
	}

	fmt.Println("\n=== Probe Complete ===")
}
