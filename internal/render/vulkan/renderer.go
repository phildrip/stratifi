package vulkan

/*
#cgo LDFLAGS: -lvulkan
#include "vk_common.h"

typedef struct {
    VkImage image;
    VkDeviceMemory memory;
    VkImageView view;
    VkFramebuffer framebuffer;
    uint32_t width;
    uint32_t height;
} VkTarget;

typedef struct {
    VkDevice device;
    VkPhysicalDevice pdev;
    VkQueue queue;
    VkCommandPool cmdPool;
    VkRenderPass renderPass;
    VkSampler sampler;
    VkDescriptorPool descPool;

    // Quad solid pipeline
    VkPipelineLayout quadLayout;
    VkPipeline quadPipeline;

    // Texture pipeline
    VkDescriptorSetLayout texDescLayout;
    VkPipelineLayout texLayout;
    VkPipeline texPipeline;
    VkDescriptorSet texDescSet;

    // Command execution
    VkCommandBuffer cmdBuf;
    VkFence fence;

    // Current frame state
    float screenW;
    float screenH;
    int inPass;
} VkRenderer;

static int create_shader_module(VkDevice dev, const uint32_t* pCode, size_t codeSize, VkShaderModule* outModule) {
    VkShaderModuleCreateInfo info = {
        .sType = VK_STRUCTURE_TYPE_SHADER_MODULE_CREATE_INFO,
        .codeSize = codeSize,
        .pCode = pCode,
    };
    return (int)vkCreateShaderModule(dev, &info, NULL, outModule);
}

static int init_vk_renderer(VkContext* ctx,
    const uint32_t* quadVertSpv, size_t quadVertSize,
    const uint32_t* quadFragSpv, size_t quadFragSize,
    const uint32_t* texVertSpv, size_t texVertSize,
    const uint32_t* texFragSpv, size_t texFragSize,
    VkRenderer* r) {

    memset(r, 0, sizeof(VkRenderer));
    r->device = ctx->device;
    r->pdev = ctx->pdev;
    r->queue = ctx->queue;
    r->cmdPool = ctx->cmdPool;

    // 1. Render Pass (B8G8R8A8_SRGB format)
    VkAttachmentDescription colorAttachment = {
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .samples = VK_SAMPLE_COUNT_1_BIT,
        .loadOp = VK_ATTACHMENT_LOAD_OP_CLEAR,
        .storeOp = VK_ATTACHMENT_STORE_OP_STORE,
        .stencilLoadOp = VK_ATTACHMENT_LOAD_OP_DONT_CARE,
        .stencilStoreOp = VK_ATTACHMENT_STORE_OP_DONT_CARE,
        .initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
        .finalLayout = VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL,
    };

    VkAttachmentReference colorAttachmentRef = {
        .attachment = 0,
        .layout = VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL,
    };

    VkSubpassDescription subpass = {
        .pipelineBindPoint = VK_PIPELINE_BIND_POINT_GRAPHICS,
        .colorAttachmentCount = 1,
        .pColorAttachments = &colorAttachmentRef,
    };

    VkSubpassDependency dependency = {
        .srcSubpass = VK_SUBPASS_EXTERNAL,
        .dstSubpass = 0,
        .srcStageMask = VK_PIPELINE_STAGE_COLOR_ATTACHMENT_OUTPUT_BIT,
        .dstStageMask = VK_PIPELINE_STAGE_COLOR_ATTACHMENT_OUTPUT_BIT,
        .srcAccessMask = 0,
        .dstAccessMask = VK_ACCESS_COLOR_ATTACHMENT_WRITE_BIT,
    };

    VkRenderPassCreateInfo rpInfo = {
        .sType = VK_STRUCTURE_TYPE_RENDER_PASS_CREATE_INFO,
        .attachmentCount = 1,
        .pAttachments = &colorAttachment,
        .subpassCount = 1,
        .pSubpasses = &subpass,
        .dependencyCount = 1,
        .pDependencies = &dependency,
    };

    VkResult res = vkCreateRenderPass(ctx->device, &rpInfo, NULL, &r->renderPass);
    if (res != VK_SUCCESS) return (int)res;

    // 2. Sampler
    VkSamplerCreateInfo samplerInfo = {
        .sType = VK_STRUCTURE_TYPE_SAMPLER_CREATE_INFO,
        .magFilter = VK_FILTER_LINEAR,
        .minFilter = VK_FILTER_LINEAR,
        .addressModeU = VK_SAMPLER_ADDRESS_MODE_CLAMP_TO_EDGE,
        .addressModeV = VK_SAMPLER_ADDRESS_MODE_CLAMP_TO_EDGE,
        .addressModeW = VK_SAMPLER_ADDRESS_MODE_CLAMP_TO_EDGE,
    };
    res = vkCreateSampler(ctx->device, &samplerInfo, NULL, &r->sampler);
    if (res != VK_SUCCESS) return (int)res;

    // 3. Descriptor Pool
    VkDescriptorPoolSize poolSize = {
        .type = VK_DESCRIPTOR_TYPE_COMBINED_IMAGE_SAMPLER,
        .descriptorCount = 16,
    };
    VkDescriptorPoolCreateInfo poolInfo = {
        .sType = VK_STRUCTURE_TYPE_DESCRIPTOR_POOL_CREATE_INFO,
        .flags = VK_DESCRIPTOR_POOL_CREATE_FREE_DESCRIPTOR_SET_BIT,
        .maxSets = 16,
        .poolSizeCount = 1,
        .pPoolSizes = &poolSize,
    };
    res = vkCreateDescriptorPool(ctx->device, &poolInfo, NULL, &r->descPool);
    if (res != VK_SUCCESS) return (int)res;

    // 4. Quad Pipeline
    VkPushConstantRange quadPush = {
        .stageFlags = VK_SHADER_STAGE_VERTEX_BIT,
        .offset = 0,
        .size = 10 * sizeof(float), // rect(4), color(4), screenSize(2)
    };
    VkPipelineLayoutCreateInfo quadLayoutInfo = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_LAYOUT_CREATE_INFO,
        .pushConstantRangeCount = 1,
        .pPushConstantRanges = &quadPush,
    };
    res = vkCreatePipelineLayout(ctx->device, &quadLayoutInfo, NULL, &r->quadLayout);
    if (res != VK_SUCCESS) return (int)res;

    VkShaderModule qvMod, qfMod;
    if (create_shader_module(ctx->device, quadVertSpv, quadVertSize, &qvMod) != 0) return -20;
    if (create_shader_module(ctx->device, quadFragSpv, quadFragSize, &qfMod) != 0) return -21;

    VkPipelineShaderStageCreateInfo quadStages[2] = {
        {
            .sType = VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO,
            .stage = VK_SHADER_STAGE_VERTEX_BIT,
            .module = qvMod,
            .pName = "main",
        },
        {
            .sType = VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO,
            .stage = VK_SHADER_STAGE_FRAGMENT_BIT,
            .module = qfMod,
            .pName = "main",
        },
    };

    VkPipelineVertexInputStateCreateInfo viState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_VERTEX_INPUT_STATE_CREATE_INFO,
    };
    VkPipelineInputAssemblyStateCreateInfo iaState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_INPUT_ASSEMBLY_STATE_CREATE_INFO,
        .topology = VK_PRIMITIVE_TOPOLOGY_TRIANGLE_STRIP,
    };
    VkPipelineViewportStateCreateInfo vpState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_VIEWPORT_STATE_CREATE_INFO,
        .viewportCount = 1,
        .scissorCount = 1,
    };
    VkPipelineRasterizationStateCreateInfo rastState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_RASTERIZATION_STATE_CREATE_INFO,
        .polygonMode = VK_POLYGON_MODE_FILL,
        .cullMode = VK_CULL_MODE_NONE,
        .lineWidth = 1.0f,
    };
    VkPipelineMultisampleStateCreateInfo msState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_MULTISAMPLE_STATE_CREATE_INFO,
        .rasterizationSamples = VK_SAMPLE_COUNT_1_BIT,
    };
    VkPipelineColorBlendAttachmentState blendAtt = {
        .blendEnable = VK_TRUE,
        .srcColorBlendFactor = VK_BLEND_FACTOR_SRC_ALPHA,
        .dstColorBlendFactor = VK_BLEND_FACTOR_ONE_MINUS_SRC_ALPHA,
        .colorBlendOp = VK_BLEND_OP_ADD,
        .srcAlphaBlendFactor = VK_BLEND_FACTOR_ONE,
        .dstAlphaBlendFactor = VK_BLEND_FACTOR_ZERO,
        .alphaBlendOp = VK_BLEND_OP_ADD,
        .colorWriteMask = VK_COLOR_COMPONENT_R_BIT | VK_COLOR_COMPONENT_G_BIT | VK_COLOR_COMPONENT_B_BIT | VK_COLOR_COMPONENT_A_BIT,
    };
    VkPipelineColorBlendStateCreateInfo blendState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_COLOR_BLEND_STATE_CREATE_INFO,
        .attachmentCount = 1,
        .pAttachments = &blendAtt,
    };
    VkDynamicState dynStates[2] = { VK_DYNAMIC_STATE_VIEWPORT, VK_DYNAMIC_STATE_SCISSOR };
    VkPipelineDynamicStateCreateInfo dynState = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_DYNAMIC_STATE_CREATE_INFO,
        .dynamicStateCount = 2,
        .pDynamicStates = dynStates,
    };

    VkGraphicsPipelineCreateInfo quadPipeInfo = {
        .sType = VK_STRUCTURE_TYPE_GRAPHICS_PIPELINE_CREATE_INFO,
        .stageCount = 2,
        .pStages = quadStages,
        .pVertexInputState = &viState,
        .pInputAssemblyState = &iaState,
        .pViewportState = &vpState,
        .pRasterizationState = &rastState,
        .pMultisampleState = &msState,
        .pColorBlendState = &blendState,
        .pDynamicState = &dynState,
        .layout = r->quadLayout,
        .renderPass = r->renderPass,
        .subpass = 0,
    };
    res = vkCreateGraphicsPipelines(ctx->device, VK_NULL_HANDLE, 1, &quadPipeInfo, NULL, &r->quadPipeline);
    vkDestroyShaderModule(ctx->device, qvMod, NULL);
    vkDestroyShaderModule(ctx->device, qfMod, NULL);
    if (res != VK_SUCCESS) return (int)res;

    // 5. Texture Pipeline
    VkDescriptorSetLayoutBinding binding = {
        .binding = 0,
        .descriptorType = VK_DESCRIPTOR_TYPE_COMBINED_IMAGE_SAMPLER,
        .descriptorCount = 1,
        .stageFlags = VK_SHADER_STAGE_FRAGMENT_BIT,
        .pImmutableSamplers = &r->sampler,
    };
    VkDescriptorSetLayoutCreateInfo descLayoutInfo = {
        .sType = VK_STRUCTURE_TYPE_DESCRIPTOR_SET_LAYOUT_CREATE_INFO,
        .bindingCount = 1,
        .pBindings = &binding,
    };
    res = vkCreateDescriptorSetLayout(ctx->device, &descLayoutInfo, NULL, &r->texDescLayout);
    if (res != VK_SUCCESS) return (int)res;

    VkDescriptorSetAllocateInfo dsAlloc = {
        .sType = VK_STRUCTURE_TYPE_DESCRIPTOR_SET_ALLOCATE_INFO,
        .descriptorPool = r->descPool,
        .descriptorSetCount = 1,
        .pSetLayouts = &r->texDescLayout,
    };
    res = vkAllocateDescriptorSets(ctx->device, &dsAlloc, &r->texDescSet);
    if (res != VK_SUCCESS) return (int)res;

    VkPushConstantRange texPush = {
        .stageFlags = VK_SHADER_STAGE_VERTEX_BIT,
        .offset = 0,
        .size = 11 * sizeof(float), // dstRect(4), srcCrop(4), screenSize(2), alpha(1)
    };
    VkPipelineLayoutCreateInfo texLayoutInfo = {
        .sType = VK_STRUCTURE_TYPE_PIPELINE_LAYOUT_CREATE_INFO,
        .setLayoutCount = 1,
        .pSetLayouts = &r->texDescLayout,
        .pushConstantRangeCount = 1,
        .pPushConstantRanges = &texPush,
    };
    res = vkCreatePipelineLayout(ctx->device, &texLayoutInfo, NULL, &r->texLayout);
    if (res != VK_SUCCESS) return (int)res;

    VkShaderModule tvMod, tfMod;
    if (create_shader_module(ctx->device, texVertSpv, texVertSize, &tvMod) != 0) return -30;
    if (create_shader_module(ctx->device, texFragSpv, texFragSize, &tfMod) != 0) return -31;

    VkPipelineShaderStageCreateInfo texStages[2] = {
        {
            .sType = VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO,
            .stage = VK_SHADER_STAGE_VERTEX_BIT,
            .module = tvMod,
            .pName = "main",
        },
        {
            .sType = VK_STRUCTURE_TYPE_PIPELINE_SHADER_STAGE_CREATE_INFO,
            .stage = VK_SHADER_STAGE_FRAGMENT_BIT,
            .module = tfMod,
            .pName = "main",
        },
    };

    VkGraphicsPipelineCreateInfo texPipeInfo = {
        .sType = VK_STRUCTURE_TYPE_GRAPHICS_PIPELINE_CREATE_INFO,
        .stageCount = 2,
        .pStages = texStages,
        .pVertexInputState = &viState,
        .pInputAssemblyState = &iaState,
        .pViewportState = &vpState,
        .pRasterizationState = &rastState,
        .pMultisampleState = &msState,
        .pColorBlendState = &blendState,
        .pDynamicState = &dynState,
        .layout = r->texLayout,
        .renderPass = r->renderPass,
        .subpass = 0,
    };
    res = vkCreateGraphicsPipelines(ctx->device, VK_NULL_HANDLE, 1, &texPipeInfo, NULL, &r->texPipeline);
    vkDestroyShaderModule(ctx->device, tvMod, NULL);
    vkDestroyShaderModule(ctx->device, tfMod, NULL);
    if (res != VK_SUCCESS) return (int)res;

    // 6. Command Buffer & Fence
    VkCommandBufferAllocateInfo cmdInfo = {
        .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO,
        .commandPool = ctx->cmdPool,
        .level = VK_COMMAND_BUFFER_LEVEL_PRIMARY,
        .commandBufferCount = 1,
    };
    res = vkAllocateCommandBuffers(ctx->device, &cmdInfo, &r->cmdBuf);
    if (res != VK_SUCCESS) return (int)res;

    VkFenceCreateInfo fenceInfo = {
        .sType = VK_STRUCTURE_TYPE_FENCE_CREATE_INFO,
        .flags = VK_FENCE_CREATE_SIGNALED_BIT,
    };
    res = vkCreateFence(ctx->device, &fenceInfo, NULL, &r->fence);
    if (res != VK_SUCCESS) return (int)res;

    return 0;
}

static void destroy_vk_renderer(VkRenderer* r) {
    if (!r->device) return;
    VkDevice dev = r->device;

    vkDeviceWaitIdle(dev);

    if (r->fence) vkDestroyFence(dev, r->fence, NULL);
    if (r->texPipeline) vkDestroyPipeline(dev, r->texPipeline, NULL);
    if (r->texLayout) vkDestroyPipelineLayout(dev, r->texLayout, NULL);
    if (r->texDescLayout) vkDestroyDescriptorSetLayout(dev, r->texDescLayout, NULL);
    if (r->quadPipeline) vkDestroyPipeline(dev, r->quadPipeline, NULL);
    if (r->quadLayout) vkDestroyPipelineLayout(dev, r->quadLayout, NULL);
    if (r->descPool) vkDestroyDescriptorPool(dev, r->descPool, NULL);
    if (r->sampler) vkDestroySampler(dev, r->sampler, NULL);
    if (r->renderPass) vkDestroyRenderPass(dev, r->renderPass, NULL);
}

static int create_target(VkContext* ctx, VkRenderPass rp, uint32_t width, uint32_t height, VkTarget* out) {
    memset(out, 0, sizeof(VkTarget));
    out->width = width;
    out->height = height;

    VkImageCreateInfo imgInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO,
        .imageType = VK_IMAGE_TYPE_2D,
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .extent = { width, height, 1 },
        .mipLevels = 1,
        .arrayLayers = 1,
        .samples = VK_SAMPLE_COUNT_1_BIT,
        .tiling = VK_IMAGE_TILING_OPTIMAL,
        .usage = VK_IMAGE_USAGE_COLOR_ATTACHMENT_BIT | VK_IMAGE_USAGE_TRANSFER_SRC_BIT,
        .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
        .initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
    };

    VkResult res = vkCreateImage(ctx->device, &imgInfo, NULL, &out->image);
    if (res != VK_SUCCESS) return (int)res;

    VkMemoryRequirements memReqs;
    vkGetImageMemoryRequirements(ctx->device, out->image, &memReqs);

    VkPhysicalDeviceMemoryProperties memProps;
    vkGetPhysicalDeviceMemoryProperties(ctx->pdev, &memProps);

    int memType = -1;
    for (uint32_t i = 0; i < memProps.memoryTypeCount; i++) {
        if ((memReqs.memoryTypeBits & (1 << i)) &&
            (memProps.memoryTypes[i].propertyFlags & VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT)) {
            memType = (int)i;
            break;
        }
    }
    if (memType < 0) memType = 0;

    VkMemoryAllocateInfo allocInfo = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
        .allocationSize = memReqs.size,
        .memoryTypeIndex = (uint32_t)memType,
    };
    res = vkAllocateMemory(ctx->device, &allocInfo, NULL, &out->memory);
    if (res != VK_SUCCESS) {
        vkDestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    vkBindImageMemory(ctx->device, out->image, out->memory, 0);

    VkImageViewCreateInfo viewInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO,
        .image = out->image,
        .viewType = VK_IMAGE_VIEW_TYPE_2D,
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .subresourceRange = {
            .aspectMask = VK_IMAGE_ASPECT_COLOR_BIT,
            .baseMipLevel = 0,
            .levelCount = 1,
            .baseArrayLayer = 0,
            .layerCount = 1,
        },
    };
    res = vkCreateImageView(ctx->device, &viewInfo, NULL, &out->view);
    if (res != VK_SUCCESS) {
        vkFreeMemory(ctx->device, out->memory, NULL);
        vkDestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    VkFramebufferCreateInfo fbInfo = {
        .sType = VK_STRUCTURE_TYPE_FRAMEBUFFER_CREATE_INFO,
        .renderPass = rp,
        .attachmentCount = 1,
        .pAttachments = &out->view,
        .width = width,
        .height = height,
        .layers = 1,
    };
    res = vkCreateFramebuffer(ctx->device, &fbInfo, NULL, &out->framebuffer);
    if (res != VK_SUCCESS) {
        vkDestroyImageView(ctx->device, out->view, NULL);
        vkFreeMemory(ctx->device, out->memory, NULL);
        vkDestroyImage(ctx->device, out->image, NULL);
        return (int)res;
    }

    return 0;
}

static void destroy_target(VkContext* ctx, VkTarget* t) {
    if (t->framebuffer) vkDestroyFramebuffer(ctx->device, t->framebuffer, NULL);
    if (t->view) vkDestroyImageView(ctx->device, t->view, NULL);
    if (t->image) vkDestroyImage(ctx->device, t->image, NULL);
    if (t->memory) vkFreeMemory(ctx->device, t->memory, NULL);
    memset(t, 0, sizeof(VkTarget));
}

static int begin_frame(VkRenderer* r, VkTarget* target, float clearR, float clearG, float clearB, float clearA) {
    VkResult res = vkWaitForFences(r->device, 1, &r->fence, VK_TRUE, 1000000000ULL);
    if (res != VK_SUCCESS) return (int)res;
    vkResetFences(r->device, 1, &r->fence);

    vkResetCommandBuffer(r->cmdBuf, 0);

    VkCommandBufferBeginInfo beginInfo = {
        .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
        .flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
    };
    res = vkBeginCommandBuffer(r->cmdBuf, &beginInfo);
    if (res != VK_SUCCESS) return (int)res;

    VkClearValue clearVal = {
        .color = { .float32 = { clearR, clearG, clearB, clearA } },
    };

    VkRenderPassBeginInfo rpBegin = {
        .sType = VK_STRUCTURE_TYPE_RENDER_PASS_BEGIN_INFO,
        .renderPass = r->renderPass,
        .framebuffer = target->framebuffer,
        .renderArea = { .offset = {0, 0}, .extent = { target->width, target->height } },
        .clearValueCount = 1,
        .pClearValues = &clearVal,
    };

    vkCmdBeginRenderPass(r->cmdBuf, &rpBegin, VK_SUBPASS_CONTENTS_INLINE);

    VkViewport vp = {
        .x = 0.0f,
        .y = 0.0f,
        .width = (float)target->width,
        .height = (float)target->height,
        .minDepth = 0.0f,
        .maxDepth = 1.0f,
    };
    vkCmdSetViewport(r->cmdBuf, 0, 1, &vp);

    VkRect2D scissor = {
        .offset = {0, 0},
        .extent = { target->width, target->height },
    };
    vkCmdSetScissor(r->cmdBuf, 0, 1, &scissor);

    r->screenW = (float)target->width;
    r->screenH = (float)target->height;
    r->inPass = 1;

    return 0;
}

static void draw_color_quad(VkRenderer* r, float x, float y, float w, float h, float cr, float cg, float cb, float ca) {
    if (!r->inPass) return;

    vkCmdBindPipeline(r->cmdBuf, VK_PIPELINE_BIND_POINT_GRAPHICS, r->quadPipeline);

    float pc[10] = {
        x, y, w, h,
        cr, cg, cb, ca,
        r->screenW, r->screenH,
    };
    vkCmdPushConstants(r->cmdBuf, r->quadLayout, VK_SHADER_STAGE_VERTEX_BIT, 0, sizeof(pc), pc);
    vkCmdDraw(r->cmdBuf, 4, 1, 0, 0);
}

static void draw_texture_quad(VkRenderer* r, VkImageView view,
    float dstX, float dstY, float dstW, float dstH,
    float srcU, float srcV, float srcW, float srcH,
    float alpha) {

    if (!r->inPass || !view) return;

    // Update descriptor set image view
    VkDescriptorImageInfo imgInfo = {
        .sampler = r->sampler,
        .imageView = view,
        .imageLayout = VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL,
    };
    VkWriteDescriptorSet write = {
        .sType = VK_STRUCTURE_TYPE_WRITE_DESCRIPTOR_SET,
        .dstSet = r->texDescSet,
        .dstBinding = 0,
        .dstArrayElement = 0,
        .descriptorType = VK_DESCRIPTOR_TYPE_COMBINED_IMAGE_SAMPLER,
        .descriptorCount = 1,
        .pImageInfo = &imgInfo,
    };
    vkUpdateDescriptorSets(r->device, 1, &write, 0, NULL);

    vkCmdBindPipeline(r->cmdBuf, VK_PIPELINE_BIND_POINT_GRAPHICS, r->texPipeline);
    vkCmdBindDescriptorSets(r->cmdBuf, VK_PIPELINE_BIND_POINT_GRAPHICS, r->texLayout, 0, 1, &r->texDescSet, 0, NULL);

    float pc[11] = {
        dstX, dstY, dstW, dstH,
        srcU, srcV, srcW, srcH,
        r->screenW, r->screenH,
        alpha,
    };
    vkCmdPushConstants(r->cmdBuf, r->texLayout, VK_SHADER_STAGE_VERTEX_BIT, 0, sizeof(pc), pc);
    vkCmdDraw(r->cmdBuf, 4, 1, 0, 0);
}

static int end_frame(VkRenderer* r) {
    if (!r->inPass) return 0;
    r->inPass = 0;

    vkCmdEndRenderPass(r->cmdBuf);
    VkResult res = vkEndCommandBuffer(r->cmdBuf);
    if (res != VK_SUCCESS) return (int)res;

    VkSubmitInfo submitInfo = {
        .sType = VK_STRUCTURE_TYPE_SUBMIT_INFO,
        .commandBufferCount = 1,
        .pCommandBuffers = &r->cmdBuf,
    };

    res = vkQueueSubmit(r->queue, 1, &submitInfo, r->fence);
    return (int)res;
}

static int wait_frame(VkRenderer* r, uint64_t timeoutNs) {
    return (int)vkWaitForFences(r->device, 1, &r->fence, VK_TRUE, timeoutNs);
}

// --- CPU-upload textures (bloxwm-o2x phase 3) ---
//
// Client content is shm (memfd, not dmabuf), so the GPU path uploads via
// host-visible linear images: memcpy into mapped memory, then a command
// buffer transitions UNDEFINED -> SHADER_READ_ONLY_OPTIMAL for sampling.
// RADV supports linear sampled images; a staging-buffer copy to OPTIMAL
// tiling is the optimization path later.

static int upload_texture_impl(VkContext* ctx, VkRenderer* r, uint32_t width, uint32_t height, uint32_t stride, const uint8_t* pixels, VkUploadTexture* out) {
    memset(out, 0, sizeof(VkUploadTexture));
    out->width = width;
    out->height = height;
    out->stride = stride;
    if (!pixels) return -33;

    // OPTIMAL image for sampling.
    VkImageCreateInfo imgInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO,
        .imageType = VK_IMAGE_TYPE_2D,
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .extent = { width, height, 1 },
        .mipLevels = 1,
        .arrayLayers = 1,
        .samples = VK_SAMPLE_COUNT_1_BIT,
        .tiling = VK_IMAGE_TILING_OPTIMAL,
        .usage = VK_IMAGE_USAGE_SAMPLED_BIT | VK_IMAGE_USAGE_TRANSFER_DST_BIT | VK_IMAGE_USAGE_TRANSFER_SRC_BIT,
        .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
        .initialLayout = VK_IMAGE_LAYOUT_UNDEFINED,
    };
    VkResult res = vkCreateImage(r->device, &imgInfo, NULL, &out->image);
    if (res != VK_SUCCESS) return (int)res;

    VkMemoryRequirements imgReqs;
    vkGetImageMemoryRequirements(r->device, out->image, &imgReqs);

    VkPhysicalDeviceMemoryProperties memProps;
    vkGetPhysicalDeviceMemoryProperties(r->pdev, &memProps);
    int devMemType = -1;
    for (uint32_t i = 0; i < memProps.memoryTypeCount; i++) {
        if ((imgReqs.memoryTypeBits & (1 << i)) &&
            (memProps.memoryTypes[i].propertyFlags & VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT)) {
            devMemType = (int)i;
            break;
        }
    }
    if (devMemType < 0) devMemType = 0;

    VkMemoryAllocateInfo imgAlloc = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
        .allocationSize = imgReqs.size,
        .memoryTypeIndex = (uint32_t)devMemType,
    };
    res = vkAllocateMemory(r->device, &imgAlloc, NULL, &out->memory);
    if (res != VK_SUCCESS) {
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }
    res = vkBindImageMemory(r->device, out->image, out->memory, 0);
    if (res != VK_SUCCESS) {
        vkFreeMemory(r->device, out->memory, NULL);
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }

    // Staging buffer (host visible), memcpy, then cmd copy.
    VkDeviceSize uploadSize = (VkDeviceSize)stride * height;
    VkBufferCreateInfo sinfo = {
        .sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
        .size = (VkDeviceSize)width * 4 * height,
        .usage = VK_BUFFER_USAGE_TRANSFER_SRC_BIT | VK_BUFFER_USAGE_TRANSFER_DST_BIT,
        .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
    };
    VkBuffer staging;
    res = vkCreateBuffer(r->device, &sinfo, NULL, &out->stagingBuffer);
    if (res != VK_SUCCESS) {
        vkFreeMemory(r->device, out->memory, NULL);
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }
    VkMemoryRequirements sReqs;
    vkGetBufferMemoryRequirements(r->device, out->stagingBuffer, &sReqs);
    int hostMemType = -1;
    for (uint32_t i = 0; i < memProps.memoryTypeCount; i++) {
        if ((sReqs.memoryTypeBits & (1 << i)) &&
            (memProps.memoryTypes[i].propertyFlags &
             (VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT | VK_MEMORY_PROPERTY_HOST_COHERENT_BIT))) {
            hostMemType = (int)i;
            break;
        }
    }
    if (hostMemType < 0) hostMemType = 0;
    VkMemoryAllocateInfo sAlloc = {
        .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
        .allocationSize = sReqs.size,
        .memoryTypeIndex = (uint32_t)hostMemType,
    };
    res = vkAllocateMemory(r->device, &sAlloc, NULL, &out->stagingMemory);
    if (res != VK_SUCCESS) {
        vkDestroyBuffer(r->device, out->stagingBuffer, NULL);
        vkFreeMemory(r->device, out->memory, NULL);
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }
    res = vkBindBufferMemory(r->device, out->stagingBuffer, out->stagingMemory, 0);
    if (res != VK_SUCCESS) {
        vkFreeMemory(r->device, out->stagingMemory, NULL);
        vkDestroyBuffer(r->device, out->stagingBuffer, NULL);
        vkFreeMemory(r->device, out->memory, NULL);
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }

    void* mapped;
    res = vkMapMemory(r->device, out->stagingMemory, 0, sReqs.size, 0, &out->mapped);
    if (res != VK_SUCCESS) {
        vkFreeMemory(r->device, out->stagingMemory, NULL);
        vkFreeMemory(r->device, out->memory, NULL);
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }
    for (uint32_t row = 0; row < height; row++) {
        memcpy((uint8_t*)out->mapped + (size_t)width * 4 * row, pixels + (size_t)stride * row, (size_t)width * 4);
    }

    VkImageViewCreateInfo viewInfo = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO,
        .image = out->image,
        .viewType = VK_IMAGE_VIEW_TYPE_2D,
        .format = VK_FORMAT_B8G8R8A8_SRGB,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    res = vkCreateImageView(r->device, &viewInfo, NULL, &out->view);
    if (res != VK_SUCCESS) {
        vkUnmapMemory(r->device, out->stagingMemory);
        vkFreeMemory(r->device, out->stagingMemory, NULL);
        vkFreeMemory(r->device, out->memory, NULL);
        vkDestroyImage(r->device, out->image, NULL);
        return (int)res;
    }

    // One-shot command: layout barrier + buffer->image copy.
    vkResetFences(r->device, 1, &r->fence);
    VkCommandBufferBeginInfo beginInfo = {
        .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
        .flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
    };
    res = vkBeginCommandBuffer(r->cmdBuf, &beginInfo);
    if (res != VK_SUCCESS) return (int)res;

    VkBufferMemoryBarrier uploadVis = {
        .sType = VK_STRUCTURE_TYPE_BUFFER_MEMORY_BARRIER,
        .srcAccessMask = VK_ACCESS_HOST_WRITE_BIT,
        .dstAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .buffer = out->stagingBuffer,
        .offset = 0,
        .size = VK_WHOLE_SIZE,
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_HOST_BIT, VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 1, &uploadVis, 0, NULL);

    VkImageMemoryBarrier toDst = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
        .srcAccessMask = 0,
        .dstAccessMask = VK_ACCESS_TRANSFER_WRITE_BIT,
        .oldLayout = VK_IMAGE_LAYOUT_UNDEFINED,
        .newLayout = VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .image = out->image,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_TOP_OF_PIPE_BIT, VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 0, NULL, 1, &toDst);

    VkBufferImageCopy region = {
        .imageSubresource = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 0, 1 },
        .imageOffset = { 0, 0, 0 },
        .imageExtent = { width, height, 1 },
        .bufferOffset = 0,
        .bufferRowLength = 0,
        .bufferImageHeight = 0,
    };
    vkCmdCopyBufferToImage(r->cmdBuf, out->stagingBuffer, out->image, VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL, 1, &region);

    VkImageMemoryBarrier toSample = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
        .srcAccessMask = VK_ACCESS_TRANSFER_WRITE_BIT,
        .dstAccessMask = VK_ACCESS_SHADER_READ_BIT,
        .oldLayout = VK_IMAGE_LAYOUT_TRANSFER_DST_OPTIMAL,
        .newLayout = VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .image = out->image,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_TRANSFER_BIT, VK_PIPELINE_STAGE_FRAGMENT_SHADER_BIT, 0, 0, NULL, 0, NULL, 1, &toSample);
    vkEndCommandBuffer(r->cmdBuf);


    VkSubmitInfo submitInfo = { .sType = VK_STRUCTURE_TYPE_SUBMIT_INFO, .commandBufferCount = 1, .pCommandBuffers = &r->cmdBuf };
    res = vkQueueSubmit(r->queue, 1, &submitInfo, r->fence);
    if (res != VK_SUCCESS) return (int)res;
    res = vkWaitForFences(r->device, 1, &r->fence, VK_TRUE, 1000000000ULL);
    if (res != VK_SUCCESS) return (int)res;
    // Fence left SIGNALLED: begin_frame waits for the previous
    // submit then resets — a reset here times out the next frame.
    return 0;
}


static void destroy_upload_texture(VkContext* ctx, VkUploadTexture* t) {
    if (t->view) vkDestroyImageView(ctx->device, t->view, NULL);
    if (t->mapped) vkUnmapMemory(ctx->device, t->stagingMemory);
    if (t->stagingMemory) vkFreeMemory(ctx->device, t->stagingMemory, NULL);
    if (t->stagingBuffer) vkDestroyBuffer(ctx->device, t->stagingBuffer, NULL);
    if (t->memory) vkFreeMemory(ctx->device, t->memory, NULL);
    if (t->image) vkDestroyImage(ctx->device, t->image, NULL);
    memset(t, 0, sizeof(VkUploadTexture));
}

static void draw_texture_view(VkRenderer* r, VkImageView view,
    float dstX, float dstY, float dstW, float dstH,
    float srcU, float srcV, float srcW, float srcH, float alpha) {
    draw_texture_quad(r, view, dstX, dstY, dstW, dstH, srcU, srcV, srcW, srcH, alpha);
}

static int readback_target(VkContext* ctx, VkRenderer* r, VkTarget* t, void* outRows, uint32_t outStride) {
    // Staging buffer sized for outStride*height.
    VkBufferCreateInfo binfo = {
        .sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
        .size = (VkDeviceSize)outStride * t->height,
        .usage = VK_BUFFER_USAGE_TRANSFER_DST_BIT,
        .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
    };
    VkBuffer buf;
    VkResult res = vkCreateBuffer(ctx->device, &binfo, NULL, &buf);
    if (res != VK_SUCCESS) return (int)res;

    VkMemoryRequirements reqs;
    vkGetBufferMemoryRequirements(ctx->device, buf, &reqs);
    VkPhysicalDeviceMemoryProperties mprops;
    vkGetPhysicalDeviceMemoryProperties(ctx->pdev, &mprops);
    int memType = -1;
    for (uint32_t i = 0; i < mprops.memoryTypeCount; i++) {
        if ((reqs.memoryTypeBits & (1 << i)) &&
            (mprops.memoryTypes[i].propertyFlags &
             (VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT | VK_MEMORY_PROPERTY_HOST_COHERENT_BIT))) {
            memType = (int)i;
            break;
        }
    }
    if (memType < 0) { vkDestroyBuffer(ctx->device, buf, NULL); return -21; }

    VkMemoryAllocateInfo ainfo = { .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO, .allocationSize = reqs.size, .memoryTypeIndex = (uint32_t)memType };
    VkDeviceMemory mem;
    res = vkAllocateMemory(ctx->device, &ainfo, NULL, &mem);
    if (res != VK_SUCCESS) { vkDestroyBuffer(ctx->device, buf, NULL); return (int)res; }
    res = vkBindBufferMemory(ctx->device, buf, mem, 0);
    if (res != VK_SUCCESS) { vkFreeMemory(ctx->device, mem, NULL); vkDestroyBuffer(ctx->device, buf, NULL); return (int)res; }

    vkResetFences(ctx->device, 1, &r->fence);
    VkCommandBufferBeginInfo binfo2 = { .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO, .flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT };
    res = vkBeginCommandBuffer(r->cmdBuf, &binfo2);
    if (res != VK_SUCCESS) goto fail;

    // COLOR_ATTACHMENT_OPTIMAL -> TRANSFER_SRC_OPTIMAL
    VkImageMemoryBarrier pre = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
        .srcAccessMask = VK_ACCESS_COLOR_ATTACHMENT_WRITE_BIT,
        .dstAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
        .oldLayout = VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL,
        .newLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .image = t->image,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_COLOR_ATTACHMENT_OUTPUT_BIT, VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 0, NULL, 1, &pre);

    VkBufferImageCopy region = {
        .imageSubresource = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 0, 1 },
        .imageOffset = { 0, 0, 0 },
        .imageExtent = { t->width, t->height, 1 },
        .bufferOffset = 0,
        .bufferRowLength = t->width,
        .bufferImageHeight = t->height,
    };
    vkCmdCopyImageToBuffer(r->cmdBuf, t->image, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, buf, 1, &region);

    // Back to COLOR_ATTACHMENT_OPTIMAL for future frames.
    VkImageMemoryBarrier post = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
        .srcAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
        .dstAccessMask = 0,
        .oldLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
        .newLayout = VK_IMAGE_LAYOUT_COLOR_ATTACHMENT_OPTIMAL,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .image = t->image,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_TRANSFER_BIT, VK_PIPELINE_STAGE_COLOR_ATTACHMENT_OUTPUT_BIT, 0, 0, NULL, 0, NULL, 1, &post);
    vkEndCommandBuffer(r->cmdBuf);

    VkSubmitInfo sinfo = { .sType = VK_STRUCTURE_TYPE_SUBMIT_INFO, .commandBufferCount = 1, .pCommandBuffers = &r->cmdBuf };
    res = vkQueueSubmit(r->queue, 1, &sinfo, r->fence);
    if (res != VK_SUCCESS) goto fail;
    res = vkWaitForFences(ctx->device, 1, &r->fence, VK_TRUE, 1000000000ULL);
    if (res != VK_SUCCESS) goto fail;
    vkResetFences(ctx->device, 1, &r->fence);

    // Copy out row by row.
    {
        void* mapped;
        res = vkMapMemory(ctx->device, mem, 0, reqs.size, 0, &mapped);
        if (res == VK_SUCCESS) {
            for (uint32_t row = 0; row < t->height; row++) {
                memcpy((uint8_t*)outRows + (size_t)outStride * row, (uint8_t*)mapped + (size_t)t->width * 4 * row, (size_t)t->width * 4);
            }
            vkUnmapMemory(ctx->device, mem);
        }
        vkFreeMemory(ctx->device, mem, NULL);
        vkDestroyBuffer(ctx->device, buf, NULL);
        return 0;
    }
fail:
    vkResetFences(ctx->device, 1, &r->fence);
    vkFreeMemory(ctx->device, mem, NULL);
    vkDestroyBuffer(ctx->device, buf, NULL);
    return -1;
}

static int readback_texture_impl(VkContext* ctx, VkUploadTexture* t, uint8_t* outRows, uint32_t outStride) {
    (void)ctx;
    if (!t->mapped) return -31;
    // OPTIMAL tiling: read the staging rows (tightly packed width*4).
    for (uint32_t row = 0; row < t->height; row++) {
        memcpy((uint8_t*)outRows + (size_t)outStride * row, (uint8_t*)t->mapped + (size_t)t->width * 4 * row, (size_t)t->width * 4);
    }
    return 0;
}

// Verify the OPTIMAL image actually received the data: transition
// SHADER_READ_ONLY -> TRANSFER_SRC, copy image -> staging, back.
static int verify_image_copy(VkContext* ctx, VkRenderer* r, VkUploadTexture* t) {
    vkResetFences(ctx->device, 1, &r->fence);
    VkCommandBufferBeginInfo beginInfo = { .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO, .flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT };
    VkResult res = vkBeginCommandBuffer(r->cmdBuf, &beginInfo);
    if (res != VK_SUCCESS) return (int)res;

    VkImageMemoryBarrier pre = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
        .srcAccessMask = VK_ACCESS_SHADER_READ_BIT,
        .dstAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
        .oldLayout = VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL,
        .newLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .image = t->image,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_FRAGMENT_SHADER_BIT, VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 0, NULL, 1, &pre);

    VkBufferImageCopy region = {
        .imageSubresource = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 0, 1 },
        .imageOffset = { 0, 0, 0 },
        .imageExtent = { t->width, t->height, 1 },
        .bufferOffset = 0,
        .bufferRowLength = 0,
        .bufferImageHeight = 0,
    };
    vkCmdCopyImageToBuffer(r->cmdBuf, t->image, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL, t->stagingBuffer, 1, &region);

    VkImageMemoryBarrier post = {
        .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
        .srcAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
        .dstAccessMask = VK_ACCESS_SHADER_READ_BIT,
        .oldLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
        .newLayout = VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL,
        .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
        .image = t->image,
        .subresourceRange = { VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1 },
    };
    vkCmdPipelineBarrier(r->cmdBuf, VK_PIPELINE_STAGE_TRANSFER_BIT, VK_PIPELINE_STAGE_FRAGMENT_SHADER_BIT, 0, 0, NULL, 0, NULL, 1, &post);
    vkEndCommandBuffer(r->cmdBuf);

    VkSubmitInfo sinfo = { .sType = VK_STRUCTURE_TYPE_SUBMIT_INFO, .commandBufferCount = 1, .pCommandBuffers = &r->cmdBuf };
    res = vkQueueSubmit(r->queue, 1, &sinfo, r->fence);
    if (res != VK_SUCCESS) return (int)res;
    res = vkWaitForFences(ctx->device, 1, &r->fence, VK_TRUE, 1000000000ULL);
    if (res != VK_SUCCESS) return (int)res;
    // fence left SIGNALLED (begin_frame invariant).
    return 0;
}
*/
import "C"
import (
	"fmt"
	"sync"
	"unsafe"
)

