#!/usr/bin/env python3
"""Patch turboquant-vllm for Gemma4 experimentation on ROCm.

This applies three source patches to upstream ``turboquant-vllm``:

1. Fall back to GPU QR when CPU LAPACK is unavailable.
2. Allow Gemma4 full-attention layers with 512-dim heads.
3. Pad TurboQuant KV rows for Gemma4's mixed 256/512 head geometry so
   vLLM can unify page sizes across layers.
"""

from __future__ import annotations

import pathlib
import sys


QUANTIZER_OLD = """def _generate_rotation_matrix(dim: int, seed: int = 42) -> torch.Tensor:
    \"\"\"Generate a Haar-distributed random orthogonal matrix.

    Uses QR decomposition of a random Gaussian matrix. The resulting
    matrix is uniformly distributed over the orthogonal group O(d).

    Generator and tensor are explicitly placed on CPU in float32 to
    avoid conflicts when ``torch.set_default_device('cuda')`` or a
    non-float32 default dtype is active (e.g., inside vLLM model init).

    Args:
        dim: Matrix dimension (d x d).
        seed: Random seed for reproducibility.

    Returns:
        Orthogonal matrix of shape (dim, dim) in float32 on CPU.
    \"\"\"
    gen = torch.Generator(device=\"cpu\").manual_seed(seed)
    gaussian = torch.randn(dim, dim, generator=gen, device=\"cpu\", dtype=torch.float32)
    q, r = torch.linalg.qr(gaussian)
    # Ensure uniform distribution by correcting signs
    diag_sign = torch.sign(torch.diag(r))
    diag_sign[diag_sign == 0] = 1.0
    return q * diag_sign.unsqueeze(0)
"""

QUANTIZER_NEW = """def _generate_rotation_matrix(dim: int, seed: int = 42) -> torch.Tensor:
    \"\"\"Generate a Haar-distributed random orthogonal matrix.

    Prefer CPU QR for portability. If the current PyTorch build lacks CPU
    LAPACK support, fall back to a GPU QR path when CUDA/ROCm is available.

    Args:
        dim: Matrix dimension (d x d).
        seed: Random seed for reproducibility.

    Returns:
        Orthogonal matrix of shape ``(dim, dim)`` in float32. The tensor
        stays on the device used to construct it.
    \"\"\"

    def _qr_on(device: str) -> torch.Tensor:
        gen = torch.Generator(device=device).manual_seed(seed)
        gaussian = torch.randn(
            dim, dim, generator=gen, device=device, dtype=torch.float32
        )
        q, r = torch.linalg.qr(gaussian)
        diag_sign = torch.sign(torch.diag(r))
        diag_sign[diag_sign == 0] = 1.0
        return q * diag_sign.unsqueeze(0)

    try:
        return _qr_on(\"cpu\")
    except RuntimeError as exc:
        msg = str(exc)
        lapack_missing = \"requires compiling PyTorch with LAPACK\" in msg
        if not lapack_missing:
            raise
        if torch.cuda.is_available():
            return _qr_on(\"cuda\")
        raise
"""

BACKEND_CLASS_OLD = """class TQ4AttentionBackend(FlashAttentionBackend):
    \"\"\"TQ4 compressed KV cache attention backend.

    Phase 3c: packed uint8 cache layout with real VRAM savings.
    The cache stores nibble-packed TQ4 indices + fp32 norms as raw bytes.
    ``get_kv_cache_shape()`` returns a 3D ``(NB, BS, bytes_per_token)``
    layout matching the packed format.
    \"\"\"

    forward_includes_kv_cache_update = True
"""

BACKEND_CLASS_NEW = """class TQ4AttentionBackend(FlashAttentionBackend):
    \"\"\"TQ4 compressed KV cache attention backend.

    Phase 3c: packed uint8 cache layout with real VRAM savings.
    The cache stores nibble-packed TQ4 indices + fp32 norms as raw bytes.
    ``get_kv_cache_shape()`` returns a 3D ``(NB, BS, bytes_per_token)``
    layout matching the packed format.
    \"\"\"

    forward_includes_kv_cache_update = True

    @classmethod
    def supports_head_size(cls, head_size: int) -> bool:
        \"\"\"Allow Gemma4 full-attention layers with 512-dim heads.\"\"\"
        return head_size % 8 == 0 and head_size <= 512
"""

CACHE_HELPER_OLD = """def _tq4_bytes_per_token_kv(
    head_dim: int, k_bits: int = TQ4_BITS, v_bits: int = TQ4_BITS
) -> int:
    \"\"\"Total packed bytes per token per KV head (K + V combined).

    Args:
        head_dim: Dimension of each attention head.
        k_bits: Key quantization bits.
        v_bits: Value quantization bits.

    Returns:
        Combined byte count for K and V.
    \"\"\"
    return _tq4_bytes_per_token(head_dim, k_bits) + _tq4_bytes_per_token(
        head_dim, v_bits
    )


# ---------------------------------------------------------------------------
# Fused paged decode feature gate (Story 6.3)
# ---------------------------------------------------------------------------
"""

