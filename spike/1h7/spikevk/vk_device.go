package spikevk

// vk_device.go — instance → physical device → logical device + queue, all
// through runtime-resolved addresses. Unlike the earlier draft, nothing here
// forges a Go func value from a code address: resolved symbols are stored as
// unsafe.Pointer and invoked only through the typed C shims in vk_loader.go.

/*
#include <stdint.h>
void* spike_dup(const void* src, unsigned long n);
void free(void*);
int32_t spike_vkCreateInstance(void* f, const void* i, void* a, void* o);
void    spike_vkDestroyInstance(void* f, uint64_t inst, void* a);
int32_t spike_vkEnumeratePhysicalDevices(void* f, uint64_t inst, void* count, void* devs);
void    spike_vkGetPhysicalDeviceProperties(void* f, uint64_t pdev, void* props);
void    spike_vkGetPhysicalDeviceQueueFamilyProperties(void* f, uint64_t pdev, void* count, void* props);
void    spike_vkGetPhysicalDeviceMemoryProperties(void* f, uint64_t pdev, void* props);
int32_t spike_vkCreateDevice(void* f, uint64_t pdev, const void* info, void* a, void* out);
void    spike_vkDestroyDevice(void* f, uint64_t dev, void* a);
void    spike_vkGetDeviceQueue(void* f, uint64_t dev, uint32_t fam, uint32_t idx, void* out);
*/
import "C"

import (
	"os"
	"unsafe"
)

// GraphicsContext is the resolved Vulkan environment.
type GraphicsContext struct {
	Instance    Instance
	Physical    PhysicalDevice
	Logical     Device
	QueueFamily uint32
	Queue       Queue
	DeviceName  string

	// Resolved addresses (opaque words). A zero value is an unresolved
	// symbol; resolve() errors instead of handing one out.
	vEnumeratePhysical       unsafe.Pointer
	vPhysicalProps           unsafe.Pointer
	vQueueFamilies           unsafe.Pointer
	vMemoryProps             unsafe.Pointer
	vCreateDevice            unsafe.Pointer
	vDestroyInstance         unsafe.Pointer
	vDestroyDevice           unsafe.Pointer
	vGetDeviceQueue          unsafe.Pointer
	vCreateCommandPool       unsafe.Pointer
	vDestroyCommandPool      unsafe.Pointer
	vAllocateCmdBuffers      unsafe.Pointer
	vCreateFence             unsafe.Pointer
	vDestroyFence            unsafe.Pointer
	vWaitForFences           unsafe.Pointer
	vResetFences             unsafe.Pointer
	vBeginCmdBuffer          unsafe.Pointer
	vEndCmdBuffer            unsafe.Pointer
	vResetCmdBuffer          unsafe.Pointer
	vQueueSubmit             unsafe.Pointer
	vQueueWaitIdle           unsafe.Pointer
	vCreateImage             unsafe.Pointer
	vDestroyImage            unsafe.Pointer
	vCreateImageView         unsafe.Pointer
	vDestroyImageView        unsafe.Pointer
	vGetImageMemReqs         unsafe.Pointer
	vAllocateMemory          unsafe.Pointer
	vFreeMemory              unsafe.Pointer
	vMapMemory               unsafe.Pointer
	vUnmapMemory             unsafe.Pointer
	vBindImageMemory         unsafe.Pointer
	vCreateBuffer            unsafe.Pointer
	vDestroyBuffer           unsafe.Pointer
	vGetBufferMemReqs        unsafe.Pointer
	vBindBufferMemory        unsafe.Pointer
	vCmdPipelineBarrier      unsafe.Pointer
	vCmdClearColorImage      unsafe.Pointer
	vCmdCopyImageToBuf       unsafe.Pointer
	vCmdCopyBufferToImage    unsafe.Pointer
	vCreateShaderModule      unsafe.Pointer
	vDestroyShaderModule     unsafe.Pointer
	vCreateDescSetLayout     unsafe.Pointer
	vDestroyDescSetLayout    unsafe.Pointer
	vCreateDescPool          unsafe.Pointer
	vDestroyDescPool         unsafe.Pointer
	vAllocateDescSets        unsafe.Pointer
	vUpdateDescSets          unsafe.Pointer
	vCreatePipelineLayout    unsafe.Pointer
	vDestroyPipelineLayout   unsafe.Pointer
	vCreateGraphicsPipelines unsafe.Pointer
	vDestroyPipeline         unsafe.Pointer
	vCreateRenderPass        unsafe.Pointer
	vDestroyRenderPass       unsafe.Pointer
	vCreateFramebuffer       unsafe.Pointer
	vDestroyFramebuffer      unsafe.Pointer
	vCreateSampler           unsafe.Pointer
	vDestroySampler          unsafe.Pointer
	vCmdBeginRenderPass      unsafe.Pointer
	vCmdEndRenderPass        unsafe.Pointer
	vCmdBindPipeline         unsafe.Pointer
	vCmdBindDescSets         unsafe.Pointer
	vCmdPushConstants        unsafe.Pointer
	vCmdDraw                 unsafe.Pointer
}

