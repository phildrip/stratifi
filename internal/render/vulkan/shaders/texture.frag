#version 450
layout(binding = 0) uniform sampler2D uTexture;
layout(location = 0) in vec2 vUV;
layout(location = 1) in float vAlpha;
layout(location = 0) out vec4 outColor;
void main() {
    // Compositor surfaces are opaque: clients (e.g. XRGB-style foot)
    // never write the alpha byte, so force it to 1.0 — otherwise the
    // texture quad blends away to invisible.
    outColor = vec4(texture(uTexture, vUV).rgb, 1.0) * vAlpha;
}
