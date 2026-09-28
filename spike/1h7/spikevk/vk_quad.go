package spikevk

// vk_quad.go — the textured-quad crate: render pass, framebuffer, sampler,
// descriptor ring, pipeline layout, graphics pipeline, texture upload, and
// textured draws, all in Go. The descriptor ring lives here as ordinary Go
// state (a slice + cursor), which is the point: the aliasing rule is now
// testable at the language level instead of hiding in C.
//
// Every C call goes through a resolved address declared in this file's own
// preamble. Textures upload through a staging buffer like the C core.

/*
#include <stdint.h>
#include <stdint.h>
int32_t spike_vkCreateBuffer(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyBuffer(void* f, uint64_t dev, uint64_t buf, void* a);
void    spike_vkGetBufferMemoryRequirements(void* f, uint64_t dev, uint64_t buf, void* out);
int32_t spike_vkAllocateMemory(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkFreeMemory(void* f, uint64_t dev, uint64_t mem, void* a);
int32_t spike_vkMapMemory(void* f, uint64_t dev, uint64_t mem, uint64_t off, uint64_t size, uint32_t flags, void* pp);
void    spike_vkUnmapMemory(void* f, uint64_t dev, uint64_t mem);
int32_t spike_vkBindBufferMemory(void* f, uint64_t dev, uint64_t buf, uint64_t mem, uint64_t off);
void    spike_vkDestroyImageView(void* f, uint64_t dev, uint64_t view, void* a);
int32_t spike_vkAllocateMemory(void* f, uint64_t dev, const void* info, void* a, void* out);
int32_t spike_vkBeginCommandBuffer(void* f, uint64_t cb, const void* info);
int32_t spike_vkBindImageMemory(void* f, uint64_t dev, uint64_t img, uint64_t mem, uint64_t off);
void    spike_vkCmdCopyBufferToImage(void* f, uint64_t cb, uint64_t buf, uint64_t img, int32_t layout, uint32_t n, const void* regions);
void    spike_vkCmdCopyImageToBuffer(void* f, uint64_t cb, uint64_t img, int32_t layout, uint64_t buf, uint32_t n, const void* regions);
void    spike_vkCmdPipelineBarrier(void* f, uint64_t cb, uint32_t src, uint32_t dst, uint32_t dep, uint32_t mc, const void* pm, uint32_t bc, const void* pb, uint32_t ic, const void* pi);
int32_t spike_vkCreateImage(void* f, uint64_t dev, const void* info, void* a, void* out);
int32_t spike_vkCreateImageView(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyImage(void* f, uint64_t dev, uint64_t img, void* a);
int32_t spike_vkEndCommandBuffer(void* f, uint64_t cb);
void    spike_vkFreeMemory(void* f, uint64_t dev, uint64_t mem, void* a);
void    spike_vkGetImageMemoryRequirements(void* f, uint64_t dev, uint64_t img, void* out);
int32_t spike_vkMapMemory(void* f, uint64_t dev, uint64_t mem, uint64_t off, uint64_t size, uint32_t flags, void* pp);
int32_t spike_vkQueueSubmit(void* f, uint64_t q, uint32_t n, const void* submits, uint64_t fence);
int32_t spike_vkResetCommandBuffer(void* f, uint64_t cb, uint32_t flags);
int32_t spike_vkResetFences(void* f, uint64_t dev, uint32_t n, const void* fences);
void    spike_vkUnmapMemory(void* f, uint64_t dev, uint64_t mem);
int32_t spike_vkWaitForFences(void* f, uint64_t dev, uint32_t n, const void* fences, uint32_t waitAll, uint64_t timeout);
int32_t spike_vkCreateShaderModule(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyShaderModule(void* f, uint64_t dev, uint64_t mod, void* a);
int32_t spike_vkCreateDescriptorSetLayout(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyDescriptorSetLayout(void* f, uint64_t dev, uint64_t layout, void* a);
int32_t spike_vkCreateDescriptorPool(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyDescriptorPool(void* f, uint64_t dev, uint64_t pool, void* a);
int32_t spike_vkAllocateDescriptorSets(void* f, uint64_t dev, const void* info, void* out);
void    spike_vkUpdateDescriptorSets(void* f, uint64_t dev, uint32_t nw, const void* writes, uint32_t nc, const void* copies);
int32_t spike_vkCreatePipelineLayout(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyPipelineLayout(void* f, uint64_t dev, uint64_t layout, void* a);
int32_t spike_vkCreateGraphicsPipelines(void* f, uint64_t dev, uint64_t cache, uint32_t n, const void* infos, void* a, void* out);
void    spike_vkDestroyPipeline(void* f, uint64_t dev, uint64_t pipe, void* a);
int32_t spike_vkCreateRenderPass(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyRenderPass(void* f, uint64_t dev, uint64_t rp, void* a);
int32_t spike_vkCreateFramebuffer(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroyFramebuffer(void* f, uint64_t dev, uint64_t fb, void* a);
int32_t spike_vkCreateSampler(void* f, uint64_t dev, const void* info, void* a, void* out);
void    spike_vkDestroySampler(void* f, uint64_t dev, uint64_t s, void* a);
void    spike_vkCmdBeginRenderPass(void* f, uint64_t cb, const void* info, int32_t contents);
void    spike_vkCmdEndRenderPass(void* f, uint64_t cb);
void    spike_vkCmdBindPipeline(void* f, uint64_t cb, int32_t bind, uint64_t pipe);
void    spike_vkCmdBindDescriptorSets(void* f, uint64_t cb, int32_t bind, uint64_t layout, uint32_t first, uint32_t n, const void* sets, uint32_t ndyn, const void* dyn);
void    spike_vkCmdPushConstants(void* f, uint64_t cb, uint64_t layout, uint32_t stages, uint32_t off, uint32_t size, const void* vals);
void    spike_vkCmdDraw(void* f, uint64_t cb, uint32_t vcount, uint32_t icount, uint32_t firstv, uint32_t firsti);
*/
import "C"