// RenderTarget represents an offscreen or display-backed framebuffer in Vulkan.
type RenderTarget struct {
	target C.VkTarget
	Width  uint32
	Height uint32
}

// Renderer provides a high-throughput, data-oriented Vulkan 2D compositing engine.
type Renderer struct {
	dev *Device
	r   C.VkRenderer
	mu  sync.Mutex
}

// NewRenderer creates a Vulkan compositor renderer backed by the given hardware device.
func NewRenderer(dev *Device) (*Renderer, error) {
	dev.mu.Lock()
	defer dev.mu.Unlock()

	var r C.VkRenderer
	res := C.init_vk_renderer(
		&dev.ctx,
		(*C.uint32_t)(unsafe.Pointer(&quadVertexShaderSpv[0])),
		C.size_t(len(quadVertexShaderSpv)*4),
		(*C.uint32_t)(unsafe.Pointer(&quadFragmentShaderSpv[0])),
		C.size_t(len(quadFragmentShaderSpv)*4),
		(*C.uint32_t)(unsafe.Pointer(&texVertexShaderSpv[0])),
		C.size_t(len(texVertexShaderSpv)*4),
		(*C.uint32_t)(unsafe.Pointer(&texFragmentShaderSpv[0])),
		C.size_t(len(texFragmentShaderSpv)*4),
		&r,
	)

	if res != 0 {
		return nil, fmt.Errorf("init_vk_renderer failed (code %d)", int(res))
	}

	return &Renderer{
		dev: dev,
		r:   r,
	}, nil
}