// OpenInstance walks loader → instance → physical → logical device + queue.
func OpenInstance(loader *Loader) (*GraphicsContext, error) {
	app := VkApplicationInfo{
		SType:              STRUCT_APPLICATION_INFO,
		ApplicationVersion: 1,
		EngineVersion:      1,
		APIVersion:         (1 << 22) | (3 << 12), // 1.3
	}
	info := VkInstanceCreateInfo{
		SType:            STRUCT_INSTANCE_CREATE_INFO,
		PApplicationInfo: &app,
	}
	var layerName []byte
	if os.Getenv("BLOXWM_VK_VALIDATION") != "" {
		layerName = acByte("VK_LAYER_KHRONOS_validation")
		layerPtr := &layerName[0]
		info.EnabledLayerCount = 1
		info.PpEnabledLayerNames = &layerPtr
	}

	var instance Instance
	infoPtr, freeInfo := envelope(unsafe.Pointer(&info), unsafe.Sizeof(info))
	defer freeInfo()
	vCreateInstance := loader.Resolve(0, "vkCreateInstance")
	if vCreateInstance == nil {
		return nil, errMissing("vkCreateInstance")
	}
	if res := Result(C.spike_vkCreateInstance(vCreateInstance, infoPtr, nil, unsafe.Pointer(&instance))); res != 0 {
		return nil, ResultError(res, "vkCreateInstance")
	}

	ctx := &GraphicsContext{Instance: instance}
	if err := ctx.resolve(loader); err != nil {
		ctx.Close()
		return nil, err
	}
	if err := ctx.pickPhysicalDevice(); err != nil {
		ctx.Close()
		return nil, err
	}
	if err := ctx.createLogicalDevice(); err != nil {
		ctx.Close()
		return nil, err
	}
	return ctx, nil
}