CACHE_HELPER_NEW = """def _tq4_bytes_per_token_kv(
    head_dim: int, k_bits: int = TQ4_BITS, v_bits: int = TQ4_BITS
) -> int:
    \"\"\"Total packed bytes per token per KV head (K + V combined).

    Args:
        head_dim: Dimension of each attention head.
        k_bits: Key quantization bits.
        v_bits: Value quantization bits.

    Returns:
        Combined byte count for K and V.
    \"\"\"
    return _tq4_bytes_per_token(head_dim, k_bits) + _tq4_bytes_per_token(
        head_dim, v_bits
    )


def _tq4_payload_row_bytes(head_dim: int, num_kv_heads: int) -> int:
    \"\"\"Return the true packed per-token row width for the cache.\"\"\"
    return num_kv_heads * _tq4_bytes_per_token_kv(head_dim)


def _tq4_cache_row_bytes(head_dim: int, num_kv_heads: int) -> int:
    \"\"\"Return the allocated per-token row width for the packed cache.

    Gemma4 mixes 256-dim sliding-attention heads with 512-dim full-attention
    heads. vLLM requires hybrid attention layers to share a compatible page
    size. Pad smaller TQ4 rows up to the Gemma4 maximum so page-size unification
    succeeds, leaving unused tail bytes inert.
    \"\"\"
    total_bytes = _tq4_payload_row_bytes(head_dim, num_kv_heads)

    try:
        from vllm.config import get_current_vllm_config_or_none

        vllm_config = get_current_vllm_config_or_none()
        model_config = None if vllm_config is None else vllm_config.model_config
        hf_config = None if model_config is None else getattr(model_config, \"hf_config\", None)
        text_config = getattr(hf_config, \"text_config\", hf_config)
        if getattr(text_config, \"model_type\", None) not in {\"gemma4\", \"gemma4_text\"}:
            return total_bytes

        sliding_head_dim = getattr(text_config, \"head_dim\", head_dim)
        sliding_kv_heads = getattr(text_config, \"num_key_value_heads\", num_kv_heads)
        global_head_dim = getattr(text_config, \"global_head_dim\", sliding_head_dim)
        use_global_kv_heads = getattr(text_config, \"attention_k_eq_v\", False)
        global_kv_heads = (
            getattr(text_config, \"num_global_key_value_heads\", None) or sliding_kv_heads
        ) if use_global_kv_heads else sliding_kv_heads

        return max(
            total_bytes,
            _tq4_payload_row_bytes(sliding_head_dim, sliding_kv_heads),
            _tq4_payload_row_bytes(global_head_dim, global_kv_heads),
        )
    except Exception:
        return total_bytes


def _tq4_page_size_padded(spec, vllm_config) -> int | None:
    \"\"\"Return an allocator-friendly padded page size when the model needs it.\"\"\"
    model_config = None if vllm_config is None else getattr(vllm_config, \"model_config\", None)
    hf_config = None if model_config is None else getattr(model_config, \"hf_config\", None)
    text_config = getattr(hf_config, \"text_config\", hf_config)
    if getattr(text_config, \"model_type\", None) not in {\"gemma4\", \"gemma4_text\"}:
        return getattr(spec, \"page_size_padded\", None)

    sliding_head_dim = getattr(text_config, \"head_dim\", spec.head_size)
    sliding_kv_heads = getattr(text_config, \"num_key_value_heads\", spec.num_kv_heads)
    global_head_dim = getattr(text_config, \"global_head_dim\", sliding_head_dim)
    use_global_kv_heads = getattr(text_config, \"attention_k_eq_v\", False)
    global_kv_heads = (
        getattr(text_config, \"num_global_key_value_heads\", None) or sliding_kv_heads
    ) if use_global_kv_heads else sliding_kv_heads

    padded = spec.block_size * max(
        _tq4_payload_row_bytes(spec.head_size, spec.num_kv_heads),
        _tq4_payload_row_bytes(sliding_head_dim, sliding_kv_heads),
        _tq4_payload_row_bytes(global_head_dim, global_kv_heads),
    )
    existing = getattr(spec, \"page_size_padded\", None)
    return padded if existing is None else max(existing, padded)


# ---------------------------------------------------------------------------
# Fused paged decode feature gate (Story 6.3)
# ---------------------------------------------------------------------------
"""