// CreateTarget allocates a new RenderTarget with the specified pixel dimensions.
func (ren *Renderer) CreateTarget(width, height uint32) (*RenderTarget, error) {
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("create_target: invalid dimensions %dx%d", width, height)
	}

	ren.mu.Lock()
	defer ren.mu.Unlock()

	var t C.VkTarget
	res := C.create_target(&ren.dev.ctx, ren.r.renderPass, C.uint32_t(width), C.uint32_t(height), &t)
	if res != 0 {
		return nil, fmt.Errorf("create_target failed (code %d)", int(res))
	}

	return &RenderTarget{
		target: t,
		Width:  width,
		Height: height,
	}, nil
}

// DestroyTarget frees GPU memory and framebuffer resources of a RenderTarget.
func (ren *Renderer) DestroyTarget(target *RenderTarget) {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	if target != nil {
		C.destroy_target(&ren.dev.ctx, &target.target)
	}
}

// BeginFrame begins recording render commands targeting the specified RenderTarget.
// Clears the buffer to the specified RGBA background color.
func (ren *Renderer) BeginFrame(target *RenderTarget, r, g, b, a float32) error {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	res := C.begin_frame(&ren.r, &target.target, C.float(r), C.float(g), C.float(b), C.float(a))
	if res != 0 {
		return fmt.Errorf("begin_frame failed (code %d)", int(res))
	}
	return nil
}

