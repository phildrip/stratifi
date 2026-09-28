package spikevk

// vk_frame.go — the frame slice in pure Go: command pool/buffer, fence,
// offscreen target image, staging buffer, record (barrier → clear → barrier
// → copy), submit, fence-wait, map, readback. Mirrors the C core's
// begin_frame/end_frame/readback_target, including the fence discipline the
// old code violated (readback leaves its own fence alone; there is only one
// fence here and the frame owns it).
//
// Every driver call in this file goes through a resolved address declared in
// this file's own preamble (cgo resolves C.* per file).

/*
#include <stdint.h>
void free(void*);
void    spike_vkGetDeviceQueue(void* f, uint64_t dev, uint32_t fam, uint32_t idx, void* out);
void    spike_vkGetPhysicalDeviceMemoryProperties(void* f, uint64_t pdev, void* props);
int32_t spike_vkCreateCommandPool(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyCommandPool(void* f, uint64_t dev, uint64_t pool, void* a);
int32_t spike_vkAllocateCommandBuffers(void* f, uint64_t dev, const void* info, void* out);
int32_t spike_vkCreateFence(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyFence(void* f, uint64_t dev, uint64_t fence, void* a);
int32_t spike_vkWaitForFences(void* f, uint64_t dev, uint32_t n, const void* fences, uint32_t waitAll, uint64_t timeout);
int32_t spike_vkResetFences(void* f, uint64_t dev, uint32_t n, const void* fences);
int32_t spike_vkBeginCommandBuffer(void* f, uint64_t cb, const void* info);
int32_t spike_vkEndCommandBuffer(void* f, uint64_t cb);
int32_t spike_vkResetCommandBuffer(void* f, uint64_t cb, uint32_t flags);
int32_t spike_vkQueueSubmit(void* f, uint64_t q, uint32_t n, const void* submits, uint64_t fence);
int32_t spike_vkCreateImage(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyImage(void* f, uint64_t dev, uint64_t img, void* a);
void    spike_vkGetImageMemoryRequirements(void* f, uint64_t dev, uint64_t img, void* out);
int32_t spike_vkAllocateMemory(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkFreeMemory(void* f, uint64_t dev, uint64_t mem, void* a);
int32_t spike_vkMapMemory(void* f, uint64_t dev, uint64_t mem, uint64_t off, uint64_t size, uint32_t flags, void* pp);
void    spike_vkUnmapMemory(void* f, uint64_t dev, uint64_t mem);
int32_t spike_vkBindImageMemory(void* f, uint64_t dev, uint64_t img, uint64_t mem, uint64_t off);
int32_t spike_vkCreateBuffer(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyBuffer(void* f, uint64_t dev, uint64_t buf, void* a);
void    spike_vkGetBufferMemoryRequirements(void* f, uint64_t dev, uint64_t buf, void* out);
int32_t spike_vkBindBufferMemory(void* f, uint64_t dev, uint64_t buf, uint64_t mem, uint64_t off);
void    spike_vkCmdPipelineBarrier(void* f, uint64_t cb, uint32_t src, uint32_t dst, uint32_t dep, uint32_t mc, const void* pm, uint32_t bc, const void* pb, uint32_t ic, const void* pi);
void    spike_vkCmdClearColorImage(void* f, uint64_t cb, uint64_t img, int32_t layout, const void* clear, uint32_t n, const void* ranges);
void    spike_vkCmdCopyImageToBuffer(void* f, uint64_t cb, uint64_t img, int32_t layout, uint64_t buf, uint32_t n, const void* regions);
*/
import "C"

import (
	"time"
	"unsafe"
)

// GpuFrame owns one reusable offscreen target and its command resources.
// Pixels are returned in a caller-visible slice the frame object owns and
// reuses across Render calls (zero steady-state allocation).
type GpuFrame struct {
	ctx    *GraphicsContext
	width  uint32
	height uint32

	pool  CommandPool
	cmd   CommandBuffer
	fence Fence

	img    Image
	imgMem DeviceMemory
	stg    Buffer
	stgMem DeviceMemory
	stgLen uint64
	// cmdSlot holds the command-buffer handle in C memory, written once at
	// creation. VkSubmitInfo points at it instead of at a Go field, which
	// cgo would reject (Go pointer inside a struct passed to C).
	cmdSlot unsafe.Pointer

	out []byte // reused readback copy, w*h*4
}