import (
	_ "embed"
	"time"
	"unsafe"
)

//go:embed testdata/texture.vert.spv
var textureVertSpv []byte

//go:embed testdata/texture.frag.spv
var textureFragSpv []byte

// QuadTexture is one uploaded client surface: device-local image + view.
type QuadTexture struct {
	img  Image
	mem  DeviceMemory
	view ImageView
	w, h uint32
}

// QuadDraw is one textured quad: which texture, where, and how.
type QuadDraw struct {
	Tex   *QuadTexture
	Dst   [4]float32 // x, y, w, h in pixels
	Src   [4]float32 // u, v, w, h (0..1)
	Alpha float32
}

// QuadRenderer owns the textured-quad pipeline state and records into a
// GpuFrame's command buffer, target, fence and queue. The descriptor ring
// (descSets + descNext) is plain Go state reset per frame — the aliasing
// invariant from the C bug is enforced here by construction and asserted by
// TestQuadTwoTexturesVisible.
type QuadRenderer struct {
	ctx   *GraphicsContext
	frame *GpuFrame

	renderPass RenderPass
	framebuf   Framebuffer
	targetView ImageView
	layout     PipelineLayout
	pipeline   Pipeline
	sampler    Sampler

	descLayout DescriptorSetLayout
	descPool   DescriptorPool
	descSets   []DescriptorSet
	descNext   uint32
}

// NewQuadRenderer builds the render pass, framebuffer, sampler, descriptor
// ring, layout and graphics pipeline for the frame's target.
func NewQuadRenderer(frame *GpuFrame) (*QuadRenderer, error) {
	ctx := frame.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	q := &QuadRenderer{ctx: ctx, frame: frame}

	// Render pass: one SRGB attachment, clear on load, store, UNDEFINED in.
	attachment := VkAttachmentDescription{
		Format: 50, Samples: SAMPLE_COUNT_1,
		LoadOp: 1, StoreOp: 0, StencilLoadOp: 2, StencilStoreOp: 1,
		InitialLayout: LAYOUT_UNDEFINED, FinalLayout: 2, // COLOR_ATTACHMENT_OPTIMAL
	}
	attachRef := VkAttachmentReference{Attachment: 0, Layout: 2}
	subpass := VkSubpassDescription{
		PipelineBindPoint:    BIND_POINT_GRAPHICS,
		ColorAttachmentCount: 1, PColorAttachments: &attachRef,
	}
	dep := VkSubpassDependency{
		SrcSubpass: 0xFFFFFFFF, DstSubpass: 0,
		SrcStageMask: STAGE_COLOR_ATTACHMENT, DstStageMask: STAGE_COLOR_ATTACHMENT,
		SrcAccessMask: 0, DstAccessMask: ACCESS_COLOR_ATTACH_WRITE,
	}
	rpInfo := VkRenderPassCreateInfo{
		SType:           STRUCT_RENDER_PASS_CREATE_INFO,
		AttachmentCount: 1, PAttachments: &attachment,
		SubpassCount: 1, PSubpasses: &subpass,
		DependencyCount: 1, PDependencies: &dep,
	}
	rpPtr, freeRp := envelope(unsafe.Pointer(&rpInfo), unsafe.Sizeof(rpInfo))
	defer freeRp()
	if res := Result(C.spike_vkCreateRenderPass(ctx.vCreateRenderPass, dev, rpPtr, nil, unsafe.Pointer(&q.renderPass))); res != 0 {
		return nil, ResultError(res, "vkCreateRenderPass")
	}

	// Target view + framebuffer over the frame's image.
	viewInfo := VkImageViewCreateInfo{
		SType: STRUCT_IMAGE_VIEW_CREATE_INFO, Image: frame.img,
		ViewType: 1, Format: 50, // 2D, SRGB
		Components:       VkComponentMapping{R: 0, G: 0, B: 0, A: 0}, // IDENTITY x4
		SubresourceRange: VkImageSubresourceRange{AspectMask: ASPECT_COLOR, BaseMipLevel: 0, LevelCount: 1, BaseArrayLayer: 0, LayerCount: 1},
	}
	if res := Result(C.spike_vkCreateImageView(ctx.vCreateImageView, dev, unsafe.Pointer(&viewInfo), nil, unsafe.Pointer(&q.targetView))); res != 0 {
		q.Close()
		return nil, ResultError(res, "vkCreateImageView (target)")
	}
	fbInfo := VkFramebufferCreateInfo{
		SType: STRUCT_FRAMEBUFFER_CREATE_INFO, RenderPass: q.renderPass,
		AttachmentCount: 1, PAttachments: &q.targetView,
		Width: frame.width, Height: frame.height, Layers: 1,
	}
	fbPtr, freeFb := envelope(unsafe.Pointer(&fbInfo), unsafe.Sizeof(fbInfo))
	defer freeFb()
	if res := Result(C.spike_vkCreateFramebuffer(ctx.vCreateFramebuffer, dev, fbPtr, nil, unsafe.Pointer(&q.framebuf))); res != 0 {
		q.Close()
		return nil, ResultError(res, "vkCreateFramebuffer")
	}

	if err := q.createSampler(); err != nil {
		q.Close()
		return nil, err
	}
	if err := q.createDescriptors(); err != nil {
		q.Close()
		return nil, err
	}
	if err := q.createPipeline(); err != nil {
		q.Close()
		return nil, err
	}
	return q, nil
}