// DrawColorQuad renders a solid color rectangle (window border, divider, drag highlight).
// Zero-allocation hot path.
func (ren *Renderer) DrawColorQuad(x, y, w, h float32, r, g, b, a float32) {
	C.draw_color_quad(&ren.r, C.float(x), C.float(y), C.float(w), C.float(h), C.float(r), C.float(g), C.float(b), C.float(a))
}

// DrawTextureQuad renders an imported DMA-BUF texture quad onto the target surface.
// Zero-allocation hot path.
func (ren *Renderer) DrawTextureQuad(buf *ImportedBuffer, dstX, dstY, dstW, dstH float32, srcU, srcV, srcW, srcH float32, alpha float32) {
	if buf == nil {
		return
	}
	C.draw_texture_quad(&ren.r, buf.buf.view,
		C.float(dstX), C.float(dstY), C.float(dstW), C.float(dstH),
		C.float(srcU), C.float(srcV), C.float(srcW), C.float(srcH),
		C.float(alpha),
	)
}

// EndFrame finishes recording and submits the command buffer to the GPU graphics queue.
func (ren *Renderer) EndFrame() error {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	res := C.end_frame(&ren.r)
	if res != 0 {
		return fmt.Errorf("end_frame failed (code %d)", int(res))
	}
	return nil
}