SPEC_OLD = """    @property
    def real_page_size_bytes(self) -> int:  # noqa: D102
        # Triton kernels always nibble-pack, so page size is independent of
        # bit-width (different codebook sizes don't change storage layout).
        return (
            self.block_size
            * self.num_kv_heads
            * _tq4_bytes_per_token_kv(self.head_size)
        )
"""

SPEC_NEW = """    @property
    def real_page_size_bytes(self) -> int:  # noqa: D102
        # Keep the real payload size separate from allocator padding.
        return self.block_size * _tq4_payload_row_bytes(self.head_size, self.num_kv_heads)
"""

SHAPE_OLD = """        total_bytes = num_kv_heads * _tq4_bytes_per_token_kv(head_size)
        return (num_blocks, block_size, total_bytes)
"""

SHAPE_NEW = """        total_bytes = _tq4_cache_row_bytes(head_size, num_kv_heads)
        return (num_blocks, block_size, total_bytes)
"""

TOTAL_BYTES_OLD = """        self._k_idx_size = k_idx_size
        self._v_idx_size = v_idx_size
        self._k_idx_end = num_kv_heads * k_idx_size
        self._k_norm_end = self._k_idx_end + num_kv_heads * TQ4_NORM_BYTES
        self._v_idx_end = self._k_norm_end + num_kv_heads * v_idx_size
        self._total_bytes = self._v_idx_end + num_kv_heads * TQ4_NORM_BYTES
"""

TOTAL_BYTES_NEW = """        self._k_idx_size = k_idx_size
        self._v_idx_size = v_idx_size
        self._k_idx_end = num_kv_heads * k_idx_size
        self._k_norm_end = self._k_idx_end + num_kv_heads * TQ4_NORM_BYTES
        self._v_idx_end = self._k_norm_end + num_kv_heads * v_idx_size
        self._payload_bytes = self._v_idx_end + num_kv_heads * TQ4_NORM_BYTES
        self._total_bytes = _tq4_cache_row_bytes(head_size, num_kv_heads)
"""

ROW_OLD = """        row = (
            row_out[:N]
            if row_out is not None
            else torch.empty(N, self._total_bytes, dtype=torch.uint8, device=key.device)
        )
"""

ROW_NEW = """        if row_out is not None:
            row = row_out[:N]
            if self._payload_bytes != self._total_bytes:
                row.zero_()
        elif self._payload_bytes != self._total_bytes:
            row = torch.zeros(N, self._total_bytes, dtype=torch.uint8, device=key.device)
        else:
            row = torch.empty(N, self._total_bytes, dtype=torch.uint8, device=key.device)
"""

VNORM_STORE_OLD = """        row[:, self._v_idx_end :] = v_norms.reshape(N, H).contiguous().view(torch.uint8)
"""

VNORM_STORE_NEW = """        row[:, self._v_idx_end : self._payload_bytes] = (
            v_norms.reshape(N, H).contiguous().view(torch.uint8)
        )
"""

VNORM_LOAD_OLD = """        v_norms = (
            flat[:, self._v_idx_end :]
            .contiguous()
            .view(torch.float32)
            .reshape(-1, H, 1)
        )
"""

VNORM_LOAD_NEW = """        v_norms = (
        flat[:, self._v_idx_end : self._payload_bytes]
            .contiguous()
            .view(torch.float32)
            .reshape(-1, H, 1)
        )
"""

PAGED_VNORM_LOAD_OLD = """        v_norms = (
            flat[:, self._v_idx_end :]
            .contiguous()
            .view(torch.float32)
            .reshape(-1, H, 1)
        )
"""

PAGED_VNORM_LOAD_NEW = """        v_norms = (
            flat[:, self._v_idx_end : self._payload_bytes]
            .contiguous()
            .view(torch.float32)
            .reshape(-1, H, 1)
        )
"""

CODEC_ENV_OLD = """def _parse_int8_prefill_env() -> bool:
    \"\"\"Parse ``TQ4_USE_INT8_PREFILL`` environment variable.

    Returns:
        ``True`` when the env var is set to a truthy value
        (``\"1\"``, ``\"true\"``, ``\"yes\"``; case-insensitive).
        ``False`` for everything else including absent.
    \"\"\"
    return os.environ.get(\"TQ4_USE_INT8_PREFILL\", \"\").lower() in (
        \"1\",
        \"true\",
        \"yes\",
    )


# ---------------------------------------------------------------------------
# KV cache spec (3c.1)
# ---------------------------------------------------------------------------
"""

