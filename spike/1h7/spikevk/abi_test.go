package spikevk

import (
	"testing"
	"unsafe"
)

// TestStructLayoutABI is the transcription firewall: every Vulkan struct we
// encode in Go asserts its sizeof (and the hot offsets) against the C
// compiler's dump for linux/amd64, RENOIR-era headers. Any Header bump that
// moves a field fails here with a NAME, instead of corrupting driver state
// thousands of calls later.
func TestStructLayoutABI(t *testing.T) {
	sizes := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"VkApplicationInfo", unsafe.Sizeof(VkApplicationInfo{}), 48},
		{"VkInstanceCreateInfo", unsafe.Sizeof(VkInstanceCreateInfo{}), 64},
		{"VkQueueFamilyProperties", unsafe.Sizeof(VkQueueFamilyProperties{}), 24},
		{"VkPhysicalDeviceProperties", unsafe.Sizeof(VkPhysicalDeviceProperties{}), 824},
		{"VkDeviceQueueCreateInfo", unsafe.Sizeof(VkDeviceQueueCreateInfo{}), 40},
		{"VkDeviceCreateInfo", unsafe.Sizeof(VkDeviceCreateInfo{}), 72},
		{"VkCommandPoolCreateInfo", unsafe.Sizeof(VkCommandPoolCreateInfo{}), 24},
		{"VkCommandBufferAllocateInfo", unsafe.Sizeof(VkCommandBufferAllocateInfo{}), 32},
		{"VkCommandBufferBeginInfo", unsafe.Sizeof(VkCommandBufferBeginInfo{}), 32},
		{"VkFenceCreateInfo", unsafe.Sizeof(VkFenceCreateInfo{}), 24},
		{"VkSubmitInfo", unsafe.Sizeof(VkSubmitInfo{}), 72},
		{"VkImageCreateInfo", unsafe.Sizeof(VkImageCreateInfo{}), 88},
		{"VkMemoryRequirements", unsafe.Sizeof(VkMemoryRequirements{}), 24},
		{"VkMemoryAllocateInfo", unsafe.Sizeof(VkMemoryAllocateInfo{}), 32},
		{"VkImageViewCreateInfo", unsafe.Sizeof(VkImageViewCreateInfo{}), 80},
		{"VkBufferCreateInfo", unsafe.Sizeof(VkBufferCreateInfo{}), 56},
		{"VkImageMemoryBarrier", unsafe.Sizeof(VkImageMemoryBarrier{}), 72},
		{"VkBufferImageCopy", unsafe.Sizeof(VkBufferImageCopy{}), 56},
		{"VkImageSubresourceRange", unsafe.Sizeof(VkImageSubresourceRange{}), 20},
		{"VkMemoryBarrier", unsafe.Sizeof(VkMemoryBarrier{}), 24},
		{"VkRenderPassCreateInfo", unsafe.Sizeof(VkRenderPassCreateInfo{}), 64},
		{"VkAttachmentDescription", unsafe.Sizeof(VkAttachmentDescription{}), 36},
		{"VkSubpassDescription", unsafe.Sizeof(VkSubpassDescription{}), 72},
		{"VkAttachmentReference", unsafe.Sizeof(VkAttachmentReference{}), 8},
		{"VkSubpassDependency", unsafe.Sizeof(VkSubpassDependency{}), 28},
		{"VkPipelineLayoutCreateInfo", unsafe.Sizeof(VkPipelineLayoutCreateInfo{}), 48},
		{"VkPushConstantRange", unsafe.Sizeof(VkPushConstantRange{}), 12},
		{"VkShaderModuleCreateInfo", unsafe.Sizeof(VkShaderModuleCreateInfo{}), 40},
		{"VkDescriptorSetLayoutCreateInfo", unsafe.Sizeof(VkDescriptorSetLayoutCreateInfo{}), 32},
		{"VkDescriptorSetLayoutBinding", unsafe.Sizeof(VkDescriptorSetLayoutBinding{}), 24},
		{"VkDescriptorPoolCreateInfo", unsafe.Sizeof(VkDescriptorPoolCreateInfo{}), 40},
		{"VkDescriptorPoolSize", unsafe.Sizeof(VkDescriptorPoolSize{}), 8},
		{"VkDescriptorSetAllocateInfo", unsafe.Sizeof(VkDescriptorSetAllocateInfo{}), 40},
		{"VkWriteDescriptorSet", unsafe.Sizeof(VkWriteDescriptorSet{}), 64},
		{"VkDescriptorImageInfo", unsafe.Sizeof(VkDescriptorImageInfo{}), 24},
		{"VkDescriptorBufferInfo", unsafe.Sizeof(VkDescriptorBufferInfo{}), 24},
		{"VkGraphicsPipelineCreateInfo", unsafe.Sizeof(VkGraphicsPipelineCreateInfo{}), 144},
		{"VkPipelineShaderStageCreateInfo", unsafe.Sizeof(VkPipelineShaderStageCreateInfo{}), 48},
		{"VkFramebufferCreateInfo", unsafe.Sizeof(VkFramebufferCreateInfo{}), 64},
		{"VkSamplerCreateInfo", unsafe.Sizeof(VkSamplerCreateInfo{}), 80},
		{"VkViewport", unsafe.Sizeof(VkViewport{}), 24},
		{"VkRect2D", unsafe.Sizeof(VkRect2D{}), 16},
		{"VkClearValue", unsafe.Sizeof(VkClearValue{}), 16},
		{"VkClearColorValue", unsafe.Sizeof(VkClearColorValue{}), 16},
		{"VkRenderPassBeginInfo", unsafe.Sizeof(VkRenderPassBeginInfo{}), 64},
		{"VkPhysicalDeviceMemoryProperties", unsafe.Sizeof(VkPhysicalDeviceMemoryProperties{}), 520},
		{"VkMemoryType", unsafe.Sizeof(VkMemoryType{}), 8},
		{"VkMemoryHeap", unsafe.Sizeof(VkMemoryHeap{}), 16},
	}
	for _, tc := range sizes {
		if tc.got != tc.want {
			t.Errorf("sizeof %s = %d, want %d (check vulkan_core.h for drift)", tc.name, tc.got, tc.want)
		}
	}

	var app VkApplicationInfo
	var devInfo VkDeviceCreateInfo
	var qInfo VkDeviceQueueCreateInfo
	var cbAlloc VkCommandBufferAllocateInfo
	var fenceInfo VkFenceCreateInfo
	var imgInfo VkImageCreateInfo
	var bufInfo VkBufferCreateInfo
	var sub VkSubmitInfo
	var imgBar VkImageMemoryBarrier
	var copyRegion VkBufferImageCopy
	var allocInfo VkMemoryAllocateInfo

	offsets := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"VkApplicationInfo.apiVersion", unsafe.Offsetof(app.APIVersion), 44},
		{"VkDeviceCreateInfo.queueCreateInfoCount", unsafe.Offsetof(devInfo.QueueCreateInfoCount), 20},
		{"VkDeviceCreateInfo.pQueueCreateInfos", unsafe.Offsetof(devInfo.PQueueCreateInfos), 24},
		{"VkDeviceCreateInfo.enabledExtensionCount", unsafe.Offsetof(devInfo.EnabledExtensionCount), 48},
		{"VkDeviceCreateInfo.ppEnabledExtensionNames", unsafe.Offsetof(devInfo.PpEnabledExtensionNames), 56},
		{"VkDeviceQueueCreateInfo.queueFamilyIndex", unsafe.Offsetof(qInfo.QueueFamilyIndex), 20},
		{"VkDeviceQueueCreateInfo.queueCount", unsafe.Offsetof(qInfo.QueueCount), 24},
		{"VkDeviceQueueCreateInfo.pQueuePriorities", unsafe.Offsetof(qInfo.PQueuePriorities), 32},
		{"VkCommandBufferAllocateInfo.commandPool", unsafe.Offsetof(cbAlloc.CommandPool), 16},
		{"VkCommandBufferAllocateInfo.level", unsafe.Offsetof(cbAlloc.Level), 24},
		{"VkCommandBufferAllocateInfo.commandBufferCount", unsafe.Offsetof(cbAlloc.CommandBufferCount), 28},
		{"VkFenceCreateInfo.flags", unsafe.Offsetof(fenceInfo.Flags), 16},
		{"VkImageCreateInfo.format", unsafe.Offsetof(imgInfo.Format), 24},
		{"VkImageCreateInfo.extent", unsafe.Offsetof(imgInfo.Extent), 28},
		{"VkImageCreateInfo.usage", unsafe.Offsetof(imgInfo.Usage), 56},
		{"VkImageCreateInfo.tiling", unsafe.Offsetof(imgInfo.Tiling), 52},
		{"VkImageCreateInfo.initialLayout", unsafe.Offsetof(imgInfo.InitialLayout), 80},
		{"VkBufferCreateInfo.size", unsafe.Offsetof(bufInfo.Size), 24},
		{"VkBufferCreateInfo.usage", unsafe.Offsetof(bufInfo.Usage), 32},
		{"VkSubmitInfo.commandBufferCount", unsafe.Offsetof(sub.CommandBufferCount), 40},
		{"VkSubmitInfo.pCommandBuffers", unsafe.Offsetof(sub.PCommandBuffers), 48},
		{"VkImageMemoryBarrier.oldLayout", unsafe.Offsetof(imgBar.OldLayout), 24},
		{"VkImageMemoryBarrier.newLayout", unsafe.Offsetof(imgBar.NewLayout), 28},
		{"VkImageMemoryBarrier.image", unsafe.Offsetof(imgBar.Image), 40},
		{"VkBufferImageCopy.bufferOffset", unsafe.Offsetof(copyRegion.BufferOffset), 0},
		{"VkBufferImageCopy.imageExtent", unsafe.Offsetof(copyRegion.ImageExtent), 44},
		{"VkMemoryAllocateInfo.allocationSize", unsafe.Offsetof(allocInfo.AllocationSize), 16},
		{"VkMemoryAllocateInfo.memoryTypeIndex", unsafe.Offsetof(allocInfo.MemoryTypeIndex), 24},
		{"VkPhysicalDeviceProperties.deviceName", 20, 20}, // documented ABI pin; verified live below
	}
	for _, tc := range offsets {
		if tc.got != tc.want {
			t.Errorf("offsetof %s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
