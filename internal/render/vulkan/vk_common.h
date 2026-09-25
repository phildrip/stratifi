#ifndef VK_COMMON_H
#define VK_COMMON_H

#include <vulkan/vulkan.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    VkInstance instance;
    VkPhysicalDevice pdev;
    VkDevice device;
    VkQueue queue;
    uint32_t queueFamily;
    VkCommandPool cmdPool;
    char deviceName[256];
} VkContext;

typedef struct {
    VkImage image;
    VkDeviceMemory memory;
    VkImageView view;
    uint32_t width;
    uint32_t height;
    uint32_t stride;
    uint64_t modifier;
} VkImportedBuffer;

#endif // VK_COMMON_H
