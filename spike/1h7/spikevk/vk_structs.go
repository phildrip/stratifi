package spikevk

// vk_structs.go — Vulkan ABI structs transcribed for linux/amd64.
//
// Every size is asserted in vk_abi_test.go against the C compiler's
// sizeof()/offsetof() dump, so a transcription error fails the build with a
// named offender instead of corrupting the driver's state. Sizes below match
// `sizeof` output from vulkan_core.h on this platform.

import "unsafe"

// Handle is Vulkan's non-dispatchable-handle width on amd64: 64-bit.
type Handle = uint64

// Opaque dispatchable handles are pointers per the spec.
type (
	Instance            = Handle
	PhysicalDevice      = Handle
	Device              = Handle
	Queue               = Handle
	CommandBuffer       = Handle
	Image               = Handle
	DeviceMemory        = Handle
	ImageView           = Handle
	Buffer              = Handle
	CommandPool         = Handle
	Fence               = Handle
	RenderPass          = Handle
	Pipeline            = Handle
	PipelineCache       = Handle
	PipelineLayout      = Handle
	Sampler             = Handle
	ShaderModule        = Handle
	DescriptorSet       = Handle
	DescriptorPool      = Handle
	DescriptorSetLayout = Handle
	Framebuffer         = Handle
	Semaphore           = Handle
)

// Struct type enum values — transcribed from vulkan_core.h, verified by
// TestStructLayoutABI against nothing (it only checks sizes); these numeric
// values were cross-checked against the header dump directly.
const (
	STRUCT_APPLICATION_INFO                  = 0
	STRUCT_INSTANCE_CREATE_INFO              = 1
	STRUCT_DEVICE_QUEUE_CREATE_INFO          = 2
	STRUCT_DEVICE_CREATE_INFO                = 3
	STRUCT_SUBMIT_INFO                       = 4
	STRUCT_MEMORY_ALLOCATE_INFO              = 5
	STRUCT_FENCE_CREATE_INFO                 = 8
	STRUCT_BUFFER_CREATE_INFO                = 12
	STRUCT_IMAGE_CREATE_INFO                 = 14
	STRUCT_IMAGE_VIEW_CREATE_INFO            = 15
	STRUCT_SHADER_MODULE_CREATE_INFO         = 16
	STRUCT_PIPELINE_SHADER_STAGE_CREATE_INFO = 18
	STRUCT_GRAPHICS_PIPELINE_CREATE_INFO     = 28
	STRUCT_PIPELINE_LAYOUT_CREATE_INFO       = 30
	STRUCT_SAMPLER_CREATE_INFO               = 31
	STRUCT_DESCRIPTOR_SET_LAYOUT_CREATE_INFO = 32
	STRUCT_DESCRIPTOR_POOL_CREATE_INFO       = 33
	STRUCT_DESCRIPTOR_SET_ALLOCATE_INFO      = 34
	STRUCT_WRITE_DESCRIPTOR_SET              = 35
	STRUCT_FRAMEBUFFER_CREATE_INFO           = 37
	STRUCT_RENDER_PASS_CREATE_INFO           = 38
	STRUCT_COMMAND_POOL_CREATE_INFO          = 39
	STRUCT_COMMAND_BUFFER_ALLOCATE_INFO      = 40
	STRUCT_COMMAND_BUFFER_BEGIN_INFO         = 42
	STRUCT_RENDER_PASS_BEGIN_INFO            = 43
	STRUCT_IMAGE_MEMORY_BARRIER              = 45
	STRUCT_MEMORY_BARRIER                    = 46
)

// Flags and formats used by this slice — header values, linux/amd64.
const (
	MEM_DEVICE_LOCAL   = 0x00000001
	MEM_HOST_VISIBLE   = 0x00000002
	MEM_HOST_COHERENT  = 0x00000004
	QUEUE_GRAPHICS_BIT = 0x00000001

	IMG_USAGE_TRANSFER_SRC     = 0x00000001
	IMG_USAGE_TRANSFER_DST     = 0x00000002
	IMG_USAGE_SAMPLED          = 0x00000004
	IMG_USAGE_COLOR_ATTACHMENT = 0x00000010
	BUFFER_USAGE_TRANSFER_SRC  = 0x00000001
	BUFFER_USAGE_TRANSFER_DST  = 0x00000002

	FORMAT_B8G8R8A8_SRGB = 50
	IMAGE_TYPE_2D        = 1
	TILING_OPTIMAL       = 0
	SAMPLE_COUNT_1       = 0x00000001
	SHARING_EXCLUSIVE    = 0
	LAYOUT_UNDEFINED     = 0
	LAYOUT_TRANSFER_SRC  = 6
	LAYOUT_TRANSFER_DST  = 7
	ASPECT_COLOR         = 0x00000001

	STAGE_TOP_OF_PIPE         = 0x00000001
	STAGE_TRANSFER            = 0x00001000
	STAGE_COLOR_ATTACHMENT    = 0x00000400
	ACCESS_TRANSFER_WRITE     = 0x00001000
	ACCESS_TRANSFER_READ      = 0x00000800
	ACCESS_COLOR_ATTACH_WRITE = 0x00000100

	CB_USAGE_ONE_TIME_SUBMIT = 0x00000001
	CB_LEVEL_PRIMARY         = 0
	FENCE_SIGNALED           = 0x00000001
	POOL_RESET_CMD_BUFFER    = 0x00000002
)