func (q *QuadRenderer) createSampler() error {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	info := VkSamplerCreateInfo{
		SType:     STRUCT_SAMPLER_CREATE_INFO,
		MagFilter: 0, MinFilter: 0, MipmapMode: 0, // NEAREST
		AddressModeU: 2, AddressModeV: 2, AddressModeW: 2, // CLAMP
		MipLodBias: 0, AnisotropyEnable: 0, MaxAnisotropy: 1,
		CompareEnable: 0, CompareOp: 7, MinLod: 0, MaxLod: 0,
		BorderColor: 3, UnnormalizedCoordinates: 0,
	}
	if res := Result(C.spike_vkCreateSampler(ctx.vCreateSampler, dev, unsafe.Pointer(&info), nil, unsafe.Pointer(&q.sampler))); res != 0 {
		return ResultError(res, "vkCreateSampler")
	}
	return nil
}

func (q *QuadRenderer) createDescriptors() error {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))

	binding := VkDescriptorSetLayoutBinding{
		Binding: 0, DescriptorType: DESC_COMBINED_SAMPLER, DescriptorCount: 1,
		StageFlags: SHADER_STAGE_FRAG,
	}
	layoutInfo := VkDescriptorSetLayoutCreateInfo{
		SType: STRUCT_DESCRIPTOR_SET_LAYOUT_CREATE_INFO, BindingCount: 1, PBindings: &binding,
	}
	layoutPtr, freeLayout := envelope(unsafe.Pointer(&layoutInfo), unsafe.Sizeof(layoutInfo))
	defer freeLayout()
	if res := Result(C.spike_vkCreateDescriptorSetLayout(ctx.vCreateDescSetLayout, dev, layoutPtr, nil, unsafe.Pointer(&q.descLayout))); res != 0 {
		return ResultError(res, "vkCreateDescriptorSetLayout")
	}

	const ringSize = 64
	poolSize := VkDescriptorPoolSize{Type: DESC_COMBINED_SAMPLER, DescriptorCount: ringSize}
	poolInfo := VkDescriptorPoolCreateInfo{
		SType:   STRUCT_DESCRIPTOR_POOL_CREATE_INFO,
		MaxSets: ringSize, PoolSizeCount: 1, PPoolSizes: &poolSize,
	}
	poolPtr, freePool := envelope(unsafe.Pointer(&poolInfo), unsafe.Sizeof(poolInfo))
	defer freePool()
	if res := Result(C.spike_vkCreateDescriptorPool(ctx.vCreateDescPool, dev, poolPtr, nil, unsafe.Pointer(&q.descPool))); res != 0 {
		return ResultError(res, "vkCreateDescriptorPool")
	}

	q.descSets = make([]DescriptorSet, ringSize)
	for i := range q.descSets {
		allocInfo := VkDescriptorSetAllocateInfo{
			SType:          STRUCT_DESCRIPTOR_SET_ALLOCATE_INFO,
			DescriptorPool: q.descPool, DescriptorSetCount: 1, PSetLayouts: &q.descLayout,
		}
		allocPtr, freeAlloc := envelope(unsafe.Pointer(&allocInfo), unsafe.Sizeof(allocInfo))
		res := Result(C.spike_vkAllocateDescriptorSets(ctx.vAllocateDescSets, dev, allocPtr, unsafe.Pointer(&q.descSets[i])))
		freeAlloc()
		if res != 0 {
			return ResultError(res, "vkAllocateDescriptorSets")
		}
	}
	return nil
}