CODEC_ENV_NEW = """def _parse_int8_prefill_env() -> bool:
    \"\"\"Parse ``TQ4_USE_INT8_PREFILL`` environment variable.

    Returns:
        ``True`` when the env var is set to a truthy value
        (``\"1\"``, ``\"true\"``, ``\"yes\"``; case-insensitive).
        ``False`` for everything else including absent.
    \"\"\"
    return os.environ.get(\"TQ4_USE_INT8_PREFILL\", \"\").lower() in (
        \"1\",
        \"true\",
        \"yes\",
    )


def _parse_pytorch_codec_env() -> bool:
    \"\"\"Parse ``TQ4_USE_PYTORCH_CODEC`` environment variable.\"\"\"
    return os.environ.get(\"TQ4_USE_PYTORCH_CODEC\", \"\").lower() in (
        \"1\",
        \"true\",
        \"yes\",
    )


# ---------------------------------------------------------------------------
# KV cache spec (3c.1)
# ---------------------------------------------------------------------------
"""

CODEC_METHODS_OLD = """        logger.info(
            "INT8 prefill path: %s",
            "enabled" if self._int8_prefill_available else "disabled",
        )

    def _init_cg_buffers(
        self, kv_cache: torch.Tensor, compute_dtype: torch.dtype
    ) -> None:
"""

CODEC_METHODS_NEW = """        logger.info(
            "INT8 prefill path: %s",
            "enabled" if self._int8_prefill_available else "disabled",
        )

    def _uses_shared_kv_cache(self) -> bool:
        return getattr(self, "kv_sharing_target_layer_name", None) is not None

    def _should_store_kv(
        self,
        key: torch.Tensor | None,
        value: torch.Tensor | None,
    ) -> bool:
        if key is None or value is None:
            return False
        if not self._uses_shared_kv_cache():
            return True
        if not getattr(self, "_kv_sharing_logged", False):
            logger.info(
                "TQ4 shared-KV layer: skipping cache write "
                "(target=%s, head_size=%d, num_kv_heads=%d)",
                self.kv_sharing_target_layer_name,
                self.head_size,
                self.num_kv_heads,
            )
            self._kv_sharing_logged = True
        return False

    def _use_pytorch_codec(self) -> bool:
        from vllm.platforms import current_platform

        return _parse_pytorch_codec_env() and current_platform.is_rocm()

    def _tq4_compress(
        self,
        x: torch.Tensor,
        boundaries: torch.Tensor,
        *,
        out: tuple[torch.Tensor, torch.Tensor] | None = None,
    ) -> tuple[torch.Tensor, torch.Tensor]:
        if not self._use_pytorch_codec():
            return tq4_compress(
                x,
                self._tq4_rot_T_even,
                self._tq4_rot_T_odd,
                boundaries,
                out=out,
            )

        N, H, D = x.shape
        flat = x.reshape(N * H, D).float()
        norms = torch.norm(flat, dim=-1, keepdim=True)
        normalized = flat / (norms + 1e-10)
        rotated = normalized @ self._tq4_rotation.T
        indices = torch.bucketize(rotated, boundaries).clamp(0, 15).to(torch.uint8)
        packed = (indices[:, 0::2] << 4) | indices[:, 1::2]

        if out is not None:
            packed_out, norms_out = out
            packed_out.reshape(N * H, D // 2).copy_(packed)
            norms_out.reshape(N * H, 1).copy_(norms)
            return packed_out, norms_out

        return packed.reshape(N, H, D // 2), norms.reshape(N, H, 1)

    def _tq4_decompress(
        self,
        packed: torch.Tensor,
        norms: torch.Tensor,
        centroids: torch.Tensor,
        compute_dtype: torch.dtype,
        *,
        out: torch.Tensor | None = None,
    ) -> torch.Tensor:
        if not self._use_pytorch_codec():
            return tq4_decompress(
                packed,
                norms,
                centroids,
                compute_dtype,
                out=out,
            )

        N, H, half_D = packed.shape
        D = half_D * 2
        high = (packed >> 4).long()
        low = (packed & 0x0F).long()
        indices = torch.stack([high, low], dim=-1).reshape(N * H, D)
        flat_norms = norms.reshape(N * H, 1).float()
        reconstructed = centroids[indices] * flat_norms

        if out is not None:
            out.reshape(N * H, D).copy_(reconstructed.to(compute_dtype))
            return out

        return reconstructed.reshape(N, H, D).to(compute_dtype)

    def _init_cg_buffers(
        self, kv_cache: torch.Tensor, compute_dtype: torch.dtype
    ) -> None:
"""

K_COMPRESS_OLD = """        k_packed, k_norms = tq4_compress(
            key,
            self._tq4_rot_T_even,
            self._tq4_rot_T_odd,
            self._k_boundaries,
            out=compress_out,
        )
"""

K_COMPRESS_NEW = """        k_packed, k_norms = self._tq4_compress(
            key,
            self._k_boundaries,
            out=compress_out,
        )
"""

V_COMPRESS_OLD = """        v_packed, v_norms = tq4_compress(
            value,
            self._tq4_rot_T_even,
            self._tq4_rot_T_odd,
            self._v_boundaries,
            out=compress_out,
        )
"""