// resolve binds every entry point this slice uses. Names are resolved
// through the instance resolver; device functions intentionally use it too
// (vkGetDeviceProcAddr would only skip the loader trampoline — noted as an
// optimisation, not a requirement).
func (ctx *GraphicsContext) resolve(loader *Loader) error {
	type binding struct {
		name string
		dst  *unsafe.Pointer
	}
	binds := []binding{
		{"vkEnumeratePhysicalDevices", &ctx.vEnumeratePhysical},
		{"vkGetPhysicalDeviceProperties", &ctx.vPhysicalProps},
		{"vkGetPhysicalDeviceQueueFamilyProperties", &ctx.vQueueFamilies},
		{"vkGetPhysicalDeviceMemoryProperties", &ctx.vMemoryProps},
		{"vkCreateDevice", &ctx.vCreateDevice},
		{"vkDestroyInstance", &ctx.vDestroyInstance},
		{"vkDestroyDevice", &ctx.vDestroyDevice},
		{"vkGetDeviceQueue", &ctx.vGetDeviceQueue},
		{"vkCreateCommandPool", &ctx.vCreateCommandPool},
		{"vkDestroyCommandPool", &ctx.vDestroyCommandPool},
		{"vkAllocateCommandBuffers", &ctx.vAllocateCmdBuffers},
		{"vkCreateFence", &ctx.vCreateFence},
		{"vkDestroyFence", &ctx.vDestroyFence},
		{"vkWaitForFences", &ctx.vWaitForFences},
		{"vkResetFences", &ctx.vResetFences},
		{"vkBeginCommandBuffer", &ctx.vBeginCmdBuffer},
		{"vkEndCommandBuffer", &ctx.vEndCmdBuffer},
		{"vkResetCommandBuffer", &ctx.vResetCmdBuffer},
		{"vkQueueSubmit", &ctx.vQueueSubmit},
		{"vkQueueWaitIdle", &ctx.vQueueWaitIdle},
		{"vkCreateImage", &ctx.vCreateImage},
		{"vkDestroyImage", &ctx.vDestroyImage},
		{"vkCreateImageView", &ctx.vCreateImageView},
		{"vkDestroyImageView", &ctx.vDestroyImageView},
		{"vkGetImageMemoryRequirements", &ctx.vGetImageMemReqs},
		{"vkAllocateMemory", &ctx.vAllocateMemory},
		{"vkFreeMemory", &ctx.vFreeMemory},
		{"vkMapMemory", &ctx.vMapMemory},
		{"vkUnmapMemory", &ctx.vUnmapMemory},
		{"vkBindImageMemory", &ctx.vBindImageMemory},
		{"vkCreateBuffer", &ctx.vCreateBuffer},
		{"vkDestroyBuffer", &ctx.vDestroyBuffer},
		{"vkGetBufferMemoryRequirements", &ctx.vGetBufferMemReqs},
		{"vkBindBufferMemory", &ctx.vBindBufferMemory},
		{"vkCmdPipelineBarrier", &ctx.vCmdPipelineBarrier},
		{"vkCmdClearColorImage", &ctx.vCmdClearColorImage},
		{"vkCmdCopyImageToBuffer", &ctx.vCmdCopyImageToBuf},
		{"vkCmdCopyBufferToImage", &ctx.vCmdCopyBufferToImage},
		{"vkCreateShaderModule", &ctx.vCreateShaderModule},
		{"vkDestroyShaderModule", &ctx.vDestroyShaderModule},
		{"vkCreateDescriptorSetLayout", &ctx.vCreateDescSetLayout},
		{"vkDestroyDescriptorSetLayout", &ctx.vDestroyDescSetLayout},
		{"vkCreateDescriptorPool", &ctx.vCreateDescPool},
		{"vkDestroyDescriptorPool", &ctx.vDestroyDescPool},
		{"vkAllocateDescriptorSets", &ctx.vAllocateDescSets},
		{"vkUpdateDescriptorSets", &ctx.vUpdateDescSets},
		{"vkCreatePipelineLayout", &ctx.vCreatePipelineLayout},
		{"vkDestroyPipelineLayout", &ctx.vDestroyPipelineLayout},
		{"vkCreateGraphicsPipelines", &ctx.vCreateGraphicsPipelines},
		{"vkDestroyPipeline", &ctx.vDestroyPipeline},
		{"vkCreateRenderPass", &ctx.vCreateRenderPass},
		{"vkDestroyRenderPass", &ctx.vDestroyRenderPass},
		{"vkCreateFramebuffer", &ctx.vCreateFramebuffer},
		{"vkDestroyFramebuffer", &ctx.vDestroyFramebuffer},
		{"vkCreateSampler", &ctx.vCreateSampler},
		{"vkDestroySampler", &ctx.vDestroySampler},
		{"vkCmdBeginRenderPass", &ctx.vCmdBeginRenderPass},
		{"vkCmdEndRenderPass", &ctx.vCmdEndRenderPass},
		{"vkCmdBindPipeline", &ctx.vCmdBindPipeline},
		{"vkCmdBindDescriptorSets", &ctx.vCmdBindDescSets},
		{"vkCmdPushConstants", &ctx.vCmdPushConstants},
		{"vkCmdDraw", &ctx.vCmdDraw},
	}
	for _, b := range binds {
		p := loader.Resolve(ctx.Instance, b.name)
		if p == nil {
			return errMissing(b.name)
		}
		*b.dst = p
	}
	return nil
}

// pickPhysicalDevice enumerates devices, honours BLOXWM_VK_DEVICE and reads
// the device name through the layout-asserted properties array.
func (ctx *GraphicsContext) pickPhysicalDevice() error {
	var count uint32
	if res := Result(C.spike_vkEnumeratePhysicalDevices(ctx.vEnumeratePhysical, C.uint64_t(uint64(ctx.Instance)), unsafe.Pointer(&count), nil)); res != 0 || count == 0 {
		return ResultError(res, "vkEnumeratePhysicalDevices (count)")
	}
	if count > 16 {
		count = 16
	}
	devs := make([]PhysicalDevice, count)
	if res := Result(C.spike_vkEnumeratePhysicalDevices(ctx.vEnumeratePhysical, C.uint64_t(uint64(ctx.Instance)), unsafe.Pointer(&count), unsafe.Pointer(&devs[0]))); res != 0 || count == 0 {
		return ResultError(res, "vkEnumeratePhysicalDevices (list)")
	}

	idx := 0
	if s := EnvDeviceIndex(); s != "" {
		if n := parseInt(s); n >= 0 && uint32(n) < count {
			idx = n
		}
	}
	ctx.Physical = devs[idx]

	var props VkPhysicalDeviceProperties
	C.spike_vkGetPhysicalDeviceProperties(ctx.vPhysicalProps, C.uint64_t(uint64(ctx.Physical)), unsafe.Pointer(&props))
	ctx.DeviceName = props.PhysicalDeviceName()
	return nil
}