func (q *QuadRenderer) createPipeline() error {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))

	vertMod, fragMod, err := q.createShaderModules()
	if err != nil {
		return err
	}
	defer func() {
		C.spike_vkDestroyShaderModule(ctx.vDestroyShaderModule, dev, C.uint64_t(uint64(vertMod)), nil)
		C.spike_vkDestroyShaderModule(ctx.vDestroyShaderModule, dev, C.uint64_t(uint64(fragMod)), nil)
	}()

	mainName := acByte("main")
	stages := [2]VkPipelineShaderStageCreateInfo{
		{SType: STRUCT_PIPELINE_SHADER_STAGE_CREATE_INFO, Stage: SHADER_STAGE_VERT, Module: vertMod, PName: &mainName[0]},
		{SType: STRUCT_PIPELINE_SHADER_STAGE_CREATE_INFO, Stage: SHADER_STAGE_FRAG, Module: fragMod, PName: &mainName[0]},
	}
	vertexInput := VkPipelineVertexInputStateCreateInfo{SType: 19}
	inputAsm := VkPipelineInputAssemblyStateCreateInfo{SType: 20, Topology: TOPOLOGY_TRIANGLE_STRIP}
	viewport := VkViewport{X: 0, Y: 0, W: float32(q.frame.width), H: float32(q.frame.height), MinDepth: 0, MaxDepth: 1}
	scissor := VkRect2D{Extent: [2]uint32{q.frame.width, q.frame.height}}
	viewportState := VkPipelineViewportStateCreateInfo{
		SType: 22, ViewportCount: 1, PViewports: &viewport, ScissorCount: 1, PScissors: &scissor,
	}
	raster := VkPipelineRasterizationStateCreateInfo{
		SType: 23, PolygonMode: POLYGON_FILL, CullMode: CULL_NONE, FrontFace: FRONT_CCW, LineWidth: 1.0,
	}
	multisample := VkPipelineMultisampleStateCreateInfo{SType: 24, RasterizationSamples: SAMPLE_COUNT_1}
	blendAtt := VkPipelineColorBlendAttachmentState{
		BlendEnable:         1,
		SrcColorBlendFactor: 1, DstColorBlendFactor: 0, ColorBlendOp: 0, // ONE, ZERO, ADD
		SrcAlphaBlendFactor: 1, DstAlphaBlendFactor: 0, AlphaBlendOp: 0,
		ColorWriteMask: COLOR_WRITE_ALL,
	}
	blend := VkPipelineColorBlendStateCreateInfo{
		SType: 26, AttachmentCount: 1, PAttachments: &blendAtt,
	}
	// No dynamic state: viewport and scissor are baked for the target size.
	// (Production uses dynamic viewport/scissor; the static choice keeps
	// this slice to one fewer moving part and is valid per-target.)

	pushRange := VkPushConstantRange{StageFlags: SHADER_STAGE_VERT, Offset: 0, Size: 44}
	layoutInfo := VkPipelineLayoutCreateInfo{
		SType: 30, SetLayoutCount: 1, PSetLayouts: &q.descLayout,
		PushConstantRangeCount: 1, PPushConstantRanges: &pushRange,
	}
	layoutPtr, freeLayout := envelope(unsafe.Pointer(&layoutInfo), unsafe.Sizeof(layoutInfo))
	defer freeLayout()
	if res := Result(C.spike_vkCreatePipelineLayout(ctx.vCreatePipelineLayout, dev, layoutPtr, nil, unsafe.Pointer(&q.layout))); res != 0 {
		return ResultError(res, "vkCreatePipelineLayout")
	}

	pipeInfo := VkGraphicsPipelineCreateInfo{
		SType: 28, StageCount: 2, PStages: &stages[0],
		PVertexInputState:   unsafe.Pointer(&vertexInput),
		PInputAssemblyState: unsafe.Pointer(&inputAsm),
		PViewportState:      unsafe.Pointer(&viewportState),
		PRasterizationState: unsafe.Pointer(&raster),
		PMultisampleState:   unsafe.Pointer(&multisample),
		PColorBlendState:    unsafe.Pointer(&blend),

		Layout: q.layout, RenderPass: q.renderPass, Subpass: 0,
	}
	pipePtr, freePipe := envelope(unsafe.Pointer(&pipeInfo), unsafe.Sizeof(pipeInfo))
	defer freePipe()
	if res := Result(C.spike_vkCreateGraphicsPipelines(ctx.vCreateGraphicsPipelines, dev, 0, 1, pipePtr, nil, unsafe.Pointer(&q.pipeline))); res != 0 {
		return ResultError(res, "vkCreateGraphicsPipelines")
	}
	return nil
}

func (q *QuadRenderer) createShaderModules() (ShaderModule, ShaderModule, error) {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	mk := func(spv []byte) (ShaderModule, error) {
		if len(spv)%4 != 0 || len(spv) == 0 {
			return 0, ResultError(-1, "SPIR-V size not a multiple of 4")
		}
		info := VkShaderModuleCreateInfo{
			SType: 16, CodeSize: uint64(len(spv)), PCode: (*uint32)(unsafe.Pointer(&spv[0])),
		}
		infoPtr, freeInfo := envelope(unsafe.Pointer(&info), unsafe.Sizeof(info))
		defer freeInfo()
		var mod ShaderModule
		if res := Result(C.spike_vkCreateShaderModule(ctx.vCreateShaderModule, dev, infoPtr, nil, unsafe.Pointer(&mod))); res != 0 {
			return 0, ResultError(res, "vkCreateShaderModule")
		}
		return mod, nil
	}
	vert, err := mk(textureVertSpv)
	if err != nil {
		return 0, 0, err
	}
	frag, err := mk(textureFragSpv)
	if err != nil {
		C.spike_vkDestroyShaderModule(ctx.vDestroyShaderModule, dev, C.uint64_t(uint64(vert)), nil)
		return 0, 0, err
	}
	return vert, frag, nil
}

