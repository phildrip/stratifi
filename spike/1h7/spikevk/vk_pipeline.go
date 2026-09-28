package spikevk

// vk_pipeline.go — graphics pipeline state structs for the textured-quad
// crate. Sizes/offsets asserted in abi_test.go against the header dump.

import "unsafe"

// VkPipelineVertexInputStateCreateInfo: C size 48 (no bindings/attributes:
// the quad shader generates vertices from gl_VertexIndex).
type VkPipelineVertexInputStateCreateInfo struct {
	SType                           uint32
	PNext                           unsafe.Pointer
	Flags                           uint32
	VertexBindingDescriptionCount   uint32
	PVertexBindingDescriptions      unsafe.Pointer
	VertexAttributeDescriptionCount uint32
	PVertexAttributeDescriptions    unsafe.Pointer
}

// VkPipelineInputAssemblyStateCreateInfo: C size 32.
type VkPipelineInputAssemblyStateCreateInfo struct {
	SType                  uint32
	PNext                  unsafe.Pointer
	Flags                  uint32
	Topology               uint32
	PrimitiveRestartEnable uint32
}

// VkPipelineViewportStateCreateInfo: C size 48.
type VkPipelineViewportStateCreateInfo struct {
	SType         uint32
	PNext         unsafe.Pointer
	Flags         uint32
	ViewportCount uint32
	PViewports    *VkViewport
	ScissorCount  uint32
	PScissors     *VkRect2D
}

// VkPipelineRasterizationStateCreateInfo: C size 64.
type VkPipelineRasterizationStateCreateInfo struct {
	SType                   uint32
	PNext                   unsafe.Pointer
	Flags                   uint32
	DepthClampEnable        uint32
	RasterizerDiscardEnable uint32
	PolygonMode             uint32
	CullMode                uint32
	FrontFace               uint32
	DepthBiasEnable         uint32
	DepthBiasConstantFactor float32
	DepthBiasClamp          float32
	DepthBiasSlopeFactor    float32
	LineWidth               float32
}

// VkPipelineMultisampleStateCreateInfo: C size 48.
type VkPipelineMultisampleStateCreateInfo struct {
	SType                 uint32
	PNext                 unsafe.Pointer
	Flags                 uint32
	RasterizationSamples  uint32
	SampleShadingEnable   uint32
	MinSampleShading      float32
	PSampleMask           *uint32
	AlphaToCoverageEnable uint32
	AlphaToOneEnable      uint32
}

// VkPipelineColorBlendAttachmentState: C size 32.
type VkPipelineColorBlendAttachmentState struct {
	BlendEnable         uint32
	SrcColorBlendFactor uint32
	DstColorBlendFactor uint32
	ColorBlendOp        uint32
	SrcAlphaBlendFactor uint32
	DstAlphaBlendFactor uint32
	AlphaBlendOp        uint32
	ColorWriteMask      uint32
}

// VkPipelineColorBlendStateCreateInfo: C size 56.
type VkPipelineColorBlendStateCreateInfo struct {
	SType           uint32
	PNext           unsafe.Pointer
	Flags           uint32
	LogicOpEnable   uint32
	LogicOp         uint32
	AttachmentCount uint32
	PAttachments    *VkPipelineColorBlendAttachmentState
	BlendConstants  [4]float32
}

// VkPipelineDynamicStateCreateInfo: C size 32.
type VkPipelineDynamicStateCreateInfo struct {
	SType             uint32
	PNext             unsafe.Pointer
	Flags             uint32
	DynamicStateCount uint32
	PDynamicStates    *uint32
}

// Pipeline state enum values used below (vulkan_core.h).
const (
	TOPOLOGY_TRIANGLE_LIST    = 3
	TOPOLOGY_TRIANGLE_STRIP   = 4
	BLEND_SRC_ALPHA           = 6
	BLEND_ONE_MINUS_SRC_ALPHA = 7
	POLYGON_FILL              = 0
	CULL_NONE                 = 0
	FRONT_CCW                 = 0

	BLEND_ZERO      = 0
	BLEND_ONE       = 1
	BLEND_ADD       = 0
	LOGIC_COPY      = 3
	COLOR_WRITE_ALL = 0x0000000F

	DYNAMIC_VIEWPORT      = 0
	DYNAMIC_SCISSOR       = 1
	BIND_POINT_GRAPHICS   = 0
	SHADER_STAGE_VERT     = 0x00000001
	SHADER_STAGE_FRAG     = 0x00000010
	DESC_COMBINED_SAMPLER = 1

	FILTER_NEAREST = 0
	SAMPLER_CLAMP  = 2
	COMPARE_ALWAYS = 7
	BORDER_BLACK   = 3
)