// VkApplicationInfo: C size 48, fields 5×u32/uintptr mix.
type VkApplicationInfo struct {
	SType              uint32 // pad for PNext alignment
	PNext              unsafe.Pointer
	PApplicationName   *byte
	ApplicationVersion uint32
	PEngineName        *byte
	EngineVersion      uint32
	APIVersion         uint32
}

// VkInstanceCreateInfo: C size 64.
type VkInstanceCreateInfo struct {
	SType                   uint32
	PNext                   unsafe.Pointer
	Flags                   uint32
	PApplicationInfo        *VkApplicationInfo
	EnabledLayerCount       uint32
	PpEnabledLayerNames     **byte
	EnabledExtensionCount   uint32
	PpEnabledExtensionNames **byte
}

// VkQueueFamilyProperties: C size 24.
type VkQueueFamilyProperties struct {
	QueueFlags                  uint32
	QueueCount                  uint32
	TimestampValidBits          uint32
	MinImageTransferGranularity [3]uint32 // VkExtent3D
}

// VkPhysicalDeviceProperties is 824 bytes in C; we only need deviceName, so
// we reserve the full size and read the name from its known offset (0x8).
// Asserted in tests.
type VkPhysicalDeviceProperties [824]byte

// PhysicalDeviceName returns the spec name. Offset 20: apiVersion, driverVersion,
// vendorID, deviceID, deviceType (5 u32s), then deviceName[256].
func (p *VkPhysicalDeviceProperties) PhysicalDeviceName() string {
	name := (*[256]byte)(unsafe.Pointer(&p[20]))[:]
	for i, c := range name {
		if c == 0 {
			return string(name[:i])
		}
		_ = i
	}
	return string(name[:])
}

// VkDeviceQueueCreateInfo: C size 40.
type VkDeviceQueueCreateInfo struct {
	SType            uint32
	PNext            unsafe.Pointer
	Flags            uint32
	QueueFamilyIndex uint32
	QueueCount       uint32
	PQueuePriorities *float32
}

// VkDeviceCreateInfo: C size 72.
type VkDeviceCreateInfo struct {
	SType                   uint32
	PNext                   unsafe.Pointer
	Flags                   uint32
	QueueCreateInfoCount    uint32
	PQueueCreateInfos       *VkDeviceQueueCreateInfo
	EnabledLayerCount       uint32
	PpEnabledLayerNames     **byte
	EnabledExtensionCount   uint32
	PpEnabledExtensionNames **byte
	PEnabledFeatures        unsafe.Pointer
}

// VkCommandPoolCreateInfo: C size 24.
type VkCommandPoolCreateInfo struct {
	SType            uint32
	PNext            unsafe.Pointer
	Flags            uint32
	QueueFamilyIndex uint32
}

// VkCommandBufferAllocateInfo: C size 40.
type VkCommandBufferAllocateInfo struct {
	SType              uint32
	PNext              unsafe.Pointer
	CommandPool        CommandPool
	Level              uint32
	CommandBufferCount uint32
}

// VkFenceCreateInfo: C size 24.
type VkFenceCreateInfo struct {
	SType uint32
	PNext unsafe.Pointer
	Flags uint32
}

// VkSubmitInfo: C size 72.
type VkSubmitInfo struct {
	SType                uint32
	PNext                unsafe.Pointer
	WaitSemaphoreCount   uint32
	PWaitSemaphores      *Semaphore
	PWaitDstStageMask    *uint32
	CommandBufferCount   uint32
	PCommandBuffers      *CommandBuffer
	SignalSemaphoreCount uint32
	PSignalSemaphores    *Semaphore
}