// UploadTexture stages pixels through a host buffer into a device-local
// sampled image, like the C core's UploadTexture. It runs as its own
// one-shot submit on the frame's command resources (the frame re-waits and
// resets before recording, so the fence discipline is undisturbed).
func (q *QuadRenderer) UploadTexture(w, h uint32, pixels []byte) (*QuadTexture, error) {
	ctx, frame := q.ctx, q.frame
	dev := C.uint64_t(uint64(ctx.Logical))
	size := uint64(w) * uint64(h) * 4
	if uint64(len(pixels)) < size {
		return nil, ResultError(-1, "pixels smaller than texture")
	}

	stg, stgMem, err := q.allocBuffer(size, BUFFER_USAGE_TRANSFER_SRC, MEM_HOST_VISIBLE|MEM_HOST_COHERENT)
	if err != nil {
		return nil, err
	}
	var mapped *byte
	if res := Result(C.spike_vkMapMemory(ctx.vMapMemory, dev, C.uint64_t(uint64(stgMem)), 0, C.uint64_t(size), 0, unsafe.Pointer(&mapped))); res != 0 {
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkMapMemory (upload)")
	}
	copy(unsafe.Slice(mapped, size), pixels[:size])
	C.spike_vkUnmapMemory(ctx.vUnmapMemory, dev, C.uint64_t(uint64(stgMem)))

	imgInfo := VkImageCreateInfo{
		SType: STRUCT_IMAGE_CREATE_INFO, ImageType: IMAGE_TYPE_2D, Format: FORMAT_B8G8R8A8_SRGB,
		Extent: [3]uint32{w, h, 1}, MipLevels: 1, ArrayLayers: 1,
		Samples: SAMPLE_COUNT_1, Tiling: TILING_OPTIMAL,
		Usage:       IMG_USAGE_SAMPLED | IMG_USAGE_TRANSFER_DST,
		SharingMode: SHARING_EXCLUSIVE, InitialLayout: LAYOUT_UNDEFINED,
	}
	var img Image
	if res := Result(C.spike_vkCreateImage(ctx.vCreateImage, dev, unsafe.Pointer(&imgInfo), nil, unsafe.Pointer(&img))); res != 0 {
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkCreateImage (texture)")
	}
	var reqs VkMemoryRequirements
	C.spike_vkGetImageMemoryRequirements(ctx.vGetImageMemReqs, dev, C.uint64_t(uint64(img)), unsafe.Pointer(&reqs))
	imgType, err := ctx.memType(reqs.MemoryTypeBits, MEM_DEVICE_LOCAL)
	if err != nil {
		C.spike_vkDestroyImage(ctx.vDestroyImage, dev, C.uint64_t(uint64(img)), nil)
		q.freeBuffer(stg, stgMem)
		return nil, err
	}
	allocInfo := VkMemoryAllocateInfo{SType: STRUCT_MEMORY_ALLOCATE_INFO, AllocationSize: reqs.Size, MemoryTypeIndex: imgType}
	var imgMem DeviceMemory
	if res := Result(C.spike_vkAllocateMemory(ctx.vAllocateMemory, dev, unsafe.Pointer(&allocInfo), nil, unsafe.Pointer(&imgMem))); res != 0 {
		C.spike_vkDestroyImage(ctx.vDestroyImage, dev, C.uint64_t(uint64(img)), nil)
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkAllocateMemory (texture)")
	}
	if res := Result(C.spike_vkBindImageMemory(ctx.vBindImageMemory, dev, C.uint64_t(uint64(img)), C.uint64_t(uint64(imgMem)), 0)); res != 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(imgMem)), nil)
		C.spike_vkDestroyImage(ctx.vDestroyImage, dev, C.uint64_t(uint64(img)), nil)
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkBindImageMemory (texture)")
	}

	view := q.makeView(img)
	if view == 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(imgMem)), nil)
		C.spike_vkDestroyImage(ctx.vDestroyImage, dev, C.uint64_t(uint64(img)), nil)
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(-1, "vkCreateImageView (texture)")
	}

	// One-shot copy on the frame's command resources.
	if res := Result(C.spike_vkWaitForFences(ctx.vWaitForFences, dev, 1, unsafe.Pointer(&frame.fence), 1, 1_000_000_000)); res != 0 {
		q.destroyTexture(img, imgMem, view)
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkWaitForFences (upload)")
	}
	C.spike_vkResetFences(ctx.vResetFences, dev, 1, unsafe.Pointer(&frame.fence))
	C.spike_vkResetCommandBuffer(ctx.vResetCmdBuffer, C.uint64_t(uint64(frame.cmd)), 0)
	begin := VkCommandBufferBeginInfo{SType: STRUCT_COMMAND_BUFFER_BEGIN_INFO, Flags: CB_USAGE_ONE_TIME_SUBMIT}
	C.spike_vkBeginCommandBuffer(ctx.vBeginCmdBuffer, C.uint64_t(uint64(frame.cmd)), unsafe.Pointer(&begin))

	sub := VkImageSubresourceRange{AspectMask: ASPECT_COLOR, LevelCount: 1, LayerCount: 1}
	toDst := VkImageMemoryBarrier{
		SType: STRUCT_IMAGE_MEMORY_BARRIER, DstAccessMask: ACCESS_TRANSFER_WRITE,
		OldLayout: LAYOUT_UNDEFINED, NewLayout: LAYOUT_TRANSFER_DST,
		SrcQueueFamily: 0xFFFFFFFF, DstQueueFamily: 0xFFFFFFFF,
		Image: img, SubresourceRange: sub,
	}
	C.spike_vkCmdPipelineBarrier(ctx.vCmdPipelineBarrier, C.uint64_t(uint64(frame.cmd)),
		STAGE_TOP_OF_PIPE, STAGE_TRANSFER, 0, 0, nil, 0, nil, 1, unsafe.Pointer(&toDst))
	region := VkBufferImageCopy{
		ImageSubresource: VkImageSubresourceLayers{AspectMask: ASPECT_COLOR, LayerCount: 1},
		ImageExtent:      [3]uint32{w, h, 1},
	}
	C.spike_vkCmdCopyBufferToImage(ctx.vCmdCopyBufferToImage, C.uint64_t(uint64(frame.cmd)),
		C.uint64_t(uint64(stg)), C.uint64_t(uint64(img)), LAYOUT_TRANSFER_DST, 1, unsafe.Pointer(&region))
	toShader := toDst
	toShader.OldLayout, toShader.NewLayout = LAYOUT_TRANSFER_DST, 5                    // SHADER_READ_ONLY_OPTIMAL
	toShader.SrcAccessMask, toShader.DstAccessMask = ACCESS_TRANSFER_WRITE, 0x00000020 // SHADER_READ
	C.spike_vkCmdPipelineBarrier(ctx.vCmdPipelineBarrier, C.uint64_t(uint64(frame.cmd)),
		STAGE_TRANSFER, 0x00000080, 0, 0, nil, 0, nil, 1, unsafe.Pointer(&toShader)) // FRAGMENT_SHADER

	C.spike_vkEndCommandBuffer(ctx.vEndCmdBuffer, C.uint64_t(uint64(frame.cmd)))
	submit := VkSubmitInfo{SType: STRUCT_SUBMIT_INFO, CommandBufferCount: 1, PCommandBuffers: &frame.cmd}
	submitPtr, freeSubmit := envelope(unsafe.Pointer(&submit), unsafe.Sizeof(submit))
	if res := Result(C.spike_vkQueueSubmit(ctx.vQueueSubmit, C.uint64_t(uint64(ctx.Queue)), 1, submitPtr, C.uint64_t(uint64(frame.fence)))); res != 0 {
		freeSubmit()
		q.destroyTexture(img, imgMem, view)
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkQueueSubmit (upload)")
	}
	freeSubmit()
	if res := Result(C.spike_vkWaitForFences(ctx.vWaitForFences, dev, 1, unsafe.Pointer(&frame.fence), 1, 1_000_000_000)); res != 0 {
		q.destroyTexture(img, imgMem, view)
		q.freeBuffer(stg, stgMem)
		return nil, ResultError(res, "vkWaitForFences (upload done)")
	}
	q.freeBuffer(stg, stgMem)

	return &QuadTexture{img: img, mem: imgMem, view: view, w: w, h: h}, nil
}

