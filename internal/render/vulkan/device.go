package vulkan

/*
#include "vk_common.h"
#include <fcntl.h>
#include <unistd.h>

static void* vk_dlopen(const char* name) {
    return dlopen(name, RTLD_NOW | RTLD_LOCAL);
}

static void* vk_dlsym(void* handle, const char* symbol) {
    return dlsym(handle, symbol);
}

static int vk_dlclose(void* handle) {
    return dlclose(handle);
}

#define RESOLVE_INST(ctx, fn) do { \
    (ctx)->procs.fn = (PFN_vk##fn)(ctx)->procs.GetInstanceProcAddr((ctx)->instance, "vk" #fn); \
    if (!(ctx)->procs.fn) return -100; \
} while (0)

#define RESOLVE_DEV(ctx, fn) do { \
    (ctx)->procs.fn = (PFN_vk##fn)(ctx)->procs.GetDeviceProcAddr((ctx)->device, "vk" #fn); \
    if (!(ctx)->procs.fn) { \
        (ctx)->procs.fn = (PFN_vk##fn)(ctx)->procs.GetInstanceProcAddr((ctx)->instance, "vk" #fn); \
    } \
    if (!(ctx)->procs.fn) return -101; \
} while (0)

static int init_vulkan_context_with_gipa(VkContext* ctx, void* libHandle, void* gipa) {
    memset(ctx, 0, sizeof(VkContext));
    ctx->libHandle = libHandle;
    ctx->procs.GetInstanceProcAddr = (PFN_vkGetInstanceProcAddr)gipa;
    if (!ctx->procs.GetInstanceProcAddr) return -99;

    ctx->procs.CreateInstance = (PFN_vkCreateInstance)ctx->procs.GetInstanceProcAddr(NULL, "vkCreateInstance");
    if (!ctx->procs.CreateInstance) return -98;

    VkApplicationInfo appInfo = {
        .sType = VK_STRUCTURE_TYPE_APPLICATION_INFO,
        .pApplicationName = "stratifi-vulkan",
        .applicationVersion = VK_MAKE_VERSION(0, 1, 0),
        .pEngineName = "stratifi",
        .engineVersion = VK_MAKE_VERSION(0, 1, 0),
        .apiVersion = VK_API_VERSION_1_3,
    };

    const char* validationLayer = "VK_LAYER_KHRONOS_validation";
    VkInstanceCreateInfo instInfo = {
        .sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO,
        .pApplicationInfo = &appInfo,
    };
    if (getenv("STRATIFI_VK_VALIDATION") != NULL) {
        instInfo.enabledLayerCount = 1;
        instInfo.ppEnabledLayerNames = &validationLayer;
    }

    VkResult res = ctx->procs.CreateInstance(&instInfo, NULL, &ctx->instance);
    if (res != VK_SUCCESS) return (int)res;

    RESOLVE_INST(ctx, DestroyInstance);
    RESOLVE_INST(ctx, EnumeratePhysicalDevices);
    RESOLVE_INST(ctx, GetPhysicalDeviceProperties);
    RESOLVE_INST(ctx, GetPhysicalDeviceQueueFamilyProperties);
    RESOLVE_INST(ctx, GetPhysicalDeviceMemoryProperties);
    RESOLVE_INST(ctx, CreateDevice);
    RESOLVE_INST(ctx, GetDeviceProcAddr);

    uint32_t devCount = 0;
    res = ctx->procs.EnumeratePhysicalDevices(ctx->instance, &devCount, NULL);
    if (res != VK_SUCCESS || devCount == 0) {
        ctx->procs.DestroyInstance(ctx->instance, NULL);
        return -1;
    }
    VkPhysicalDevice devices[16];
    if (devCount > 16) devCount = 16;
    res = ctx->procs.EnumeratePhysicalDevices(ctx->instance, &devCount, devices);
    if (res != VK_SUCCESS || devCount == 0) {
        ctx->procs.DestroyInstance(ctx->instance, NULL);
        return -1;
    }
    int devIdx = 0;
    const char* sel = getenv("STRATIFI_VK_DEVICE");
    if (sel) devIdx = atoi(sel);
    if (devIdx < 0 || devIdx >= (int)devCount) devIdx = 0;
    ctx->pdev = devices[devIdx];

    VkPhysicalDeviceProperties props;
    ctx->procs.GetPhysicalDeviceProperties(ctx->pdev, &props);
    strncpy(ctx->deviceName, props.deviceName, sizeof(ctx->deviceName) - 1);

    // Find graphics queue
    uint32_t qCount = 0;
    ctx->procs.GetPhysicalDeviceQueueFamilyProperties(ctx->pdev, &qCount, NULL);
    VkQueueFamilyProperties qProps[qCount];
    ctx->procs.GetPhysicalDeviceQueueFamilyProperties(ctx->pdev, &qCount, qProps);

    int graphicsQ = -1;
    for (uint32_t i = 0; i < qCount; i++) {
        if (qProps[i].queueFlags & VK_QUEUE_GRAPHICS_BIT) {
            graphicsQ = (int)i;
            break;
        }
    }
    if (graphicsQ < 0) {
        ctx->procs.DestroyInstance(ctx->instance, NULL);
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

    res = ctx->procs.CreateDevice(ctx->pdev, &devInfo, NULL, &ctx->device);
    if (res != VK_SUCCESS) {
        ctx->procs.DestroyInstance(ctx->instance, NULL);
        return (int)res;
    }

    RESOLVE_DEV(ctx, DestroyDevice);
    RESOLVE_DEV(ctx, GetDeviceQueue);
    RESOLVE_DEV(ctx, DeviceWaitIdle);
    RESOLVE_DEV(ctx, CreateCommandPool);
    RESOLVE_DEV(ctx, DestroyCommandPool);
    RESOLVE_DEV(ctx, AllocateCommandBuffers);
    RESOLVE_DEV(ctx, ResetCommandBuffer);
    RESOLVE_DEV(ctx, BeginCommandBuffer);
    RESOLVE_DEV(ctx, EndCommandBuffer);
    RESOLVE_DEV(ctx, QueueSubmit);
    RESOLVE_DEV(ctx, CreateFence);
    RESOLVE_DEV(ctx, DestroyFence);
    RESOLVE_DEV(ctx, WaitForFences);
    RESOLVE_DEV(ctx, ResetFences);
    RESOLVE_DEV(ctx, CreateImage);
    RESOLVE_DEV(ctx, DestroyImage);
    RESOLVE_DEV(ctx, GetImageMemoryRequirements);
    RESOLVE_DEV(ctx, AllocateMemory);
    RESOLVE_DEV(ctx, FreeMemory);
    RESOLVE_DEV(ctx, MapMemory);
    RESOLVE_DEV(ctx, UnmapMemory);
    RESOLVE_DEV(ctx, BindImageMemory);
    RESOLVE_DEV(ctx, CreateImageView);
    RESOLVE_DEV(ctx, DestroyImageView);
    RESOLVE_DEV(ctx, CreateBuffer);
    RESOLVE_DEV(ctx, DestroyBuffer);
    RESOLVE_DEV(ctx, GetBufferMemoryRequirements);
    RESOLVE_DEV(ctx, BindBufferMemory);
    RESOLVE_DEV(ctx, CreateRenderPass);
    RESOLVE_DEV(ctx, DestroyRenderPass);
    RESOLVE_DEV(ctx, CreateFramebuffer);
    RESOLVE_DEV(ctx, DestroyFramebuffer);
    RESOLVE_DEV(ctx, CreateSampler);
    RESOLVE_DEV(ctx, DestroySampler);
    RESOLVE_DEV(ctx, CreateDescriptorPool);
    RESOLVE_DEV(ctx, DestroyDescriptorPool);
    RESOLVE_DEV(ctx, CreateDescriptorSetLayout);
    RESOLVE_DEV(ctx, DestroyDescriptorSetLayout);
    RESOLVE_DEV(ctx, AllocateDescriptorSets);
    RESOLVE_DEV(ctx, UpdateDescriptorSets);
    RESOLVE_DEV(ctx, CreateShaderModule);
    RESOLVE_DEV(ctx, DestroyShaderModule);
    RESOLVE_DEV(ctx, CreatePipelineLayout);
    RESOLVE_DEV(ctx, DestroyPipelineLayout);
    RESOLVE_DEV(ctx, CreateGraphicsPipelines);
    RESOLVE_DEV(ctx, DestroyPipeline);
    RESOLVE_DEV(ctx, CmdBeginRenderPass);
    RESOLVE_DEV(ctx, CmdEndRenderPass);
    RESOLVE_DEV(ctx, CmdSetViewport);
    RESOLVE_DEV(ctx, CmdSetScissor);
    RESOLVE_DEV(ctx, CmdBindPipeline);
    RESOLVE_DEV(ctx, CmdBindDescriptorSets);
    RESOLVE_DEV(ctx, CmdPushConstants);
    RESOLVE_DEV(ctx, CmdDraw);
    RESOLVE_DEV(ctx, CmdPipelineBarrier);
    RESOLVE_DEV(ctx, CmdCopyBufferToImage);
    RESOLVE_DEV(ctx, CmdCopyImageToBuffer);
    ctx->procs.GetMemoryFdPropertiesKHR = (PFN_vkGetMemoryFdPropertiesKHR)ctx->procs.GetDeviceProcAddr(ctx->device, "vkGetMemoryFdPropertiesKHR");

    ctx->procs.GetDeviceQueue(ctx->device, ctx->queueFamily, 0, &ctx->queue);

    // Create command pool
    VkCommandPoolCreateInfo poolInfo = {
        .sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO,
        .flags = VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT,
        .queueFamilyIndex = ctx->queueFamily,
    };
    res = ctx->procs.CreateCommandPool(ctx->device, &poolInfo, NULL, &ctx->cmdPool);
    if (res != VK_SUCCESS) {
        ctx->procs.DestroyDevice(ctx->device, NULL);
        ctx->procs.DestroyInstance(ctx->instance, NULL);
        return (int)res;
    }

    return 0;
}

static void destroy_vulkan_context(VkContext* ctx) {
    if (ctx->device) {
        if (ctx->cmdPool && ctx->procs.DestroyCommandPool) {
            ctx->procs.DestroyCommandPool(ctx->device, ctx->cmdPool, NULL);
            ctx->cmdPool = NULL;
        }
        if (ctx->procs.DestroyDevice) {
            ctx->procs.DestroyDevice(ctx->device, NULL);
        }
        ctx->device = NULL;
    }
    if (ctx->instance) {
        if (ctx->procs.DestroyInstance) {
            ctx->procs.DestroyInstance(ctx->instance, NULL);
        }
        ctx->instance = NULL;
    }
}

static int import_dmabuf_impl(VkContext* ctx, int fd, uint32_t width, uint32_t height, uint32_t stride, uint64_t modifier, VkImageUsageFlags usage, VkImportedBuffer* out) {
    memset(out, 0, sizeof(VkImportedBuffer));
    out->width = width;
    out->height = height;
    out->stride = stride;
    out->modifier = modifier;

    if (!ctx->procs.GetMemoryFdPropertiesKHR) return -10;

    VkMemoryFdPropertiesKHR fdProps = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_FD_PROPERTIES_KHR,
    };
    VkResult res = ctx->procs.GetMemoryFdPropertiesKHR(ctx->device, VK_EXTERNAL_MEMORY_HANDLE_TYPE_DMA_BUF_BIT_EXT, fd, &fdProps);
    if (res != VK_SUCCESS) return (int)res;

    // Find suitable memory type index
    VkPhysicalDeviceMemoryProperties memProps;
    ctx->procs.GetPhysicalDeviceMemoryProperties(ctx->pdev, &memProps);

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
        .usage = usage,
        .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
        .initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
    };

    res = ctx->procs.CreateImage(ctx->device, &imgInfo, NULL, &out->image);
    if (res != VK_SUCCESS) {
        return (int)res;
    }

    int dupFd = fcntl(fd, F_DUPFD_CLOEXEC, 0);
    if (dupFd < 0) {
        ctx->procs.DestroyImage(ctx->device, out->image, NULL);
        return -12;
    }

    VkMemoryRequirements memReqs;
    ctx->procs.GetImageMemoryRequirements(ctx->device, out->image, &memReqs);

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

    res = ctx->procs.AllocateMemory(ctx->device, &allocInfo, NULL, &out->memory);
    if (res != VK_SUCCESS) {
        close(dupFd);
        ctx->procs.DestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    res = ctx->procs.BindImageMemory(ctx->device, out->image, out->memory, 0);
    if (res != VK_SUCCESS) {
        ctx->procs.FreeMemory(ctx->device, out->memory, NULL);
        ctx->procs.DestroyImage(ctx->device, out->image, NULL);
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

    res = ctx->procs.CreateImageView(ctx->device, &viewInfo, NULL, &out->view);
    if (res != VK_SUCCESS) {
        ctx->procs.FreeMemory(ctx->device, out->memory, NULL);
        ctx->procs.DestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    return 0;
}

// import_dmabuf_buffer imports a dma-buf as a sampled client surface texture.
static int import_dmabuf_buffer(VkContext* ctx, int fd, uint32_t width, uint32_t height, uint32_t stride, uint64_t modifier, VkImportedBuffer* out) {
    return import_dmabuf_impl(ctx, fd, width, height, stride, modifier,
        VK_IMAGE_USAGE_SAMPLED_BIT | VK_IMAGE_USAGE_TRANSFER_SRC_BIT, out);
}

// import_dmabuf_target imports a dma-buf (typically a KMS scanout dumb
// buffer, linear modifier) as a renderable image for direct scanout.
static int import_dmabuf_target(VkContext* ctx, int fd, uint32_t width, uint32_t height, uint32_t stride, uint64_t modifier, VkImportedBuffer* out) {
    return import_dmabuf_impl(ctx, fd, width, height, stride, modifier,
        VK_IMAGE_USAGE_COLOR_ATTACHMENT_BIT | VK_IMAGE_USAGE_TRANSFER_SRC_BIT, out);
}

static void destroy_imported_buffer(VkContext* ctx, VkImportedBuffer* buf) {
    if (buf->view) {
        ctx->procs.DestroyImageView(ctx->device, buf->view, NULL);
        buf->view = NULL;
    }
    if (buf->image) {
        ctx->procs.DestroyImage(ctx->device, buf->image, NULL);
        buf->image = NULL;
    }
    if (buf->memory) {
        ctx->procs.FreeMemory(ctx->device, buf->memory, NULL);
        buf->memory = NULL;
    }
}
*/
import "C"
import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

