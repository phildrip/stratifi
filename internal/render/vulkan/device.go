package vulkan

/*
#cgo LDFLAGS: -lvulkan
#include "vk_common.h"
#include <fcntl.h>
#include <unistd.h>

static int init_vulkan_context(VkContext* ctx) {
    memset(ctx, 0, sizeof(VkContext));

    VkApplicationInfo appInfo = {
        .sType = VK_STRUCTURE_TYPE_APPLICATION_INFO,
        .pApplicationName = "bloxwm-vulkan",
        .applicationVersion = VK_MAKE_VERSION(0, 1, 0),
        .pEngineName = "bloxwm",
        .engineVersion = VK_MAKE_VERSION(0, 1, 0),
        .apiVersion = VK_API_VERSION_1_3,
    };

    // Optional validation (BLOXWM_VK_VALIDATION=1 + VK_LAYER_PATH): catches
    // layout and sync bugs the silent paths hide.
    const char* validationLayer = "VK_LAYER_KHRONOS_validation";
    VkInstanceCreateInfo instInfo = {
        .sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO,
        .pApplicationInfo = &appInfo,
    };
    if (getenv("BLOXWM_VK_VALIDATION") != NULL) {
        instInfo.enabledLayerCount = 1;
        instInfo.ppEnabledLayerNames = &validationLayer;
    }

    VkResult res = vkCreateInstance(&instInfo, NULL, &ctx->instance);
    if (res != VK_SUCCESS) return (int)res;

    uint32_t devCount = 0;
    res = vkEnumeratePhysicalDevices(ctx->instance, &devCount, NULL);
    if (res != VK_SUCCESS || devCount == 0) {
        vkDestroyInstance(ctx->instance, NULL);
        return -1;
    }
    VkPhysicalDevice devices[16];
    if (devCount > 16) devCount = 16;
    res = vkEnumeratePhysicalDevices(ctx->instance, &devCount, devices);
    if (res != VK_SUCCESS || devCount == 0) {
        vkDestroyInstance(ctx->instance, NULL);
        return -1;
    }
    int devIdx = 0;
    const char* sel = getenv("BLOXWM_VK_DEVICE");
    if (sel) devIdx = atoi(sel);
    if (devIdx < 0 || devIdx >= (int)devCount) devIdx = 0;
    ctx->pdev = devices[devIdx];

    VkPhysicalDeviceProperties props;
    vkGetPhysicalDeviceProperties(ctx->pdev, &props);
    strncpy(ctx->deviceName, props.deviceName, sizeof(ctx->deviceName) - 1);

    // Find graphics queue
    uint32_t qCount = 0;
    vkGetPhysicalDeviceQueueFamilyProperties(ctx->pdev, &qCount, NULL);
    VkQueueFamilyProperties qProps[qCount];
    vkGetPhysicalDeviceQueueFamilyProperties(ctx->pdev, &qCount, qProps);

    int graphicsQ = -1;
    for (uint32_t i = 0; i < qCount; i++) {
        if (qProps[i].queueFlags & VK_QUEUE_GRAPHICS_BIT) {
            graphicsQ = (int)i;
            break;
        }
    }
    if (graphicsQ < 0) {
        vkDestroyInstance(ctx->instance, NULL);
        return -2;
    }
    ctx->queueFamily = (uint32_t)graphicsQ;

    float priority = 1.0f;
    VkDeviceQueueCreateInfo qInfo = {
        .sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO,
        .queueFamilyIndex = ctx->queueFamily,
        .queueCount = 1,
        .pQueuePriorities = &priority,
    };

    const char* extNames[] = {
        "VK_KHR_external_memory_fd",
        "VK_EXT_image_drm_format_modifier",
        "VK_KHR_image_format_list",
    };

    VkDeviceCreateInfo devInfo = {
        .sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO,
        .queueCreateInfoCount = 1,
        .pQueueCreateInfos = &qInfo,
        .enabledExtensionCount = 3,
        .ppEnabledExtensionNames = extNames,
    };

    res = vkCreateDevice(ctx->pdev, &devInfo, NULL, &ctx->device);
    if (res != VK_SUCCESS) {
        vkDestroyInstance(ctx->instance, NULL);
        return (int)res;
    }

    vkGetDeviceQueue(ctx->device, ctx->queueFamily, 0, &ctx->queue);

    // Create command pool
    VkCommandPoolCreateInfo poolInfo = {
        .sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO,
        .flags = VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT,
        .queueFamilyIndex = ctx->queueFamily,
    };
    res = vkCreateCommandPool(ctx->device, &poolInfo, NULL, &ctx->cmdPool);
    if (res != VK_SUCCESS) {
        vkDestroyDevice(ctx->device, NULL);
        vkDestroyInstance(ctx->instance, NULL);
        return (int)res;
    }

    return 0;
}

static void destroy_vulkan_context(VkContext* ctx) {
    if (ctx->device) {
        if (ctx->cmdPool) {
            vkDestroyCommandPool(ctx->device, ctx->cmdPool, NULL);
            ctx->cmdPool = NULL;
        }
        vkDestroyDevice(ctx->device, NULL);
        ctx->device = NULL;
    }
    if (ctx->instance) {
        vkDestroyInstance(ctx->instance, NULL);
        ctx->instance = NULL;
    }
}

static int import_dmabuf_buffer(VkContext* ctx, int fd, uint32_t width, uint32_t height, uint32_t stride, uint64_t modifier, VkImportedBuffer* out) {
    memset(out, 0, sizeof(VkImportedBuffer));
    out->width = width;
    out->height = height;
    out->stride = stride;
    out->modifier = modifier;

    PFN_vkGetMemoryFdPropertiesKHR pfnGetMemoryFdPropertiesKHR =
        (PFN_vkGetMemoryFdPropertiesKHR)vkGetDeviceProcAddr(ctx->device, "vkGetMemoryFdPropertiesKHR");
    if (!pfnGetMemoryFdPropertiesKHR) return -10;

    VkMemoryFdPropertiesKHR fdProps = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_FD_PROPERTIES_KHR,
    };
    VkResult res = pfnGetMemoryFdPropertiesKHR(ctx->device, VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT, fd, &fdProps);
    if (res != VK_SUCCESS) return (int)res;

    // Find suitable memory type index
    VkPhysicalDeviceMemoryProperties memProps;
    vkGetPhysicalDeviceMemoryProperties(ctx->pdev, &memProps);

    int memTypeIndex = -1;
    for (uint32_t i = 0; i < memProps.memoryTypeCount; i++) {
        if (fdProps.memoryTypeBits & (1 << i)) {
            memTypeIndex = (int)i;
            break;
        }
    }
    if (memTypeIndex < 0) return -11;

    // Create VkImage for the DMA-BUF
    VkSubresourceLayout planeLayout = {
        .offset = 0,
        .rowPitch = stride,
        .size = 0,
    };

    VkImageDrmFormatModifierExplicitCreateInfoEXT modInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_DRM_FORMAT_MODIFIER_EXPLICIT_CREATE_INFO_EXT,
        .drmFormatModifierPlaneCount = 1,
        .drmFormatModifier = modifier,
        .pPlaneLayouts = &planeLayout,
    };

    VkExternalMemoryImageCreateInfo extImageInfo = {
        .sType = VK_STRUCTURE_TYPE_EXTERNAL_MEMORY_IMAGE_CREATE_INFO,
        .pNext = &modInfo,
        .handleTypes = VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT,
    };

    VkImageCreateInfo imgInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO,
        .pNext = &extImageInfo,
        .imageType = VK_IMAGE_TYPE_2D,
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .extent = { width, height, 1 },
        .mipLevels = 1,
        .arrayLayers = 1,
        .samples = VK_SAMPLE_COUNT_1_BIT,
        .tiling = VK_IMAGE_TILING_DRM_FORMAT_MODIFIER_EXT,
        .usage = VK_IMAGE_USAGE_SAMPLED_BIT | VK_IMAGE_USAGE_TRANSFER_SRC_BIT,
        .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
        .initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
    };

    res = vkCreateImage(ctx->device, &imgInfo, NULL, &out->image);
    if (res != VK_SUCCESS) {
        return (int)res;
    }

    int dupFd = fcntl(fd, F_DUPFD_CLOEXEC, 0);
    if (dupFd < 0) {
        vkDestroyImage(ctx->device, out->image, NULL);
        return -12;
    }

    VkMemoryRequirements memReqs;
    vkGetImageMemoryRequirements(ctx->device, out->image, &memReqs);

    // Import device memory from FD
    VkImportMemoryFdInfoKHR importInfo = {
        .sType = VK_STRUCTURE_TYPE_IMPORT_MEMORY_FD_INFO_KHR,
        .handleType = VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT,
        .fd = dupFd,
    };

    VkMemoryDedicatedAllocateInfo dedAlloc = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_DEDICATED_ALLOCATE_INFO,
        .pNext = &importInfo,
        .image = out->image,
    };

    VkMemoryAllocateInfo allocInfo = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
        .pNext = &dedAlloc,
        .allocationSize = memReqs.size,
        .memoryTypeIndex = (uint32_t)memTypeIndex,
    };

    res = vkAllocateMemory(ctx->device, &allocInfo, NULL, &out->memory);
    if (res != VK_SUCCESS) {
        close(dupFd);
        vkDestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    res = vkBindImageMemory(ctx->device, out->image, out->memory, 0);
    if (res != VK_SUCCESS) {
        vkFreeMemory(ctx->device, out->memory, NULL);
        vkDestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    VkImageViewCreateInfo viewInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO,
        .image = out->image,
        .viewType = VK_IMAGE_VIEW_TYPE_2D,
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .components = {
            .r = VK_COMPONENT_SWIZZLE_IDENTITY,
            .g = VK_COMPONENT_SWIZZLE_IDENTITY,
            .b = VK_COMPONENT_SWIZZLE_IDENTITY,
            .a = VK_COMPONENT_SWIZZLE_IDENTITY,
        },
        .subresourceRange = {
            .aspectMask = VK_IMAGE_ASPECT_COLOR_BIT,
            .baseMipLevel = 0,
            .levelCount = 1,
            .baseArrayLayer = 0,
            .layerCount = 1,
        },
    };

    res = vkCreateImageView(ctx->device, &viewInfo, NULL, &out->view);
    if (res != VK_SUCCESS) {
        vkFreeMemory(ctx->device, out->memory, NULL);
        vkDestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    return 0;
}

static void destroy_imported_buffer(VkContext* ctx, VkImportedBuffer* buf) {
    if (buf->view) {
        vkDestroyImageView(ctx->device, buf->view, NULL);
        buf->view = NULL;
    }
    if (buf->image) {
        vkDestroyImage(ctx->device, buf->image, NULL);
        buf->image = NULL;
    }
    if (buf->memory) {
        vkFreeMemory(ctx->device, buf->memory, NULL);
        buf->memory = NULL;
    }
}
*/
import "C"
import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Device manages a Vulkan 1.3+ instance, AMD/Intel/NVIDIA GPU device, and graphics queue.
type Device struct {
	ctx        C.VkContext
	deviceName string
	closed     atomic.Bool
	mu         sync.Mutex
}

