The gfx1100 vLLM 0.32 runtime image (`rocm-gfx1100-serving-v032`) now includes what the XTX
creative lanes need to run in-pod: bitsandbytes 0.50.2 for Qwen-Image 2.1's NF4 components,
torchsde for Stable Audio, imageio and imageio-ffmpeg for Wan's MP4 export, and ftfy for Wan VACE.
These are installed under the frozen vLLM stack constraints. The build verifier now fails when one
of them is missing, when bitsandbytes lacks the ROCm library for the image's HIP version, or when
imageio-ffmpeg has no executable ffmpeg.
