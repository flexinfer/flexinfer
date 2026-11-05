The Wan 2.1 VAE now encodes and decodes without MIOpen on ROCm when it runs in bf16 or fp16.
For the VAE's 3D convolutions on gfx1100, MIOpen 3.4 only offered its naive kernel, in every
find mode, because PyTorch asks for a zero-workspace convolution. A 33-frame 832x480 clip spent
370 s in VAE decode on the 7900 XTX. PyTorch's im2col + GEMM path decodes the same latent in
5–11 s at an 11.6 GiB peak (2026-11-03). An fp32 VAE (the VACE lane) keeps MIOpen, because its
im2col buffer would need 15.4 GiB.