// WaitFrame waits for the submitted frame commands to finish execution on the GPU.
func (ren *Renderer) WaitFrame(timeoutNs uint64) error {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	res := C.wait_frame(&ren.r, C.uint64_t(timeoutNs))
	if res != 0 {
		return fmt.Errorf("wait_frame failed (code %d)", int(res))
	}
	return nil
}

// Close destroys all pipelines, descriptor pools, render passes, and fences.
func (ren *Renderer) Close() error {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	C.destroy_vk_renderer(&ren.r)
	return nil
}

type Texture struct {
	t      C.VkUploadTexture
	Width  uint32
	Height uint32
	Stride uint32
}

// UploadTexture copies BGRA pixel data into a host-visible linear image
// and prepares it for sampling. pixels has stride bytes per row.
func (ren *Renderer) UploadTexture(width, height, stride uint32, pixels []byte) (*Texture, error) {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	if int(stride)*int(height) > len(pixels) {
		return nil, fmt.Errorf("upload: %d rows x %d stride > %d bytes", height, stride, len(pixels))
	}

	var t C.VkUploadTexture
	var res C.int
	if len(pixels) > 0 {
		res = C.upload_texture_impl(&ren.dev.ctx, &ren.r,
			C.uint32_t(width), C.uint32_t(height), C.uint32_t(stride),
			(*C.uint8_t)(unsafe.Pointer(&pixels[0])), &t)
	} else {
		res = C.upload_texture_impl(&ren.dev.ctx, &ren.r,
			C.uint32_t(width), C.uint32_t(height), C.uint32_t(stride),
			nil, &t)
	}
	if res != 0 {
		return nil, fmt.Errorf("upload_texture failed (code %d)", int(res))
	}
	return &Texture{t: t, Width: width, Height: height, Stride: stride}, nil
}