// VkImageCreateInfo: C size 88.
type VkImageCreateInfo struct {
	SType                 uint32
	PNext                 unsafe.Pointer
	Flags                 uint32
	ImageType             uint32
	Format                int32
	Extent                [3]uint32
	MipLevels             uint32
	ArrayLayers           uint32
	Samples               uint32
	Tiling                uint32
	Usage                 uint32
	SharingMode           uint32
	QueueFamilyIndexCount uint32
	PQueueFamilyIndices   *uint32
	InitialLayout         int32
}

// VkMemoryRequirements: C size 24.
type VkMemoryRequirements struct {
	Size           uint64
	Alignment      uint64
	MemoryTypeBits uint32
}

// VkMemoryAllocateInfo: C size 32.
type VkMemoryAllocateInfo struct {
	SType           uint32
	PNext           unsafe.Pointer
	AllocationSize  uint64
	MemoryTypeIndex uint32
}

// VkImageViewCreateInfo: C size 80.
type VkImageViewCreateInfo struct {
	SType            uint32
	PNext            unsafe.Pointer
	Flags            uint32
	Image            Image
	ViewType         int32
	Format           int32
	Components       VkComponentMapping
	SubresourceRange VkImageSubresourceRange
}

// VkComponentMapping: C size 16.
type VkComponentMapping struct{ R, G, B, A int32 }

// VkImageSubresourceRange: C size 20.
type VkImageSubresourceRange struct {
	AspectMask     uint32
	BaseMipLevel   uint32
	LevelCount     uint32
	BaseArrayLayer uint32
	LayerCount     uint32
}

// VkBufferCreateInfo: C size 56.
type VkBufferCreateInfo struct {
	SType                 uint32
	PNext                 unsafe.Pointer
	Flags                 uint32
	Size                  uint64
	Usage                 uint32
	SharingMode           uint32
	QueueFamilyIndexCount uint32
	PQueueFamilyIndices   *uint32
}

// VkImageMemoryBarrier: C size 72.
type VkImageMemoryBarrier struct {
	SType            uint32
	PNext            unsafe.Pointer
	SrcAccessMask    uint32
	DstAccessMask    uint32
	OldLayout        int32
	NewLayout        int32
	SrcQueueFamily   uint32
	DstQueueFamily   uint32
	Image            Image
	SubresourceRange VkImageSubresourceRange
}

// VkBufferImageCopy: C size 56.
type VkBufferImageCopy struct {
	BufferOffset      uint64
	BufferRowLength   uint32
	BufferImageHeight uint32
	ImageSubresource  VkImageSubresourceLayers
	ImageOffset       [3]int32
	ImageExtent       [3]uint32
}

// VkImageSubresourceLayers: C size 20.
type VkImageSubresourceLayers struct {
	AspectMask     uint32
	MipLevel       uint32
	BaseArrayLayer uint32
	LayerCount     uint32
}

// VkMemoryBarrier: C size 24.
type VkMemoryBarrier struct {
	SType         uint32
	PNext         unsafe.Pointer
	SrcAccessMask uint32
	DstAccessMask uint32
}

// VkRenderPassCreateInfo: C size 64.
type VkRenderPassCreateInfo struct {
	SType           uint32
	PNext           unsafe.Pointer
	Flags           uint32
	AttachmentCount uint32
	PAttachments    *VkAttachmentDescription
	SubpassCount    uint32
	PSubpasses      *VkSubpassDescription
	DependencyCount uint32
	PDependencies   *VkSubpassDependency
}

// VkAttachmentDescription: C size 32.
type VkAttachmentDescription struct {
	Flags          uint32
	Format         int32
	Samples        uint32
	LoadOp         int32
	StoreOp        int32
	StencilLoadOp  int32
	StencilStoreOp int32
	InitialLayout  int32
	FinalLayout    int32
}

// VkSubpassDescription: C size 72.
type VkSubpassDescription struct {
	Flags                   uint32
	PipelineBindPoint       int32
	InputAttachmentCount    uint32
	PInputAttachments       *VkAttachmentReference
	ColorAttachmentCount    uint32
	PColorAttachments       *VkAttachmentReference
	PResolveAttachments     *VkAttachmentReference
	PDepthStencilAttachment *VkAttachmentReference
	PreserveAttachmentCount uint32
	PPreserveAttachments    *uint32
}

// VkAttachmentReference: C size 8.
type VkAttachmentReference struct {
	Attachment uint32
	Layout     int32
}

