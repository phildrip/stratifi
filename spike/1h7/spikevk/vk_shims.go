package spikevk

// vk_shims.go — the single definition site for every C shim in the spike.
// Go files that call into C each declare the prototypes they need in their
// own preamble (cgo resolves C.* per file, so a bare `import "C"` elsewhere
// sees nothing). Prototypes must match these definitions exactly; all
// handles cross as uint64_t, all results as int32_t, nothing by float.

/*
#include <dlfcn.h>
#include <stdlib.h>
#include <stdint.h>
#include <vulkan/vulkan.h>
#include <string.h>
void* spike_dlopen(const char* n)  { return dlopen(n, RTLD_NOW | RTLD_GLOBAL); }
void* spike_dlsym(void* h, const char* n) { return dlsym(h, n); }
int   spike_dlclose(void* h)       { return dlclose(h); }
void* spike_dup(const void* src, unsigned long n) {
    void* d = malloc(n);
    if (d && src) memcpy(d, src, n);
    return d;
}
void* spike_gipa(void* g, uint64_t inst, const char* n) {
    return ((PFN_vkGetInstanceProcAddr)g)((VkInstance)(uintptr_t)inst, n);
}
int32_t spike_vkCreateInstance(void* f, const void* i, void* a, void* o) {
    return (int32_t)((PFN_vkCreateInstance)f)((const VkInstanceCreateInfo*)i, (const VkAllocationCallbacks*)a, (VkInstance*)o);
}
void spike_vkDestroyInstance(void* f, uint64_t inst, void* a) {
    ((PFN_vkDestroyInstance)f)((VkInstance)(uintptr_t)inst, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkEnumeratePhysicalDevices(void* f, uint64_t inst, void* count, void* devs) {
    return (int32_t)((PFN_vkEnumeratePhysicalDevices)f)((VkInstance)(uintptr_t)inst, (uint32_t*)count, (VkPhysicalDevice*)devs);
}
void spike_vkGetPhysicalDeviceProperties(void* f, uint64_t pdev, void* props) {
    ((PFN_vkGetPhysicalDeviceProperties)f)((VkPhysicalDevice)(uintptr_t)pdev, (VkPhysicalDeviceProperties*)props);
}
void spike_vkGetPhysicalDeviceQueueFamilyProperties(void* f, uint64_t pdev, void* count, void* props) {
    ((PFN_vkGetPhysicalDeviceQueueFamilyProperties)f)((VkPhysicalDevice)(uintptr_t)pdev, (uint32_t*)count, (VkQueueFamilyProperties*)props);
}
void spike_vkGetPhysicalDeviceMemoryProperties(void* f, uint64_t pdev, void* props) {
    ((PFN_vkGetPhysicalDeviceMemoryProperties)f)((VkPhysicalDevice)(uintptr_t)pdev, (VkPhysicalDeviceMemoryProperties*)props);
}
int32_t spike_vkCreateDevice(void* f, uint64_t pdev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateDevice)f)((VkPhysicalDevice)(uintptr_t)pdev, (const VkDeviceCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkDevice*)out);
}
void spike_vkDestroyDevice(void* f, uint64_t dev, void* a) {
    ((PFN_vkDestroyDevice)f)((VkDevice)(uintptr_t)dev, (const VkAllocationCallbacks*)a);
}
void spike_vkGetDeviceQueue(void* f, uint64_t dev, uint32_t fam, uint32_t idx, void* out) {
    ((PFN_vkGetDeviceQueue)f)((VkDevice)(uintptr_t)dev, fam, idx, (VkQueue*)out);
}
int32_t spike_vkCreateCommandPool(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateCommandPool)f)((VkDevice)(uintptr_t)dev, (const VkCommandPoolCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkCommandPool*)out);
}
void spike_vkDestroyCommandPool(void* f, uint64_t dev, uint64_t pool, void* a) {
    ((PFN_vkDestroyCommandPool)f)((VkDevice)(uintptr_t)dev, (VkCommandPool)(uintptr_t)pool, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkAllocateCommandBuffers(void* f, uint64_t dev, const void* info, void* out) {
    return (int32_t)((PFN_vkAllocateCommandBuffers)f)((VkDevice)(uintptr_t)dev, (const VkCommandBufferAllocateInfo*)info, (VkCommandBuffer*)out);
}
int32_t spike_vkCreateFence(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateFence)f)((VkDevice)(uintptr_t)dev, (const VkFenceCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkFence*)out);
}
void spike_vkDestroyFence(void* f, uint64_t dev, uint64_t fence, void* a) {
    ((PFN_vkDestroyFence)f)((VkDevice)(uintptr_t)dev, (VkFence)(uintptr_t)fence, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkWaitForFences(void* f, uint64_t dev, uint32_t n, const void* fences, uint32_t waitAll, uint64_t timeout) {
    return (int32_t)((PFN_vkWaitForFences)f)((VkDevice)(uintptr_t)dev, n, (const VkFence*)fences, waitAll, timeout);
}
int32_t spike_vkResetFences(void* f, uint64_t dev, uint32_t n, const void* fences) {
    return (int32_t)((PFN_vkResetFences)f)((VkDevice)(uintptr_t)dev, n, (const VkFence*)fences);
}
int32_t spike_vkBeginCommandBuffer(void* f, uint64_t cb, const void* info) {
    return (int32_t)((PFN_vkBeginCommandBuffer)f)((VkCommandBuffer)(uintptr_t)cb, (const VkCommandBufferBeginInfo*)info);
}
int32_t spike_vkEndCommandBuffer(void* f, uint64_t cb) {
    return (int32_t)((PFN_vkEndCommandBuffer)f)((VkCommandBuffer)(uintptr_t)cb);
}
int32_t spike_vkResetCommandBuffer(void* f, uint64_t cb, uint32_t flags) {
    return (int32_t)((PFN_vkResetCommandBuffer)f)((VkCommandBuffer)(uintptr_t)cb, flags);
}
int32_t spike_vkQueueSubmit(void* f, uint64_t q, uint32_t n, const void* submits, uint64_t fence) {
    return (int32_t)((PFN_vkQueueSubmit)f)((VkQueue)(uintptr_t)q, n, (const VkSubmitInfo*)submits, (VkFence)(uintptr_t)fence);
}
int32_t spike_vkQueueWaitIdle(void* f, uint64_t q) {
    return (int32_t)((PFN_vkQueueWaitIdle)f)((VkQueue)(uintptr_t)q);
}
int32_t spike_vkCreateImage(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateImage)f)((VkDevice)(uintptr_t)dev, (const VkImageCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkImage*)out);
}
void spike_vkDestroyImage(void* f, uint64_t dev, uint64_t img, void* a) {
    ((PFN_vkDestroyImage)f)((VkDevice)(uintptr_t)dev, (VkImage)(uintptr_t)img, (const VkAllocationCallbacks*)a);
}
void spike_vkGetImageMemoryRequirements(void* f, uint64_t dev, uint64_t img, void* out) {
    ((PFN_vkGetImageMemoryRequirements)f)((VkDevice)(uintptr_t)dev, (VkImage)(uintptr_t)img, (VkMemoryRequirements*)out);
}
int32_t spike_vkAllocateMemory(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkAllocateMemory)f)((VkDevice)(uintptr_t)dev, (const VkMemoryAllocateInfo*)info, (const VkAllocationCallbacks*)a, (VkDeviceMemory*)out);
}
void spike_vkFreeMemory(void* f, uint64_t dev, uint64_t mem, void* a) {
    ((PFN_vkFreeMemory)f)((VkDevice)(uintptr_t)dev, (VkDeviceMemory)(uintptr_t)mem, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkMapMemory(void* f, uint64_t dev, uint64_t mem, uint64_t off, uint64_t size, uint32_t flags, void* pp) {
    return (int32_t)((PFN_vkMapMemory)f)((VkDevice)(uintptr_t)dev, (VkDeviceMemory)(uintptr_t)mem, off, size, flags, (void**)pp);
}
void spike_vkUnmapMemory(void* f, uint64_t dev, uint64_t mem) {
    ((PFN_vkUnmapMemory)f)((VkDevice)(uintptr_t)dev, (VkDeviceMemory)(uintptr_t)mem);
}
int32_t spike_vkBindImageMemory(void* f, uint64_t dev, uint64_t img, uint64_t mem, uint64_t off) {
    return (int32_t)((PFN_vkBindImageMemory)f)((VkDevice)(uintptr_t)dev, (VkImage)(uintptr_t)img, (VkDeviceMemory)(uintptr_t)mem, off);
}
int32_t spike_vkCreateBuffer(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateBuffer)f)((VkDevice)(uintptr_t)dev, (const VkBufferCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkBuffer*)out);
}
void spike_vkDestroyBuffer(void* f, uint64_t dev, uint64_t buf, void* a) {
    ((PFN_vkDestroyBuffer)f)((VkDevice)(uintptr_t)dev, (VkBuffer)(uintptr_t)buf, (const VkAllocationCallbacks*)a);
}
void spike_vkGetBufferMemoryRequirements(void* f, uint64_t dev, uint64_t buf, void* out) {
    ((PFN_vkGetBufferMemoryRequirements)f)((VkDevice)(uintptr_t)dev, (VkBuffer)(uintptr_t)buf, (VkMemoryRequirements*)out);
}
int32_t spike_vkBindBufferMemory(void* f, uint64_t dev, uint64_t buf, uint64_t mem, uint64_t off) {
    return (int32_t)((PFN_vkBindBufferMemory)f)((VkDevice)(uintptr_t)dev, (VkBuffer)(uintptr_t)buf, (VkDeviceMemory)(uintptr_t)mem, off);
}
void spike_vkCmdPipelineBarrier(void* f, uint64_t cb, uint32_t src, uint32_t dst, uint32_t dep,
        uint32_t mc, const void* pm, uint32_t bc, const void* pb, uint32_t ic, const void* pi) {
    ((PFN_vkCmdPipelineBarrier)f)((VkCommandBuffer)(uintptr_t)cb, (VkPipelineStageFlags)src, (VkPipelineStageFlags)dst,
        dep, mc, (const VkMemoryBarrier*)pm, bc, (const VkBufferMemoryBarrier*)pb, ic, (const VkImageMemoryBarrier*)pi);
}
void spike_vkCmdClearColorImage(void* f, uint64_t cb, uint64_t img, int32_t layout, const void* clear, uint32_t n, const void* ranges) {
    ((PFN_vkCmdClearColorImage)f)((VkCommandBuffer)(uintptr_t)cb, (VkImage)(uintptr_t)img, (VkImageLayout)layout,
        (const VkClearColorValue*)clear, n, (const VkImageSubresourceRange*)ranges);
}
void spike_vkCmdCopyImageToBuffer(void* f, uint64_t cb, uint64_t img, int32_t layout, uint64_t buf, uint32_t n, const void* regions) {
    ((PFN_vkCmdCopyImageToBuffer)f)((VkCommandBuffer)(uintptr_t)cb, (VkImage)(uintptr_t)img, (VkImageLayout)layout,
        (VkBuffer)(uintptr_t)buf, n, (const VkBufferImageCopy*)regions);
}
void spike_vkCmdCopyBufferToImage(void* f, uint64_t cb, uint64_t buf, uint64_t img, int32_t layout, uint32_t n, const void* regions) {
    ((PFN_vkCmdCopyBufferToImage)f)((VkCommandBuffer)(uintptr_t)cb, (VkBuffer)(uintptr_t)buf, (VkImage)(uintptr_t)img, (VkImageLayout)layout,
        n, (const VkBufferImageCopy*)regions);
}
int32_t spike_vkCreateImageView(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateImageView)f)((VkDevice)(uintptr_t)dev, (const VkImageViewCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkImageView*)out);
}
void spike_vkDestroyImageView(void* f, uint64_t dev, uint64_t view, void* a) {
    ((PFN_vkDestroyImageView)f)((VkDevice)(uintptr_t)dev, (VkImageView)(uintptr_t)view, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateShaderModule(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateShaderModule)f)((VkDevice)(uintptr_t)dev, (const VkShaderModuleCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkShaderModule*)out);
}
void spike_vkDestroyShaderModule(void* f, uint64_t dev, uint64_t mod, void* a) {
    ((PFN_vkDestroyShaderModule)f)((VkDevice)(uintptr_t)dev, (VkShaderModule)(uintptr_t)mod, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateDescriptorSetLayout(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateDescriptorSetLayout)f)((VkDevice)(uintptr_t)dev, (const VkDescriptorSetLayoutCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkDescriptorSetLayout*)out);
}
void spike_vkDestroyDescriptorSetLayout(void* f, uint64_t dev, uint64_t layout, void* a) {
    ((PFN_vkDestroyDescriptorSetLayout)f)((VkDevice)(uintptr_t)dev, (VkDescriptorSetLayout)(uintptr_t)layout, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateDescriptorPool(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateDescriptorPool)f)((VkDevice)(uintptr_t)dev, (const VkDescriptorPoolCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkDescriptorPool*)out);
}
void spike_vkDestroyDescriptorPool(void* f, uint64_t dev, uint64_t pool, void* a) {
    ((PFN_vkDestroyDescriptorPool)f)((VkDevice)(uintptr_t)dev, (VkDescriptorPool)(uintptr_t)pool, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkAllocateDescriptorSets(void* f, uint64_t dev, const void* info, void* out) {
    return (int32_t)((PFN_vkAllocateDescriptorSets)f)((VkDevice)(uintptr_t)dev, (const VkDescriptorSetAllocateInfo*)info, (VkDescriptorSet*)out);
}
void spike_vkUpdateDescriptorSets(void* f, uint64_t dev, uint32_t nw, const void* writes, uint32_t nc, const void* copies) {
    ((PFN_vkUpdateDescriptorSets)f)((VkDevice)(uintptr_t)dev, nw, (const VkWriteDescriptorSet*)writes, nc, (const VkCopyDescriptorSet*)copies);
}
int32_t spike_vkCreatePipelineLayout(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreatePipelineLayout)f)((VkDevice)(uintptr_t)dev, (const VkPipelineLayoutCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkPipelineLayout*)out);
}
void spike_vkDestroyPipelineLayout(void* f, uint64_t dev, uint64_t layout, void* a) {
    ((PFN_vkDestroyPipelineLayout)f)((VkDevice)(uintptr_t)dev, (VkPipelineLayout)(uintptr_t)layout, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateGraphicsPipelines(void* f, uint64_t dev, uint64_t cache, uint32_t n, const void* infos, void* a, void* out) {
    return (int32_t)((PFN_vkCreateGraphicsPipelines)f)((VkDevice)(uintptr_t)dev, (VkPipelineCache)(uintptr_t)cache, n, (const VkGraphicsPipelineCreateInfo*)infos, (const VkAllocationCallbacks*)a, (VkPipeline*)out);
}
void spike_vkDestroyPipeline(void* f, uint64_t dev, uint64_t pipe, void* a) {
    ((PFN_vkDestroyPipeline)f)((VkDevice)(uintptr_t)dev, (VkPipeline)(uintptr_t)pipe, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateRenderPass(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateRenderPass)f)((VkDevice)(uintptr_t)dev, (const VkRenderPassCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkRenderPass*)out);
}
void spike_vkDestroyRenderPass(void* f, uint64_t dev, uint64_t rp, void* a) {
    ((PFN_vkDestroyRenderPass)f)((VkDevice)(uintptr_t)dev, (VkRenderPass)(uintptr_t)rp, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateFramebuffer(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateFramebuffer)f)((VkDevice)(uintptr_t)dev, (const VkFramebufferCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkFramebuffer*)out);
}
void spike_vkDestroyFramebuffer(void* f, uint64_t dev, uint64_t fb, void* a) {
    ((PFN_vkDestroyFramebuffer)f)((VkDevice)(uintptr_t)dev, (VkFramebuffer)(uintptr_t)fb, (const VkAllocationCallbacks*)a);
}
int32_t spike_vkCreateSampler(void* f, uint64_t dev, const void* info, void* a, void* out) {
    return (int32_t)((PFN_vkCreateSampler)f)((VkDevice)(uintptr_t)dev, (const VkSamplerCreateInfo*)info, (const VkAllocationCallbacks*)a, (VkSampler*)out);
}
void spike_vkDestroySampler(void* f, uint64_t dev, uint64_t s, void* a) {
    ((PFN_vkDestroySampler)f)((VkDevice)(uintptr_t)dev, (VkSampler)(uintptr_t)s, (const VkAllocationCallbacks*)a);
}
void spike_vkCmdBeginRenderPass(void* f, uint64_t cb, const void* info, int32_t contents) {
    ((PFN_vkCmdBeginRenderPass)f)((VkCommandBuffer)(uintptr_t)cb, (const VkRenderPassBeginInfo*)info, (VkSubpassContents)contents);
}
void spike_vkCmdEndRenderPass(void* f, uint64_t cb) {
    ((PFN_vkCmdEndRenderPass)f)((VkCommandBuffer)(uintptr_t)cb);
}
void spike_vkCmdBindPipeline(void* f, uint64_t cb, int32_t bind, uint64_t pipe) {
    ((PFN_vkCmdBindPipeline)f)((VkCommandBuffer)(uintptr_t)cb, (VkPipelineBindPoint)bind, (VkPipeline)(uintptr_t)pipe);
}
void spike_vkCmdBindDescriptorSets(void* f, uint64_t cb, int32_t bind, uint64_t layout, uint32_t first, uint32_t n, const void* sets, uint32_t ndyn, const void* dyn) {
    ((PFN_vkCmdBindDescriptorSets)f)((VkCommandBuffer)(uintptr_t)cb, (VkPipelineBindPoint)bind, (VkPipelineLayout)(uintptr_t)layout,
        first, n, (const VkDescriptorSet*)sets, ndyn, (const uint32_t*)dyn);
}
void spike_vkCmdPushConstants(void* f, uint64_t cb, uint64_t layout, uint32_t stages, uint32_t off, uint32_t size, const void* vals) {
    ((PFN_vkCmdPushConstants)f)((VkCommandBuffer)(uintptr_t)cb, (VkPipelineLayout)(uintptr_t)layout, stages, off, size, vals);
}
void spike_vkCmdDraw(void* f, uint64_t cb, uint32_t vcount, uint32_t icount, uint32_t firstv, uint32_t firsti) {
    ((PFN_vkCmdDraw)f)((VkCommandBuffer)(uintptr_t)cb, vcount, icount, firstv, firsti);
}
*/
import "C"