var defaultVulkanLibraries = []string{"libvulkan.so.1", "libvulkan.so"}

var (
	sharedLoaderHandle unsafe.Pointer
	sharedLoaderGIPA   unsafe.Pointer
	sharedLoaderMu     sync.Mutex
)

func acquireSharedLoader() (unsafe.Pointer, unsafe.Pointer, error) {
	sharedLoaderMu.Lock()
	defer sharedLoaderMu.Unlock()
	if sharedLoaderHandle != nil && sharedLoaderGIPA != nil {
		return sharedLoaderHandle, sharedLoaderGIPA, nil
	}
	h, gipa, err := openVulkanLibrary(defaultVulkanLibraries)
	if err != nil {
		return nil, nil, err
	}
	sharedLoaderHandle = h
	sharedLoaderGIPA = gipa
	return h, gipa, nil
}

func openVulkanLibrary(names []string) (unsafe.Pointer, unsafe.Pointer, error) {
	var lastErr error
	for _, name := range names {
		cname := C.CString(name)
		h := C.vk_dlopen(cname)
		C.free(unsafe.Pointer(cname))
		if h == nil {
			lastErr = fmt.Errorf("%s: not found", name)
			continue
		}
		symName := C.CString("vkGetInstanceProcAddr")
		gipa := C.vk_dlsym(h, symName)
		C.free(unsafe.Pointer(symName))
		if gipa == nil {
			C.vk_dlclose(h)
			lastErr = fmt.Errorf("%s: missing vkGetInstanceProcAddr", name)
			continue
		}
		return h, gipa, nil
	}
	return nil, nil, fmt.Errorf("no vulkan loader available: %w", lastErr)
}

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