// ImportedBuffer tracks a DMA-BUF imported into Vulkan memory.
type ImportedBuffer struct {
	buf      C.VkImportedBuffer
	Width    uint32
	Height   uint32
	Stride   uint32
	Modifier uint64
}

// NewDevice creates and initializes a hardware-accelerated Vulkan device with DMA-BUF support.
func NewDevice() (*Device, error) {
	var ctx C.VkContext
	res := C.init_vulkan_context(&ctx)
	if res != 0 {
		return nil, fmt.Errorf("failed to initialize Vulkan context (code %d)", int(res))
	}

	devName := C.GoString(&ctx.deviceName[0])

	return &Device{
		ctx:        ctx,
		deviceName: devName,
	}, nil
}

// DeviceName returns the hardware GPU model identified by the Vulkan driver.
func (d *Device) DeviceName() string {
	return d.deviceName
}

// ImportDmaBufInto imports an external Linux DMA-BUF into an existing ImportedBuffer struct.
// Enables 0-allocation hot path buffer imports.
func (d *Device) ImportDmaBufInto(dst *ImportedBuffer, fd int, width, height, stride uint32, modifier uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed.Load() {
		return fmt.Errorf("device is closed")
	}

	var buf C.VkImportedBuffer
	res := C.import_dmabuf_buffer(
		&d.ctx,
		C.int(fd),
		C.uint32_t(width),
		C.uint32_t(height),
		C.uint32_t(stride),
		C.uint64_t(modifier),
		&buf,
	)

	if res != 0 {
		return fmt.Errorf("import_dmabuf_buffer failed (code %d)", int(res))
	}

	dst.buf = buf
	dst.Width = width
	dst.Height = height
	dst.Stride = stride
	dst.Modifier = modifier
	return nil
}

// ImportDmaBuf imports an external Linux DMA-BUF file descriptor into a Vulkan device memory allocation.
// Zero-copy transfer directly into GPU address space.
func (d *Device) ImportDmaBuf(fd int, width, height, stride uint32, modifier uint64) (*ImportedBuffer, error) {
	var buf ImportedBuffer
	if err := d.ImportDmaBufInto(&buf, fd, width, height, stride, modifier); err != nil {
		return nil, err
	}
	return &buf, nil
}

// DestroyImportedBuffer releases Vulkan resources associated with an imported DMA-BUF.
func (d *Device) DestroyImportedBuffer(b *ImportedBuffer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if b != nil {
		buf := b.buf
		C.destroy_imported_buffer(&d.ctx, &buf)
		b.buf = C.VkImportedBuffer{}
	}
}

// Close destroys the Vulkan device, command pools, and instance.
func (d *Device) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	C.destroy_vulkan_context(&d.ctx)
	return nil
}
