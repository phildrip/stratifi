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
*/
import "C"