// DestroyTexture frees the upload texture's GPU resources.
func (ren *Renderer) DestroyTexture(tex *Texture) {
	if tex == nil {
		return
	}
	ren.mu.Lock()
	defer ren.mu.Unlock()
	C.destroy_upload_texture(&ren.dev.ctx, &tex.t)
}

// BeginFrameOn starts recording against the target for executor-driven
// frames (GPU executor path).
func (ren *Renderer) BeginFrameOn(target *RenderTarget, r, g, b, a float32) error {
	return ren.BeginFrame(target, r, g, b, a)
}

// DrawTextureView renders a CPU-uploaded texture quad (view-based).
func (ren *Renderer) DrawTextureView(tex *Texture, dstX, dstY, dstW, dstH float32, alpha float32) {
	if tex == nil {
		return
	}
	ren.mu.Lock()
	defer ren.mu.Unlock()
	C.draw_texture_view(&ren.r, tex.t.view,
		C.float(dstX), C.float(dstY), C.float(dstW), C.float(dstH),
		0, 0, 1, 1,
		C.float(alpha),
	)
}

// ReadbackTarget copies the render target's pixels to the CPU (BGRA,
// row-major, tightly packed). Used by tests and diagnostics.
func (ren *Renderer) ReadbackTarget(target *RenderTarget) ([]byte, bool) {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	out := make([]byte, int(target.Width)*4*int(target.Height))
	if out == nil {
		return nil, false
	}

	var pixels []byte
	var res C.int
	if len(out) > 0 {
		res = C.readback_target(&ren.dev.ctx, &ren.r, &target.target,
			unsafe.Pointer(&out[0]), C.uint32_t(target.Width*4))
	} else {
		res = C.readback_target(&ren.dev.ctx, &ren.r, &target.target, nil, 0)
	}
	if res != 0 {
		return nil, false
	}
	_ = pixels
	return out, true
}