// memType finds a memory type with the requested property bits, mirroring
// the C core's first-match policy.
func (ctx *GraphicsContext) memType(bits uint32, props uint32) (uint32, error) {
	var mp VkPhysicalDeviceMemoryProperties
	C.spike_vkGetPhysicalDeviceMemoryProperties(ctx.vMemoryProps, C.uint64_t(uint64(ctx.Physical)), unsafe.Pointer(&mp))
	for i := uint32(0); i < mp.MemoryTypeCount; i++ {
		if bits&(1<<i) != 0 && mp.MemoryTypes[i].PropertyFlags&props == props {
			return i, nil
		}
	}
	return 0, ResultError(-1, "no suitable memory type")
}

// NewGpuFrame builds the command resources, the DEVICE_LOCAL target image
// and the HOST staging buffer.
func NewGpuFrame(ctx *GraphicsContext, width, height uint32) (*GpuFrame, error) {
	f := &GpuFrame{ctx: ctx, width: width, height: height}
	dev := C.uint64_t(uint64(ctx.Logical))

	poolInfo := VkCommandPoolCreateInfo{SType: STRUCT_COMMAND_POOL_CREATE_INFO, Flags: POOL_RESET_CMD_BUFFER, QueueFamilyIndex: ctx.QueueFamily}
	if res := Result(C.spike_vkCreateCommandPool(ctx.vCreateCommandPool, dev, unsafe.Pointer(&poolInfo), nil, unsafe.Pointer(&f.pool))); res != 0 {
		return nil, ResultError(res, "vkCreateCommandPool")
	}
	allocInfo := VkCommandBufferAllocateInfo{SType: STRUCT_COMMAND_BUFFER_ALLOCATE_INFO, CommandPool: f.pool, Level: CB_LEVEL_PRIMARY, CommandBufferCount: 1}
	if res := Result(C.spike_vkAllocateCommandBuffers(ctx.vAllocateCmdBuffers, dev, unsafe.Pointer(&allocInfo), unsafe.Pointer(&f.cmd))); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkAllocateCommandBuffers")
	}
	fenceInfo := VkFenceCreateInfo{SType: STRUCT_FENCE_CREATE_INFO, Flags: FENCE_SIGNALED}
	if res := Result(C.spike_vkCreateFence(ctx.vCreateFence, dev, unsafe.Pointer(&fenceInfo), nil, unsafe.Pointer(&f.fence))); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkCreateFence")
	}

	imgInfo := VkImageCreateInfo{
		SType: STRUCT_IMAGE_CREATE_INFO, ImageType: IMAGE_TYPE_2D, Format: FORMAT_B8G8R8A8_SRGB,
		Extent: [3]uint32{width, height, 1}, MipLevels: 1, ArrayLayers: 1,
		Samples: SAMPLE_COUNT_1, Tiling: TILING_OPTIMAL,
		Usage:       IMG_USAGE_COLOR_ATTACHMENT | IMG_USAGE_TRANSFER_SRC | IMG_USAGE_TRANSFER_DST,
		SharingMode: SHARING_EXCLUSIVE, InitialLayout: LAYOUT_UNDEFINED,
	}
	if res := Result(C.spike_vkCreateImage(ctx.vCreateImage, dev, unsafe.Pointer(&imgInfo), nil, unsafe.Pointer(&f.img))); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkCreateImage")
	}
	var imgReqs VkMemoryRequirements
	C.spike_vkGetImageMemoryRequirements(ctx.vGetImageMemReqs, dev, C.uint64_t(uint64(f.img)), unsafe.Pointer(&imgReqs))
	imgType, err := ctx.memType(imgReqs.MemoryTypeBits, MEM_DEVICE_LOCAL)
	if err != nil {
		f.Close()
		return nil, err
	}
	imgAlloc := VkMemoryAllocateInfo{SType: STRUCT_MEMORY_ALLOCATE_INFO, AllocationSize: imgReqs.Size, MemoryTypeIndex: imgType}
	if res := Result(C.spike_vkAllocateMemory(ctx.vAllocateMemory, dev, unsafe.Pointer(&imgAlloc), nil, unsafe.Pointer(&f.imgMem))); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkAllocateMemory (image)")
	}
	if res := Result(C.spike_vkBindImageMemory(ctx.vBindImageMemory, dev, C.uint64_t(uint64(f.img)), C.uint64_t(uint64(f.imgMem)), 0)); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkBindImageMemory")
	}

	f.stgLen = uint64(width) * uint64(height) * 4
	bufInfo := VkBufferCreateInfo{
		SType: STRUCT_BUFFER_CREATE_INFO, Size: f.stgLen,
		Usage: BUFFER_USAGE_TRANSFER_DST, SharingMode: SHARING_EXCLUSIVE,
	}
	if res := Result(C.spike_vkCreateBuffer(ctx.vCreateBuffer, dev, unsafe.Pointer(&bufInfo), nil, unsafe.Pointer(&f.stg))); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkCreateBuffer")
	}
	var bufReqs VkMemoryRequirements
	C.spike_vkGetBufferMemoryRequirements(ctx.vGetBufferMemReqs, dev, C.uint64_t(uint64(f.stg)), unsafe.Pointer(&bufReqs))
	bufType, err := ctx.memType(bufReqs.MemoryTypeBits, MEM_HOST_VISIBLE|MEM_HOST_COHERENT)
	if err != nil {
		f.Close()
		return nil, err
	}
	bufAlloc := VkMemoryAllocateInfo{SType: STRUCT_MEMORY_ALLOCATE_INFO, AllocationSize: bufReqs.Size, MemoryTypeIndex: bufType}
	if res := Result(C.spike_vkAllocateMemory(ctx.vAllocateMemory, dev, unsafe.Pointer(&bufAlloc), nil, unsafe.Pointer(&f.stgMem))); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkAllocateMemory (staging)")
	}
	if res := Result(C.spike_vkBindBufferMemory(ctx.vBindBufferMemory, dev, C.uint64_t(uint64(f.stg)), C.uint64_t(uint64(f.stgMem)), 0)); res != 0 {
		f.Close()
		return nil, ResultError(res, "vkBindBufferMemory")
	}

	f.cmdSlot = C.malloc(8)
	*(*CommandBuffer)(f.cmdSlot) = f.cmd

	f.out = make([]byte, f.stgLen)
	return f, nil
}