V_COMPRESS_NEW = """        v_packed, v_norms = self._tq4_compress(
            value,
            self._v_boundaries,
            out=compress_out,
        )
"""

DECOMPRESS_OLD = """        key_out = tq4_decompress(
            k_packed, k_norms, self._k_centroids, compute_dtype, out=out_k
        )
        value_out = tq4_decompress(
            v_packed, v_norms, self._v_centroids, compute_dtype, out=out_v
        )
"""

DECOMPRESS_NEW = """        key_out = self._tq4_decompress(
            k_packed, k_norms, self._k_centroids, compute_dtype, out=out_k
        )
        value_out = self._tq4_decompress(
            v_packed, v_norms, self._v_centroids, compute_dtype, out=out_v
        )
"""

PAGED_DECOMPRESS_OLD = """        key_out = tq4_decompress(
            k_packed, k_norms, self._k_centroids, compute_dtype, out=k_out_slice
        )
        value_out = tq4_decompress(
            v_packed, v_norms, self._v_centroids, compute_dtype, out=v_out_slice
        )
"""

PAGED_DECOMPRESS_NEW = """        key_out = self._tq4_decompress(
            k_packed, k_norms, self._k_centroids, compute_dtype, out=k_out_slice
        )
        value_out = self._tq4_decompress(
            v_packed, v_norms, self._v_centroids, compute_dtype, out=v_out_slice
        )
"""

STORE_CHECK_PREFILL_OLD = """        if kv_cache is not None and key is not None and value is not None:
            self._compress_and_store(key, value, kv_cache, attn_metadata.slot_mapping)
"""

STORE_CHECK_PREFILL_NEW = """        if kv_cache is not None and self._should_store_kv(key, value):
            self._compress_and_store(key, value, kv_cache, attn_metadata.slot_mapping)
"""

STORE_CHECK_DECODE_OLD = """        \"\"\"Decode path: compress, rotate Q, paged decompress with bounded buffers.\"\"\"
        if key is not None and value is not None:
            self._compress_and_store(
"""

STORE_CHECK_DECODE_NEW = """        \"\"\"Decode path: compress, rotate Q, paged decompress with bounded buffers.\"\"\"
        if self._should_store_kv(key, value):
            self._compress_and_store(
"""

STORE_CHECK_FUSED_OLD = """        # Step 1: Compress and store new tokens (same as decompress-all path)
        if key is not None and value is not None:
            self._compress_and_store(
"""

STORE_CHECK_FUSED_NEW = """        # Step 1: Compress and store new tokens (same as decompress-all path)
        if self._should_store_kv(key, value):
            self._compress_and_store(
"""

STORE_CHECK_INT8_OLD = """        # Step 1: Compress and store (same as decompress-all path)
        if key is not None and value is not None:
            self._compress_and_store(
"""

STORE_CHECK_INT8_NEW = """        # Step 1: Compress and store (same as decompress-all path)
        if self._should_store_kv(key, value):
            self._compress_and_store(
"""

FLASH_ATTN_CALL_OLD = """        flash_attn_varlen_func(
            q=q_rot,
            k=key_cache,
            v=value_cache,
            out=output[:num_actual_tokens],
            cu_seqlens_q=attn_metadata.query_start_loc,
            max_seqlen_q=attn_metadata.max_query_len,
            seqused_k=attn_metadata.seq_lens,
            max_seqlen_k=attn_metadata.max_seq_len,
            softmax_scale=self.scale,
            causal=attn_metadata.causal,
            alibi_slopes=self.alibi_slopes,
            window_size=list(self.sliding_window)
            if self.sliding_window is not None
            else None,
            block_table=fa_block_table,
            softcap=self.logits_soft_cap,
            scheduler_metadata=attn_metadata.scheduler_metadata,
            fa_version=self.vllm_flash_attn_version,
            q_descale=q_descale,
            k_descale=k_descale,
            v_descale=v_descale,
            num_splits=attn_metadata.max_num_splits,
            s_aux=self.sinks,
        )
"""