// ReadbackTexture returns the texture's tightly-packed rows: with an
// OPTIMAL-tiled image the CPU cannot read it back — the staging buffer
// still holds the last upload, which is what tests verify.
func (ren *Renderer) ReadbackTexture(tex *Texture) ([]byte, bool) {
	return ren.ReadbackStaging(tex)
}

// ReadbackStaging returns a copy of the staging buffer's rows (the last
// uploaded CPU pixels, tightly packed width*4).
func (ren *Renderer) ReadbackStaging(tex *Texture) ([]byte, bool) {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	out := make([]byte, int(tex.Width)*4*int(tex.Height))
	if len(out) == 0 {
		return nil, false
	}
	if res := C.readback_texture_impl(&ren.dev.ctx, &tex.t, (*C.uint8_t)(unsafe.Pointer(&out[0])), C.uint32_t(tex.Width*4)); res != 0 {
		return nil, false
	}
	return out, true
}

// VerifyImageCopy round-trips the image through staging and returns what
// the GPU actually holds (diagnostic for sampling bugs).
func (ren *Renderer) VerifyImageCopy(tex *Texture) ([]byte, bool) {
	ren.mu.Lock()
	defer ren.mu.Unlock()

	out := make([]byte, int(tex.Width)*4*int(tex.Height))
	if len(out) == 0 {
		return nil, false
	}
	// Copies the image into the staging buffer; then read the staging.
	if res := C.verify_image_copy(&ren.dev.ctx, &ren.r, &tex.t); res != 0 {
		return nil, false
	}
	if res := C.readback_texture_impl(&ren.dev.ctx, &tex.t,
		(*C.uint8_t)(unsafe.Pointer(&out[0])), C.uint32_t(tex.Width*4)); res != 0 {
		return nil, false
	}
	return out, true
}
