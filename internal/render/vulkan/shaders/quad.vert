#version 450
layout(push_constant) uniform PushConstants {
    vec4 rect;      // x, y, w, h in pixels
    vec4 color;
    vec2 screenSize;
} pc;

layout(location = 0) out vec4 vColor;

void main() {
    vec2 pixelPos = pc.rect.xy + vec2(float(gl_VertexIndex & 1), float((gl_VertexIndex >> 1) & 1)) * pc.rect.zw;
    // Vulkan: framebuffer row 0 is NDC y = -1 (Y-down). Scene space has
    // y=0 at the top, so map scene y directly: p.y=0 -> -1, p.y=H -> +1.
    vec2 ndc = vec2(pixelPos.x / pc.screenSize.x * 2.0 - 1.0,
                    pixelPos.y / pc.screenSize.y * 2.0 - 1.0);
    gl_Position = vec4(ndc, 0.0, 1.0);
    vColor = pc.color;
}