FLASH_ATTN_CALL_NEW = """        flash_attn_kwargs = dict(
            q=q_rot,
            k=key_cache,
            v=value_cache,
            cu_seqlens_q=attn_metadata.query_start_loc,
            max_seqlen_q=attn_metadata.max_query_len,
            seqused_k=attn_metadata.seq_lens,
            max_seqlen_k=attn_metadata.max_seq_len,
            softmax_scale=self.scale,
            causal=attn_metadata.causal,
            alibi_slopes=self.alibi_slopes,
            window_size=list(self.sliding_window)
            if self.sliding_window is not None
            else None,
            block_table=fa_block_table,
            softcap=self.logits_soft_cap,
            scheduler_metadata=attn_metadata.scheduler_metadata,
            fa_version=self.vllm_flash_attn_version,
            q_descale=q_descale,
            k_descale=k_descale,
            v_descale=v_descale,
            num_splits=attn_metadata.max_num_splits,
            s_aux=self.sinks,
        )

        import inspect
        from vllm.platforms import current_platform

        if (
            fa_block_table is not None
            and getattr(key_cache, "ndim", 0) == 4
        ):
            block_size = key_cache.shape[1]
            seq_lens = attn_metadata.seq_lens.to(dtype=torch.int32)
            dense_key = []
            dense_value = []
            cu_seqlens_k = [0]

            for batch_idx in range(seq_lens.shape[0]):
                seq_len = int(seq_lens[batch_idx].item())
                num_blocks = (seq_len + block_size - 1) // block_size
                logical_blocks = fa_block_table[batch_idx, :num_blocks].to(torch.long)
                seq_key = (
                    key_cache.index_select(0, logical_blocks)
                    .reshape(-1, self.num_kv_heads, self.head_size)[:seq_len]
                    .contiguous()
                )
                seq_value = (
                    value_cache.index_select(0, logical_blocks)
                    .reshape(-1, self.num_kv_heads, self.head_size)[:seq_len]
                    .contiguous()
                )
                dense_key.append(seq_key)
                dense_value.append(seq_value)
                cu_seqlens_k.append(cu_seqlens_k[-1] + seq_len)

            flash_attn_kwargs["k"] = torch.cat(dense_key, dim=0)
            flash_attn_kwargs["v"] = torch.cat(dense_value, dim=0)
            flash_attn_kwargs["cu_seqlens_k"] = torch.tensor(
                cu_seqlens_k,
                device=attn_metadata.query_start_loc.device,
                dtype=attn_metadata.query_start_loc.dtype,
            )
            flash_attn_kwargs["max_seqlen_k"] = int(attn_metadata.seq_lens.max().item())
            flash_attn_kwargs.pop("seqused_k", None)
            flash_attn_kwargs.pop("block_table", None)

        if current_platform.is_rocm():
            cu_seqlens_k = flash_attn_kwargs.get("cu_seqlens_k")
            if cu_seqlens_k is None:
                seq_lens = attn_metadata.seq_lens.to(
                    device=attn_metadata.query_start_loc.device,
                    dtype=attn_metadata.query_start_loc.dtype,
                )
                cu_seqlens_k = torch.cat(
                    (
                        seq_lens.new_zeros(1),
                        torch.cumsum(seq_lens, dim=0),
                    )
                )

            q_all = flash_attn_kwargs["q"]
            k_all = flash_attn_kwargs["k"]
            v_all = flash_attn_kwargs["v"]
            cu_seqlens_q = flash_attn_kwargs["cu_seqlens_q"]
            causal = flash_attn_kwargs["causal"]
            scale = flash_attn_kwargs["softmax_scale"]
            softcap = flash_attn_kwargs["softcap"]
            if flash_attn_kwargs["alibi_slopes"] is not None:
                raise NotImplementedError("ROCm SDPA fallback does not support ALiBi")

            num_q_heads = q_all.shape[1]
            num_kv_heads = k_all.shape[1]
            gqa_ratio = num_q_heads // num_kv_heads

            for batch_idx in range(cu_seqlens_q.numel() - 1):
                q_start = int(cu_seqlens_q[batch_idx].item())
                q_end = int(cu_seqlens_q[batch_idx + 1].item())
                k_start = int(cu_seqlens_k[batch_idx].item())
                k_end = int(cu_seqlens_k[batch_idx + 1].item())
                if q_end <= q_start:
                    continue

                q_seq = q_all[q_start:q_end].transpose(0, 1).unsqueeze(0)
                k_seq = k_all[k_start:k_end].transpose(0, 1).unsqueeze(0)
                v_seq = v_all[k_start:k_end].transpose(0, 1).unsqueeze(0)

                if num_q_heads != num_kv_heads:
                    k_seq = k_seq.repeat_interleave(gqa_ratio, dim=1)
                    v_seq = v_seq.repeat_interleave(gqa_ratio, dim=1)

                q_len = q_end - q_start
                k_len = k_end - k_start
                attn_mask = None
                if causal or (
                    self.sliding_window is not None and self.sliding_window[0] != -1
                ):
                    q_pos = torch.arange(
                        k_len - q_len,
                        k_len,
                        device=q_seq.device,
                        dtype=torch.int32,
                    )
                    k_pos = torch.arange(
                        k_len,
                        device=q_seq.device,
                        dtype=torch.int32,
                    )
                    mask = torch.ones(
                        (q_len, k_len),
                        device=q_seq.device,
                        dtype=torch.bool,
                    )
                    if causal:
                        mask &= k_pos.unsqueeze(0) <= q_pos.unsqueeze(1)
                    if self.sliding_window is not None and self.sliding_window[0] != -1:
                        left, right = self.sliding_window[:2]
                        mask &= k_pos.unsqueeze(0) >= (q_pos.unsqueeze(1) - left)
                        mask &= k_pos.unsqueeze(0) <= (q_pos.unsqueeze(1) + right)
                    attn_mask = torch.zeros(
                        (1, 1, q_len, k_len),
                        device=q_seq.device,
                        dtype=torch.float32,
                    )
                    attn_mask = attn_mask.masked_fill(~mask.view(1, 1, q_len, k_len), float("-inf"))

                q_compute = q_seq.float()
                k_compute = k_seq.float()
                v_compute = v_seq.float()
                scores = torch.matmul(q_compute, k_compute.transpose(-2, -1))
                if scale is not None:
                    scores = scores * float(scale)
                if softcap not in (None, 0):
                    scores = float(softcap) * torch.tanh(scores / float(softcap))
                if attn_mask is not None:
                    scores = scores + attn_mask

                probs = torch.softmax(scores, dim=-1, dtype=torch.float32)
                seq_out = torch.matmul(probs, v_compute).to(q_seq.dtype)
                output[q_start:q_end].copy_(seq_out.squeeze(0).transpose(0, 1))
        else:
            flash_attn_params = inspect.signature(flash_attn_varlen_func).parameters
            if "window_size" in flash_attn_params and flash_attn_kwargs["window_size"] is None:
                flash_attn_kwargs["window_size"] = (-1, -1)

            if "seqused_k" not in flash_attn_params and "cu_seqlens_k" in flash_attn_params:
                seq_lens = attn_metadata.seq_lens.to(
                    device=attn_metadata.query_start_loc.device,
                    dtype=attn_metadata.query_start_loc.dtype,
                )
                flash_attn_kwargs["cu_seqlens_k"] = torch.cat(
                    (
                        seq_lens.new_zeros(1),
                        torch.cumsum(seq_lens, dim=0),
                    )
                )
                flash_attn_kwargs.pop("seqused_k", None)

            flash_attn_kwargs = {
                key: value
                for key, value in flash_attn_kwargs.items()
                if key in flash_attn_params
            }

            if "out" in flash_attn_params:
                flash_attn_varlen_func(
                    out=output[:num_actual_tokens],
                    **flash_attn_kwargs,
                )
            else:
                attn_out = flash_attn_varlen_func(**flash_attn_kwargs)
                output[:num_actual_tokens].copy_(attn_out)
"""