// Render records a render pass with the given textured draws, then the
// copy-out, submit, wait and readback. descNext resets here: each draw in
// this frame takes the next ring slot, which is the entire fix from the C
// bug, now visible as two Go lines.
func (q *QuadRenderer) Render(clear [4]float32, draws []QuadDraw, screenW, screenH float32) ([]byte, time.Duration, error) {
	ctx, frame := q.ctx, q.frame
	dev := C.uint64_t(uint64(ctx.Logical))
	start := time.Now()

	if res := Result(C.spike_vkWaitForFences(ctx.vWaitForFences, dev, 1, unsafe.Pointer(&frame.fence), 1, 1_000_000_000)); res != 0 {
		return nil, 0, ResultError(res, "vkWaitForFences")
	}
	C.spike_vkResetFences(ctx.vResetFences, dev, 1, unsafe.Pointer(&frame.fence))
	C.spike_vkResetCommandBuffer(ctx.vResetCmdBuffer, C.uint64_t(uint64(frame.cmd)), 0)
	begin := VkCommandBufferBeginInfo{SType: STRUCT_COMMAND_BUFFER_BEGIN_INFO, Flags: CB_USAGE_ONE_TIME_SUBMIT}
	if res := Result(C.spike_vkBeginCommandBuffer(ctx.vBeginCmdBuffer, C.uint64_t(uint64(frame.cmd)), unsafe.Pointer(&begin))); res != 0 {
		return nil, 0, ResultError(res, "vkBeginCommandBuffer")
	}

	clearVal := VkClearValue{Color: clear}
	rpBegin := VkRenderPassBeginInfo{
		SType: STRUCT_RENDER_PASS_BEGIN_INFO, RenderPass: q.renderPass, Framebuffer: q.framebuf,
		RenderArea:      VkRect2D{Extent: [2]uint32{frame.width, frame.height}},
		ClearValueCount: 1, PClearValues: &clearVal,
	}
	rpPtr, freeRp := envelope(unsafe.Pointer(&rpBegin), unsafe.Sizeof(rpBegin))
	defer freeRp()
	C.spike_vkCmdBeginRenderPass(ctx.vCmdBeginRenderPass, C.uint64_t(uint64(frame.cmd)), rpPtr, 0)

	C.spike_vkCmdBindPipeline(ctx.vCmdBindPipeline, C.uint64_t(uint64(frame.cmd)), BIND_POINT_GRAPHICS, C.uint64_t(uint64(q.pipeline)))
	q.descNext = 0
	for _, d := range draws {
		if d.Tex == nil {
			continue
		}
		slot := q.descNext
		q.descNext++
		if slot >= uint32(len(q.descSets)) {
			slot = uint32(len(q.descSets)) - 1
		}
		imgInfo := VkDescriptorImageInfo{
			Sampler: q.sampler, ImageView: d.Tex.view, ImageLayout: 5, // SHADER_READ_ONLY_OPTIMAL
		}
		write := VkWriteDescriptorSet{
			SType: STRUCT_WRITE_DESCRIPTOR_SET, DstSet: q.descSets[slot],
			DstBinding: 0, DescriptorCount: 1, DescriptorType: DESC_COMBINED_SAMPLER,
			PImageInfo: &imgInfo,
		}
		writePtr, freeWrite := envelope(unsafe.Pointer(&write), unsafe.Sizeof(write))
		C.spike_vkUpdateDescriptorSets(ctx.vUpdateDescSets, dev, 1, writePtr, 0, nil)
		freeWrite()

		C.spike_vkCmdBindDescriptorSets(ctx.vCmdBindDescSets, C.uint64_t(uint64(frame.cmd)),
			BIND_POINT_GRAPHICS, C.uint64_t(uint64(q.layout)), 0, 1, unsafe.Pointer(&q.descSets[slot]), 0, nil)

		pc := [11]float32{
			d.Dst[0], d.Dst[1], d.Dst[2], d.Dst[3],
			d.Src[0], d.Src[1], d.Src[2], d.Src[3],
			screenW, screenH, d.Alpha,
		}
		C.spike_vkCmdPushConstants(ctx.vCmdPushConstants, C.uint64_t(uint64(frame.cmd)),
			C.uint64_t(uint64(q.layout)), SHADER_STAGE_VERT, 0, 44, unsafe.Pointer(&pc[0]))
		C.spike_vkCmdDraw(ctx.vCmdDraw, C.uint64_t(uint64(frame.cmd)), 4, 1, 0, 0)
	}
	C.spike_vkCmdEndRenderPass(ctx.vCmdEndRenderPass, C.uint64_t(uint64(frame.cmd)))

	// Target back to TRANSFER_SRC for the copy-out.
	sub := VkImageSubresourceRange{AspectMask: ASPECT_COLOR, LevelCount: 1, LayerCount: 1}
	toSrc := VkImageMemoryBarrier{
		SType:         STRUCT_IMAGE_MEMORY_BARRIER,
		SrcAccessMask: ACCESS_COLOR_ATTACH_WRITE, DstAccessMask: ACCESS_TRANSFER_READ,
		OldLayout: 2, NewLayout: LAYOUT_TRANSFER_SRC, // COLOR_ATTACHMENT_OPTIMAL
		SrcQueueFamily: 0xFFFFFFFF, DstQueueFamily: 0xFFFFFFFF,
		Image: frame.img, SubresourceRange: sub,
	}
	C.spike_vkCmdPipelineBarrier(ctx.vCmdPipelineBarrier, C.uint64_t(uint64(frame.cmd)),
		STAGE_COLOR_ATTACHMENT, STAGE_TRANSFER, 0, 0, nil, 0, nil, 1, unsafe.Pointer(&toSrc))
	region := VkBufferImageCopy{
		ImageSubresource: VkImageSubresourceLayers{AspectMask: ASPECT_COLOR, LayerCount: 1},
		ImageExtent:      [3]uint32{frame.width, frame.height, 1},
	}
	C.spike_vkCmdCopyImageToBuffer(ctx.vCmdCopyImageToBuf, C.uint64_t(uint64(frame.cmd)),
		C.uint64_t(uint64(frame.img)), LAYOUT_TRANSFER_SRC, C.uint64_t(uint64(frame.stg)), 1, unsafe.Pointer(&region))

	if res := Result(C.spike_vkEndCommandBuffer(ctx.vEndCmdBuffer, C.uint64_t(uint64(frame.cmd)))); res != 0 {
		return nil, 0, ResultError(res, "vkEndCommandBuffer")
	}
	submit := VkSubmitInfo{SType: STRUCT_SUBMIT_INFO, CommandBufferCount: 1, PCommandBuffers: &frame.cmd}
	submitPtr, freeSubmit := envelope(unsafe.Pointer(&submit), unsafe.Sizeof(submit))
	if res := Result(C.spike_vkQueueSubmit(ctx.vQueueSubmit, C.uint64_t(uint64(ctx.Queue)), 1, submitPtr, C.uint64_t(uint64(frame.fence)))); res != 0 {
		freeSubmit()
		return nil, 0, ResultError(res, "vkQueueSubmit")
	}
	freeSubmit()
	if res := Result(C.spike_vkWaitForFences(ctx.vWaitForFences, dev, 1, unsafe.Pointer(&frame.fence), 1, 1_000_000_000)); res != 0 {
		return nil, 0, ResultError(res, "vkWaitForFences (frame)")
	}

	var mapped *byte
	if res := Result(C.spike_vkMapMemory(ctx.vMapMemory, dev, C.uint64_t(uint64(frame.stgMem)), 0, C.uint64_t(frame.stgLen), 0, unsafe.Pointer(&mapped))); res != 0 {
		return nil, 0, ResultError(res, "vkMapMemory")
	}
	copy(frame.out, unsafe.Slice(mapped, frame.stgLen))
	C.spike_vkUnmapMemory(ctx.vUnmapMemory, dev, C.uint64_t(uint64(frame.stgMem)))
	return frame.out, time.Since(start), nil
}