// VkSubpassDependency: C size 32.
type VkSubpassDependency struct {
	SrcSubpass      uint32
	DstSubpass      uint32
	SrcStageMask    uint32
	DstStageMask    uint32
	SrcAccessMask   uint32
	DstAccessMask   uint32
	DependencyFlags uint32
}

// VkPipelineLayoutCreateInfo: C size 48.
type VkPipelineLayoutCreateInfo struct {
	SType                  uint32
	PNext                  unsafe.Pointer
	Flags                  uint32
	SetLayoutCount         uint32
	PSetLayouts            *DescriptorSetLayout
	PushConstantRangeCount uint32
	PPushConstantRanges    *VkPushConstantRange
}

// VkPushConstantRange: C size 16.
type VkPushConstantRange struct {
	StageFlags uint32
	Offset     uint32
	Size       uint32
}

// VkShaderModuleCreateInfo: C size 40.
type VkShaderModuleCreateInfo struct {
	SType    uint32
	PNext    unsafe.Pointer
	Flags    uint32
	CodeSize uint64
	PCode    *uint32
}

// VkDescriptorSetLayoutCreateInfo: C size 32.
type VkDescriptorSetLayoutCreateInfo struct {
	SType        uint32
	PNext        unsafe.Pointer
	Flags        uint32
	BindingCount uint32
	PBindings    *VkDescriptorSetLayoutBinding
}

// VkDescriptorSetLayoutBinding: C size 32.
type VkDescriptorSetLayoutBinding struct {
	Binding         uint32
	DescriptorType  int32
	DescriptorCount uint32
	StageFlags      uint32
	PSamplers       *Sampler
}

// VkDescriptorPoolCreateInfo: C size 40.
type VkDescriptorPoolCreateInfo struct {
	SType         uint32
	PNext         unsafe.Pointer
	Flags         uint32
	MaxSets       uint32
	PoolSizeCount uint32
	PPoolSizes    *VkDescriptorPoolSize
}

// VkDescriptorPoolSize: C size 8.
type VkDescriptorPoolSize struct {
	Type            int32
	DescriptorCount uint32
}

// VkDescriptorSetAllocateInfo: C size 40.
type VkDescriptorSetAllocateInfo struct {
	SType              uint32
	PNext              unsafe.Pointer
	DescriptorPool     DescriptorPool
	DescriptorSetCount uint32
	PSetLayouts        *DescriptorSetLayout
}

// VkWriteDescriptorSet: C size 64.
type VkWriteDescriptorSet struct {
	SType            uint32
	PNext            unsafe.Pointer
	DstSet           DescriptorSet
	DstBinding       uint32
	DstArrayElement  uint32
	DescriptorCount  uint32
	DescriptorType   int32
	PImageInfo       *VkDescriptorImageInfo
	PBufferInfo      *VkDescriptorBufferInfo
	PTexelBufferView *Buffer
}

// VkDescriptorImageInfo: C size 24.
type VkDescriptorImageInfo struct {
	Sampler     Sampler
	ImageView   ImageView
	ImageLayout int32
}

// VkDescriptorBufferInfo: C size 24.
type VkDescriptorBufferInfo struct {
	Buffer Buffer
	Offset uint64
	Range  uint64
}

// VkGraphicsPipelineCreateInfo: C size 144.
type VkGraphicsPipelineCreateInfo struct {
	SType               uint32
	PNext               unsafe.Pointer
	Flags               uint32
	StageCount          uint32
	PStages             *VkPipelineShaderStageCreateInfo
	PVertexInputState   unsafe.Pointer
	PInputAssemblyState unsafe.Pointer
	PTessellationState  unsafe.Pointer
	PViewportState      unsafe.Pointer
	PRasterizationState unsafe.Pointer
	PMultisampleState   unsafe.Pointer
	PDepthStencilState  unsafe.Pointer
	PColorBlendState    unsafe.Pointer
	PDynamicState       unsafe.Pointer
	Layout              PipelineLayout
	RenderPass          RenderPass
	Subpass             uint32
	BasePipelineHandle  Pipeline
	BasePipelineIndex   int32
}

// VkPipelineShaderStageCreateInfo: C size 48.
type VkPipelineShaderStageCreateInfo struct {
	SType               uint32
	PNext               unsafe.Pointer
	Flags               uint32
	Stage               int32
	Module              ShaderModule
	PName               *byte
	PSpecializationInfo unsafe.Pointer
}

// VkFramebufferCreateInfo: C size 64.
type VkFramebufferCreateInfo struct {
	SType           uint32
	PNext           unsafe.Pointer
	Flags           uint32
	RenderPass      RenderPass
	AttachmentCount uint32
	PAttachments    *ImageView
	Width           uint32
	Height          uint32
	Layers          uint32
}

