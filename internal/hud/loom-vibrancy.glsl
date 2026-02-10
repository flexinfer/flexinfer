// loom-vibrancy.glsl - Custom Ghostty shader for the Loom HUD deep teal theme.
//
// Effects:
//   1. Subtle scanline overlay (2px spacing, ~4% opacity)
//   2. Soft vignette darkening at screen edges
//   3. Chromatic tint toward the #00171A base hue
//   4. CRT phosphor glow (bloom) on bright text
//
// Install: loom hud --install-shader
// Config:  custom-shader = ~/.config/loom/loom-vibrancy.glsl

void mainImage(out vec4 fragColor, in vec2 fragCoord) {
    vec2 uv = fragCoord / iResolution.xy;

    // Sample the terminal framebuffer.
    vec4 color = texture(iChannel0, uv);

    // --- 1. Scanline overlay ---
    // 2px spacing, very subtle darkening on alternate pixel rows.
    float scanline = mod(fragCoord.y, 2.0) < 1.0 ? 1.0 : 0.96;
    color.rgb *= scanline;

    // --- 2. Vignette ---
    // Smooth darkening toward edges, keeps center fully lit.
    vec2 vignetteUV = uv * (1.0 - uv);
    float vignette = vignetteUV.x * vignetteUV.y * 15.0;
    vignette = clamp(pow(vignette, 0.25), 0.0, 1.0);
    color.rgb *= mix(0.7, 1.0, vignette);

    // --- 3. Chromatic tint ---
    // Subtle shift toward the deep teal base (#00171A ~ rgb(0, 0.09, 0.10)).
    // Only applies to darker areas to preserve bright text readability.
    float luminance = dot(color.rgb, vec3(0.299, 0.587, 0.114));
    vec3 tealTint = vec3(0.0, 0.09, 0.10);
    float tintStrength = 0.08 * (1.0 - luminance);
    color.rgb = mix(color.rgb, color.rgb + tealTint, tintStrength);

    // --- 4. Phosphor glow on bright text ---
    // Bright pixels (cyan #81F0FE and similar) get a soft bloom halo.
    // We detect brightness and apply a subtle additive glow.
    float brightness = max(color.r, max(color.g, color.b));
    if (brightness > 0.6) {
        // Sample neighboring pixels for a cheap 5-tap box blur.
        vec2 texel = 1.0 / iResolution.xy;
        vec3 bloom = vec3(0.0);
        bloom += texture(iChannel0, uv + vec2(-texel.x, 0.0)).rgb;
        bloom += texture(iChannel0, uv + vec2( texel.x, 0.0)).rgb;
        bloom += texture(iChannel0, uv + vec2(0.0, -texel.y)).rgb;
        bloom += texture(iChannel0, uv + vec2(0.0,  texel.y)).rgb;
        bloom *= 0.25;

        // Blend bloom additively, weighted by how bright the pixel is.
        float glowAmount = smoothstep(0.6, 1.0, brightness) * 0.12;
        color.rgb += bloom * glowAmount;
    }

    // Clamp to valid range.
    fragColor = vec4(clamp(color.rgb, 0.0, 1.0), color.a);
}