// NewDevice creates and initializes a hardware-accelerated Vulkan device with
// runtime-dlopened libvulkan.so.1 and DMA-BUF support.
func NewDevice() (*Device, error) {
	h, gipa, err := acquireSharedLoader()
	if err != nil {
		return nil, err
	}
	return newDeviceWithGIPA(h, gipa)
}

func newDeviceFromLibraries(names []string) (*Device, error) {
	h, gipa, err := openVulkanLibrary(names)
	if err != nil {
		return nil, err
	}
	dev, err := newDeviceWithGIPA(h, gipa)
	if err != nil {
		C.vk_dlclose(h)
		return nil, err
	}
	return dev, nil
}

func newDeviceWithGIPA(h, gipa unsafe.Pointer) (*Device, error) {
	var ctx C.VkContext
	res := C.init_vulkan_context_with_gipa(&ctx, h, gipa)
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

// ImportDmaBufAsTarget imports a dma-buf (KMS scanout dumb buffer, linear)
// as a COLOR_ATTACHMENT image so the compositor can render straight into
// scanout memory. No CPU round trip.
func (d *Device) ImportDmaBufAsTarget(fd int, width, height, stride uint32, modifier uint64) (*ImportedBuffer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed.Load() {
		return nil, fmt.Errorf("device is closed")
	}

	var buf C.VkImportedBuffer
	res := C.import_dmabuf_target(
		&d.ctx,
		C.int(fd),
		C.uint32_t(width),
		C.uint32_t(height),
		C.uint32_t(stride),
		C.uint64_t(modifier),
		&buf,
	)

	if res != 0 {
		return nil, fmt.Errorf("import_dmabuf_target failed (code %d)", int(res))
	}

	return &ImportedBuffer{
		buf:      buf,
		Width:    width,
		Height:   height,
		Stride:   stride,
		Modifier: modifier,
	}, nil
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