// VkSamplerCreateInfo: C size 80.
type VkSamplerCreateInfo struct {
	SType                   uint32
	PNext                   unsafe.Pointer
	Flags                   uint32
	MagFilter               int32
	MinFilter               int32
	MipmapMode              int32
	AddressModeU            int32
	AddressModeV            int32
	AddressModeW            int32
	MipLodBias              float32
	AnisotropyEnable        uint32
	MaxAnisotropy           float32
	CompareEnable           uint32
	CompareOp               int32
	MinLod                  float32
	MaxLod                  float32
	BorderColor             int32
	UnnormalizedCoordinates uint32
}

// VkViewport: C size 24.
type VkViewport struct{ X, Y, W, H, MinDepth, MaxDepth float32 }

// VkRect2D: C size 16.
type VkRect2D struct {
	Offset [2]int32
	Extent [2]uint32
}

// VkClearValue: C size 16.
type VkClearValue struct{ Color [4]float32 }

// VkRenderPassBeginInfo: C size 64.
type VkRenderPassBeginInfo struct {
	SType           uint32
	PNext           unsafe.Pointer
	RenderPass      RenderPass
	Framebuffer     Framebuffer
	RenderArea      VkRect2D
	ClearValueCount uint32
	PClearValues    *VkClearValue
}

// VkClearColorValue: C size 16.
type VkClearColorValue struct{ Float32 [4]float32 }

// FnGetInstanceProcAddr resolves an instance-level entry point by name.
// A nil result means the instance (or the loader) does not support it.
type FnGetInstanceProcAddr func(instance Instance, name []byte) unsafe.Pointer

// FnGetDeviceProcAddr resolves device-level entry points; using it avoids
// the loader trampoline for the hottest functions.
type FnGetDeviceProcAddr func(device Device, name *byte) unsafe.Pointer

// FnCreateInstance creates the top-level instance; this is the first call
// through the loader and where "loader present but no ICD" failures surface.
type FnCreateInstance func(pCreateInfo *VkInstanceCreateInfo, pAllocator uintptr, pInstance *Instance) Result

// FnDestroyInstance
type FnDestroyInstance func(instance Instance, pAllocator uintptr)

// FnGetPhysicalDeviceProperties
type FnGetPhysicalDeviceProperties func(physicalDevice PhysicalDevice, pProperties *VkPhysicalDeviceProperties)

// FnGetPhysicalDeviceQueueFamilyProperties
type FnGetPhysicalDeviceQueueFamilyProperties func(
	physicalDevice PhysicalDevice,
	pQueueFamilyPropertyCount *uint32,
	pQueueFamilyProperties *VkQueueFamilyProperties,
)

// FnGetPhysicalDeviceMemoryProperties
type FnGetPhysicalDeviceMemoryProperties func(physicalDevice PhysicalDevice, pProperties *VkPhysicalDeviceMemoryProperties)

// FnCreateDevice
type FnCreateDevice func(physicalDevice PhysicalDevice, pCreateInfo *VkDeviceCreateInfo, pAllocator uintptr, pDevice *Handle) Result

// FnEnumeratePhysicalDevices
type FnEnumeratePhysicalDevices func(instance Instance, pCount *uint32, pPhysicalDevices *PhysicalDevice) Result

// Result is the raw Vulkan VkResult (0 == VK_SUCCESS).
type Result int32

func (r Result) Error() string { return "VkResult(" + itoa(int32(r)) + ")" }

func itoa(v int32) string {
	if v == 0 {
		return "VK_SUCCESS"
	}
	// small helper to avoid importing fmt here
	return strconvItoa(int64(v))
}

func strconvItoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// VkPhysicalDeviceMemoryProperties: C size 520 (32 types + 16 heaps).
type VkPhysicalDeviceMemoryProperties struct {
	MemoryTypeCount uint32
	MemoryTypes     [32]VkMemoryType
	MemoryHeapCount uint32
	MemoryHeaps     [16]VkMemoryHeap
}

// VkMemoryType: C size 8.
type VkMemoryType struct {
	PropertyFlags uint32
	HeapIndex     uint32
}

// VkMemoryHeap: C size 16.
type VkMemoryHeap struct {
	Size  uint64
	Flags uint32
}

// VkCommandBufferBeginInfo: C size 32.
type VkCommandBufferBeginInfo struct {
	SType            uint32
	PNext            unsafe.Pointer
	Flags            uint32
	PInheritanceInfo unsafe.Pointer
}