// allocBuffer creates a host- or device-visible buffer with bound memory.
func (q *QuadRenderer) allocBuffer(size uint64, usage, props uint32) (Buffer, DeviceMemory, error) {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	info := VkBufferCreateInfo{SType: STRUCT_BUFFER_CREATE_INFO, Size: size, Usage: usage, SharingMode: SHARING_EXCLUSIVE}
	var buf Buffer
	if res := Result(C.spike_vkCreateBuffer(ctx.vCreateBuffer, dev, unsafe.Pointer(&info), nil, unsafe.Pointer(&buf))); res != 0 {
		return 0, 0, ResultError(res, "vkCreateBuffer")
	}
	var reqs VkMemoryRequirements
	C.spike_vkGetBufferMemoryRequirements(ctx.vGetBufferMemReqs, dev, C.uint64_t(uint64(buf)), unsafe.Pointer(&reqs))
	memType, err := ctx.memType(reqs.MemoryTypeBits, props)
	if err != nil {
		C.spike_vkDestroyBuffer(ctx.vDestroyBuffer, dev, C.uint64_t(uint64(buf)), nil)
		return 0, 0, err
	}
	allocInfo := VkMemoryAllocateInfo{SType: STRUCT_MEMORY_ALLOCATE_INFO, AllocationSize: reqs.Size, MemoryTypeIndex: memType}
	var mem DeviceMemory
	if res := Result(C.spike_vkAllocateMemory(ctx.vAllocateMemory, dev, unsafe.Pointer(&allocInfo), nil, unsafe.Pointer(&mem))); res != 0 {
		C.spike_vkDestroyBuffer(ctx.vDestroyBuffer, dev, C.uint64_t(uint64(buf)), nil)
		return 0, 0, ResultError(res, "vkAllocateMemory (buffer)")
	}
	if res := Result(C.spike_vkBindBufferMemory(ctx.vBindBufferMemory, dev, C.uint64_t(uint64(buf)), C.uint64_t(uint64(mem)), 0)); res != 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(mem)), nil)
		C.spike_vkDestroyBuffer(ctx.vDestroyBuffer, dev, C.uint64_t(uint64(buf)), nil)
		return 0, 0, ResultError(res, "vkBindBufferMemory")
	}
	return buf, mem, nil
}