// Render records barrier → clear → barrier → copy, submits on the frame
// fence (wait then reset, the C core's exact discipline), waits, maps the
// staging buffer and returns the pixels. The returned slice aliases the
// frame's own buffer and is reused by the next call.
func (f *GpuFrame) Render(r, g, b, a float32) ([]byte, time.Duration, error) {
	ctx := f.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	start := time.Now()

	if res := Result(C.spike_vkWaitForFences(ctx.vWaitForFences, dev, 1, unsafe.Pointer(&f.fence), 1, 1_000_000_000)); res != 0 {
		return nil, 0, ResultError(res, "vkWaitForFences")
	}
	if res := Result(C.spike_vkResetFences(ctx.vResetFences, dev, 1, unsafe.Pointer(&f.fence))); res != 0 {
		return nil, 0, ResultError(res, "vkResetFences")
	}
	if res := Result(C.spike_vkResetCommandBuffer(ctx.vResetCmdBuffer, C.uint64_t(uint64(f.cmd)), 0)); res != 0 {
		return nil, 0, ResultError(res, "vkResetCommandBuffer")
	}
	begin := VkCommandBufferBeginInfo{SType: STRUCT_COMMAND_BUFFER_BEGIN_INFO, Flags: CB_USAGE_ONE_TIME_SUBMIT}
	if res := Result(C.spike_vkBeginCommandBuffer(ctx.vBeginCmdBuffer, C.uint64_t(uint64(f.cmd)), unsafe.Pointer(&begin))); res != 0 {
		return nil, 0, ResultError(res, "vkBeginCommandBuffer")
	}

	sub := VkImageSubresourceRange{AspectMask: ASPECT_COLOR, BaseMipLevel: 0, LevelCount: 1, BaseArrayLayer: 0, LayerCount: 1}
	pre := VkImageMemoryBarrier{
		SType: STRUCT_IMAGE_MEMORY_BARRIER, SrcAccessMask: 0, DstAccessMask: ACCESS_TRANSFER_WRITE,
		OldLayout: LAYOUT_UNDEFINED, NewLayout: LAYOUT_TRANSFER_DST,
		SrcQueueFamily: 0xFFFFFFFF, DstQueueFamily: 0xFFFFFFFF,
		Image: f.img, SubresourceRange: sub,
	}
	C.spike_vkCmdPipelineBarrier(ctx.vCmdPipelineBarrier, C.uint64_t(uint64(f.cmd)),
		STAGE_TOP_OF_PIPE, STAGE_TRANSFER, 0, 0, nil, 0, nil, 1, unsafe.Pointer(&pre))

	clear := VkClearColorValue{Float32: [4]float32{r, g, b, a}}
	C.spike_vkCmdClearColorImage(ctx.vCmdClearColorImage, C.uint64_t(uint64(f.cmd)),
		C.uint64_t(uint64(f.img)), LAYOUT_TRANSFER_DST, unsafe.Pointer(&clear), 1, unsafe.Pointer(&sub))

	post := pre
	post.OldLayout, post.NewLayout = LAYOUT_TRANSFER_DST, LAYOUT_TRANSFER_SRC
	post.SrcAccessMask, post.DstAccessMask = ACCESS_TRANSFER_WRITE, ACCESS_TRANSFER_READ
	C.spike_vkCmdPipelineBarrier(ctx.vCmdPipelineBarrier, C.uint64_t(uint64(f.cmd)),
		STAGE_TRANSFER, STAGE_TRANSFER, 0, 0, nil, 0, nil, 1, unsafe.Pointer(&post))

	region := VkBufferImageCopy{
		BufferOffset: 0, BufferRowLength: 0, BufferImageHeight: 0,
		ImageSubresource: VkImageSubresourceLayers{AspectMask: ASPECT_COLOR, MipLevel: 0, BaseArrayLayer: 0, LayerCount: 1},
		ImageOffset:      [3]int32{0, 0, 0},
		ImageExtent:      [3]uint32{f.width, f.height, 1},
	}
	C.spike_vkCmdCopyImageToBuffer(ctx.vCmdCopyImageToBuf, C.uint64_t(uint64(f.cmd)),
		C.uint64_t(uint64(f.img)), LAYOUT_TRANSFER_SRC, C.uint64_t(uint64(f.stg)), 1, unsafe.Pointer(&region))

	if res := Result(C.spike_vkEndCommandBuffer(ctx.vEndCmdBuffer, C.uint64_t(uint64(f.cmd)))); res != 0 {
		return nil, 0, ResultError(res, "vkEndCommandBuffer")
	}
	submit := VkSubmitInfo{SType: STRUCT_SUBMIT_INFO, CommandBufferCount: 1, PCommandBuffers: (*CommandBuffer)(f.cmdSlot)}
	if res := Result(C.spike_vkQueueSubmit(ctx.vQueueSubmit, C.uint64_t(uint64(ctx.Queue)), 1, unsafe.Pointer(&submit), C.uint64_t(uint64(f.fence)))); res != 0 {
		return nil, 0, ResultError(res, "vkQueueSubmit")
	}
	if res := Result(C.spike_vkWaitForFences(ctx.vWaitForFences, dev, 1, unsafe.Pointer(&f.fence), 1, 1_000_000_000)); res != 0 {
		return nil, 0, ResultError(res, "vkWaitForFences (frame)")
	}

	var mapped *byte
	if res := Result(C.spike_vkMapMemory(ctx.vMapMemory, dev, C.uint64_t(uint64(f.stgMem)), 0, C.uint64_t(f.stgLen), 0, unsafe.Pointer(&mapped))); res != 0 {
		return nil, 0, ResultError(res, "vkMapMemory")
	}
	copy(f.out, unsafe.Slice(mapped, f.stgLen))
	C.spike_vkUnmapMemory(ctx.vUnmapMemory, dev, C.uint64_t(uint64(f.stgMem)))

	return f.out, time.Since(start), nil
}