FUSED_DECODE_OLD = """        if self._fused_paged_available and is_decode:
            return self._fused_decode_path(
                query, key, value, kv_cache, attn_metadata, output
            )
"""

FUSED_DECODE_NEW = """        from vllm.platforms import current_platform

        if self._fused_paged_available and is_decode and not current_platform.is_rocm():
            return self._fused_decode_path(
                query, key, value, kv_cache, attn_metadata, output
            )
"""

REGISTER_OLD = """    # Monkey-patch Attention.get_kv_cache_spec to return TQ4 spec
    from vllm.model_executor.layers.attention.attention import Attention

    if _original_get_kv_cache_spec is None:
        _original_get_kv_cache_spec = Attention.get_kv_cache_spec

    def _tq4_get_kv_cache_spec(self, vllm_config):
        spec = _original_get_kv_cache_spec(self, vllm_config)
        if isinstance(spec, FullAttentionSpec) and not isinstance(
            spec, TQ4FullAttentionSpec
        ):
            kwargs = {f.name: getattr(spec, f.name) for f in dc_fields(spec)}
            kwargs[\"dtype\"] = torch.uint8
            return TQ4FullAttentionSpec(**kwargs)
        return spec

    Attention.get_kv_cache_spec = _tq4_get_kv_cache_spec
"""

REGISTER_NEW = """    # Monkey-patch Attention.get_kv_cache_spec to return TQ4 spec
    from vllm.model_executor.layers.attention.attention import Attention
    from vllm.platforms.interface import AttentionBackendEnum
    from vllm.v1.kv_cache_interface import ChunkedLocalAttentionSpec, SlidingWindowSpec

    if _original_get_kv_cache_spec is None:
        _original_get_kv_cache_spec = Attention.get_kv_cache_spec

    def _tq4_get_kv_cache_spec(self, vllm_config):
        spec = _original_get_kv_cache_spec(self, vllm_config)
        attention_backend = None
        if vllm_config is not None:
            attention_config = getattr(vllm_config, "attention_config", None)
            attention_backend = getattr(attention_config, "backend", None)
        if attention_backend != AttentionBackendEnum.CUSTOM:
            return spec
        padded = _tq4_page_size_padded(spec, vllm_config)

        if isinstance(spec, FullAttentionSpec) and not isinstance(
            spec, TQ4FullAttentionSpec
        ):
            kwargs = {f.name: getattr(spec, f.name) for f in dc_fields(spec)}
            kwargs[\"dtype\"] = torch.uint8
            kwargs[\"page_size_padded\"] = padded
            return TQ4FullAttentionSpec(**kwargs)

        if isinstance(spec, (SlidingWindowSpec, ChunkedLocalAttentionSpec)):
            kwargs = {f.name: getattr(spec, f.name) for f in dc_fields(spec)}
            kwargs[\"dtype\"] = torch.uint8
            kwargs[\"page_size_padded\"] = padded
            return type(spec)(**kwargs)

        return spec

    Attention.get_kv_cache_spec = _tq4_get_kv_cache_spec
"""