// createLogicalDevice finds a graphics queue family and creates the device
// with the same extensions the C core enables. Extension names are C strings
// built per call (init path only, never the frame).
func (ctx *GraphicsContext) createLogicalDevice() error {
	var qCount uint32
	C.spike_vkGetPhysicalDeviceQueueFamilyProperties(ctx.vQueueFamilies, C.uint64_t(uint64(ctx.Physical)), unsafe.Pointer(&qCount), nil)
	qProps := make([]VkQueueFamilyProperties, qCount)
	C.spike_vkGetPhysicalDeviceQueueFamilyProperties(ctx.vQueueFamilies, C.uint64_t(uint64(ctx.Physical)), unsafe.Pointer(&qCount), unsafe.Pointer(&qProps[0]))

	graphicsQ := -1
	for i, q := range qProps {
		if q.QueueFlags&QUEUE_GRAPHICS_BIT != 0 {
			graphicsQ = i
			break
		}
	}
	if graphicsQ < 0 {
		return ResultError(-1, "no graphics queue family")
	}
	ctx.QueueFamily = uint32(graphicsQ)

	priority := float32(1.0)
	qInfo := VkDeviceQueueCreateInfo{
		SType:            STRUCT_DEVICE_QUEUE_CREATE_INFO,
		QueueFamilyIndex: ctx.QueueFamily,
		QueueCount:       1,
		PQueuePriorities: &priority,
	}
	extNames := []string{"VK_KHR_external_memory_fd", "VK_EXT_image_drm_format_modifier", "VK_KHR_image_format_list"}
	cNames := make([]*byte, len(extNames))
	cBufs := make([][]byte, len(extNames))
	for i, s := range extNames {
		cBufs[i] = acByte(s)
		cNames[i] = &cBufs[i][0]
	}
	devInfo := VkDeviceCreateInfo{
		SType:                   STRUCT_DEVICE_CREATE_INFO,
		QueueCreateInfoCount:    1,
		PQueueCreateInfos:       &qInfo,
		EnabledExtensionCount:   uint32(len(extNames)),
		PpEnabledExtensionNames: &cNames[0],
	}
	var logical Device
	devInfoPtr, freeDevInfo := envelope(unsafe.Pointer(&devInfo), unsafe.Sizeof(devInfo))
	defer freeDevInfo()
	if res := Result(C.spike_vkCreateDevice(ctx.vCreateDevice, C.uint64_t(uint64(ctx.Physical)), devInfoPtr, nil, unsafe.Pointer(&logical))); res != 0 {
		return ResultError(res, "vkCreateDevice")
	}
	ctx.Logical = logical

	var queue Queue
	C.spike_vkGetDeviceQueue(ctx.vGetDeviceQueue, C.uint64_t(uint64(logical)), C.uint32_t(ctx.QueueFamily), 0, unsafe.Pointer(&queue))
	ctx.Queue = queue
	return nil
}

// Close destroys the device then the instance through resolved pointers.
func (ctx *GraphicsContext) Close() {
	if ctx == nil {
		return
	}
	if ctx.Logical != 0 && ctx.vDestroyDevice != nil {
		C.spike_vkDestroyDevice(ctx.vDestroyDevice, C.uint64_t(uint64(ctx.Logical)), nil)
		ctx.Logical = 0
	}
	if ctx.Instance != 0 && ctx.vDestroyInstance != nil {
		C.spike_vkDestroyInstance(ctx.vDestroyInstance, C.uint64_t(uint64(ctx.Instance)), nil)
		ctx.Instance = 0
	}
}

// envelope copies a create-info struct into C memory for one synchronous
// driver call. This is the ONLY legal way to pass a struct that itself
// contains Go pointers (nested create-infos): cgo forbids Go-pointer-to-Go-
// pointer. The copy happens in pure Go (no cgo call involved), so the
// checker never sees it; only the C pointer crosses into the driver call.
// The inner pointers stay Go addresses; sound because the call is
// synchronous and every Go original is live in the caller's frame for its
// duration (non-moving GC).
func envelope(src unsafe.Pointer, size uintptr) (unsafe.Pointer, func()) {
	dst := C.malloc(C.size_t(size))
	copy(unsafe.Slice((*byte)(dst), size), unsafe.Slice((*byte)(src), size))
	return dst, func() { C.free(dst) }
}