func (q *QuadRenderer) freeBuffer(buf Buffer, mem DeviceMemory) {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	if buf != 0 {
		C.spike_vkDestroyBuffer(ctx.vDestroyBuffer, dev, C.uint64_t(uint64(buf)), nil)
	}
	if mem != 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(mem)), nil)
	}
}

func (q *QuadRenderer) makeView(img Image) ImageView {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	info := VkImageViewCreateInfo{
		SType: STRUCT_IMAGE_VIEW_CREATE_INFO, Image: img,
		ViewType: 1, Format: 50,
		Components:       VkComponentMapping{R: 0, G: 0, B: 0, A: 0}, // IDENTITY x4
		SubresourceRange: VkImageSubresourceRange{AspectMask: ASPECT_COLOR, LevelCount: 1, LayerCount: 1},
	}
	var view ImageView
	if res := Result(C.spike_vkCreateImageView(ctx.vCreateImageView, dev, unsafe.Pointer(&info), nil, unsafe.Pointer(&view))); res != 0 {
		return 0
	}
	return view
}

func (q *QuadRenderer) destroyTexture(img Image, mem DeviceMemory, view ImageView) {
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	if view != 0 {
		C.spike_vkDestroyImageView(ctx.vDestroyImageView, dev, C.uint64_t(uint64(view)), nil)
	}
	if img != 0 {
		C.spike_vkDestroyImage(ctx.vDestroyImage, dev, C.uint64_t(uint64(img)), nil)
	}
	if mem != 0 {
		C.spike_vkFreeMemory(ctx.vFreeMemory, dev, C.uint64_t(uint64(mem)), nil)
	}
}

// DestroyTexture frees one uploaded texture's image, memory and view.
func (q *QuadRenderer) DestroyTexture(tex *QuadTexture) {
	if tex == nil {
		return
	}
	q.destroyTexture(tex.img, tex.mem, tex.view)
	*tex = QuadTexture{}
}

// Close destroys the renderer's pipeline objects in reverse creation order.
// Textures outlive it only if the caller destroys them first; destroying
// the device with live children is a validation error, not a crash, but
// clean teardown keeps the validation log usable.
func (q *QuadRenderer) Close() {
	if q == nil || q.ctx == nil {
		return
	}
	ctx := q.ctx
	dev := C.uint64_t(uint64(ctx.Logical))
	if q.pipeline != 0 {
		C.spike_vkDestroyPipeline(ctx.vDestroyPipeline, dev, C.uint64_t(uint64(q.pipeline)), nil)
		q.pipeline = 0
	}
	if q.layout != 0 {
		C.spike_vkDestroyPipelineLayout(ctx.vDestroyPipelineLayout, dev, C.uint64_t(uint64(q.layout)), nil)
		q.layout = 0
	}
	if q.descPool != 0 {
		C.spike_vkDestroyDescriptorPool(ctx.vDestroyDescPool, dev, C.uint64_t(uint64(q.descPool)), nil)
		q.descPool = 0
	}
	if q.descLayout != 0 {
		C.spike_vkDestroyDescriptorSetLayout(ctx.vDestroyDescSetLayout, dev, C.uint64_t(uint64(q.descLayout)), nil)
		q.descLayout = 0
	}
	if q.sampler != 0 {
		C.spike_vkDestroySampler(ctx.vDestroySampler, dev, C.uint64_t(uint64(q.sampler)), nil)
		q.sampler = 0
	}
	if q.framebuf != 0 {
		C.spike_vkDestroyFramebuffer(ctx.vDestroyFramebuffer, dev, C.uint64_t(uint64(q.framebuf)), nil)
		q.framebuf = 0
	}
	if q.targetView != 0 {
		C.spike_vkDestroyImageView(ctx.vDestroyImageView, dev, C.uint64_t(uint64(q.targetView)), nil)
		q.targetView = 0
	}
	if q.renderPass != 0 {
		C.spike_vkDestroyRenderPass(ctx.vDestroyRenderPass, dev, C.uint64_t(uint64(q.renderPass)), nil)
		q.renderPass = 0
	}
}