// Close releases buffer, image, fence, command pool (reverse-ish order like
// the C core; the logical device dies with the GraphicsContext).
func (f *GpuFrame) Close() {
	if f == nil || f.ctx == nil {
		return
	}
	ctx := f.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	if f.stg != 0 {
		C.spike_vkDestroyBuffer(ctx.vDestroyBuffer, dev, C.uint64_t(uint64(f.stg)), nil)
		f.stg = 0
	}
	if f.stgMem != 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(f.stgMem)), nil)
		f.stgMem = 0
	}
	if f.img != 0 {
		C.spike_vkDestroyImage(ctx.vDestroyImage, dev, C.uint64_t(uint64(f.img)), nil)
		f.img = 0
	}
	if f.imgMem != 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(f.imgMem)), nil)
		f.imgMem = 0
	}
	if f.fence != 0 {
		C.spike_vkDestroyFence(ctx.vDestroyFence, dev, C.uint64_t(uint64(f.fence)), nil)
		f.fence = 0
	}
	if f.cmdSlot != nil {
		C.free(f.cmdSlot)
		f.cmdSlot = nil
	}
	if ctx.vDestroyCommandPool != nil && f.pool != 0 {
		C.spike_vkDestroyCommandPool(ctx.vDestroyCommandPool, dev, C.uint64_t(uint64(f.pool)), nil)
		f.pool = 0
	}
}
