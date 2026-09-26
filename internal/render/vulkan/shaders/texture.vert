#version 450
layout(push_constant) uniform PushConstants {
    vec4 dstRect;
    vec4 srcCrop;
    vec2 screenSize;
    float alpha;
} pc;

layout(location = 0) out vec2 vUV;
layout(location = 1) out float vAlpha;

void main() {
    vec2 pixelPos = pc.dstRect.xy + vec2(float(gl_VertexIndex & 1), float((gl_VertexIndex >> 1) & 1)) * pc.dstRect.zw;
    vec2 ndc = vec2(pixelPos.x / pc.screenSize.x * 2.0 - 1.0,
                    pixelPos.y / pc.screenSize.y * 2.0 - 1.0);
    gl_Position = vec4(ndc, 0.0, 1.0);
    vUV = pc.srcCrop.xy + vec2(float(gl_VertexIndex & 1), float((gl_VertexIndex >> 1) & 1)) * pc.srcCrop.zw;
    vAlpha = pc.alpha;
}