def apply_exact_patch(
    text: str,
    *,
    old: str,
    new: str,
    label: str,
) -> tuple[str, bool]:
    if new in text:
        return text, False
    if old not in text:
        raise RuntimeError(f"unexpected source contents while patching {label}")
    return text.replace(old, new, 1), True


def patch_quantizer(target: pathlib.Path) -> bool:
    text = target.read_text()
    text, changed = apply_exact_patch(
        text, old=QUANTIZER_OLD, new=QUANTIZER_NEW, label="quantizer gpu-qr"
    )
    if changed:
        target.write_text(text)
    return changed


def patch_backend(target: pathlib.Path) -> bool:
    text = target.read_text()
    changed = False
    for old, new, label in (
        (CACHE_HELPER_OLD, CACHE_HELPER_NEW, "cache helper"),
        (CODEC_ENV_OLD, CODEC_ENV_NEW, "codec env"),
        (BACKEND_CLASS_OLD, BACKEND_CLASS_NEW, "head-size support"),
        (SPEC_OLD, SPEC_NEW, "spec page size"),
        (SHAPE_OLD, SHAPE_NEW, "kv cache shape"),
        (TOTAL_BYTES_OLD, TOTAL_BYTES_NEW, "payload/row bytes"),
        (CODEC_METHODS_OLD, CODEC_METHODS_NEW, "codec methods"),
        (ROW_OLD, ROW_NEW, "row allocation"),
        (K_COMPRESS_OLD, K_COMPRESS_NEW, "k compress wrapper"),
        (V_COMPRESS_OLD, V_COMPRESS_NEW, "v compress wrapper"),
        (VNORM_STORE_OLD, VNORM_STORE_NEW, "vnorm store"),
        (VNORM_LOAD_OLD, VNORM_LOAD_NEW, "vnorm load"),
        (PAGED_VNORM_LOAD_OLD, PAGED_VNORM_LOAD_NEW, "paged vnorm load"),
        (DECOMPRESS_OLD, DECOMPRESS_NEW, "decompress wrapper"),
        (PAGED_DECOMPRESS_OLD, PAGED_DECOMPRESS_NEW, "paged decompress wrapper"),
        (STORE_CHECK_PREFILL_OLD, STORE_CHECK_PREFILL_NEW, "prefill shared-kv guard"),
        (STORE_CHECK_DECODE_OLD, STORE_CHECK_DECODE_NEW, "decode shared-kv guard"),
        (STORE_CHECK_FUSED_OLD, STORE_CHECK_FUSED_NEW, "fused shared-kv guard"),
        (STORE_CHECK_INT8_OLD, STORE_CHECK_INT8_NEW, "int8 shared-kv guard"),
        (FUSED_DECODE_OLD, FUSED_DECODE_NEW, "rocm fused decode guard"),
        (FLASH_ATTN_CALL_OLD, FLASH_ATTN_CALL_NEW, "flash attn out compatibility"),
        (REGISTER_OLD, REGISTER_NEW, "spec registration"),
    ):
        text, changed_i = apply_exact_patch(text, old=old, new=new, label=label)
        changed = changed or changed_i
    if changed:
        target.write_text(text)
    return changed


def main() -> int:
    if len(sys.argv) != 2:
        print(
            "usage: patch_turboquant_quantizer_gpu_qr.py <turboquant-source-root>",
            file=sys.stderr,
        )
        return 2

    root = pathlib.Path(sys.argv[1])
    quantizer = root / "src" / "turboquant_vllm" / "quantizer.py"
    backend = root / "src" / "turboquant_vllm" / "vllm" / "tq4_backend.py"

    quantizer_changed = patch_quantizer(quantizer)
    backend_changed = patch_backend(backend)

    print(f"{'patched' if quantizer_changed else 'already patched'}: {quantizer}")
    print(f"{'patched' if backend_changed else 'already patched'}: {backend}")
    if not quantizer_changed and not backend_changed:
        print("no changes needed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
