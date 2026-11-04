#!/usr/bin/env python3
"""Unit contract for flexinfer_vllm_common's opt-in ROCm tuning paths.

Covers the two FlexInfer changes that sit behind env flags: the relaxed FLA
autotune pruning (``FLEXINFER_FLA_AUTOTUNE``) and the segmented-attention
autotune at the runtime block-table width
(``FLEXINFER_SEGMENTED_RUNTIME_TABLES``). Neither vLLM nor torch is needed:
the segmented tests drive a fake warmup module whose functions call each other
through module globals, as upstream's do, so the patch is exercised on the same
call path the engine takes.
"""

from __future__ import annotations

import contextlib
import math
import os
import sys
import types
import unittest
from pathlib import Path
from unittest import mock

COMMON_SRC = Path(__file__).parents[1] / "vllm-plugin-common" / "src"
sys.path.insert(0, str(COMMON_SRC))

from flexinfer_vllm_common import rocm, segmented  # noqa: E402


class Config:
    def __init__(self, warps: int, stages: int | None = None) -> None:
        self.num_warps = warps
        if stages is not None:
            self.num_stages = stages


def _fla_module(name: str, *kernels):
    module = types.ModuleType(name)
    for index, kernel in enumerate(kernels):
        setattr(module, f"kernel_{index}", kernel)
    return module


class FlaAutotunePolicyTest(unittest.TestCase):
    PREFIX = "fake_fla_ops."

    def _prune(self, policy, *kernels) -> int:
        module = _fla_module(self.PREFIX + "chunk", *kernels)
        with mock.patch.dict(sys.modules, {module.__name__: module}):
            return rocm.select_rocm_safe_fla_configs((self.PREFIX,), policy)

    def test_single_keeps_the_lowest_stage_and_warp_config(self) -> None:
        kernel = types.SimpleNamespace(
            configs=[Config(4, 3), Config(2, 2), Config(2, 4), Config(1, 2)]
        )
        self.assertEqual(self._prune("single", kernel), 1)
        self.assertEqual(
            [(c.num_warps, c.num_stages) for c in kernel.configs], [(1, 2)]
        )

    def test_relaxed_drops_only_four_plus_stage_configs(self) -> None:
        kernel = types.SimpleNamespace(
            fn=types.SimpleNamespace(
                configs=[
                    Config(2, 2),
                    Config(4, 3),
                    Config(8, 4),
                    Config(2, 5),
                    Config(16, 2),
                ]
            )
        )
        self.assertEqual(self._prune("relaxed", kernel), 1)
        self.assertEqual(
            [(c.num_warps, c.num_stages) for c in kernel.fn.configs],
            [(2, 2), (4, 3), (16, 2)],
        )

    def test_relaxed_keeps_configs_on_triton_default_stages(self) -> None:
        """A Config without num_stages gets Triton's default, 3: it stays."""
        kernel = types.SimpleNamespace(configs=[Config(w) for w in (1, 2, 4, 8)])
        self._prune("relaxed", kernel)
        self.assertEqual([c.num_warps for c in kernel.configs], [1, 2, 4, 8])

    def test_relaxed_falls_back_to_single_when_every_config_stalls(self) -> None:
        kernel = types.SimpleNamespace(configs=[Config(4, 5), Config(2, 4)])
        self._prune("relaxed", kernel)
        self.assertEqual(
            [(c.num_warps, c.num_stages) for c in kernel.configs], [(2, 4)]
        )

    def test_policy_comes_from_the_environment_and_defaults_to_single(self) -> None:
        self.assertEqual(rocm.fla_autotune_policy({}), "single")
        self.assertEqual(
            rocm.fla_autotune_policy({rocm.FLA_AUTOTUNE_ENV: "relaxed"}), "relaxed"
        )
        with self.assertRaises(RuntimeError) as ctx:
            rocm.fla_autotune_policy({rocm.FLA_AUTOTUNE_ENV: "all"})
        self.assertIn(rocm.FLA_AUTOTUNE_ENV, str(ctx.exception))

    def test_invalid_policy_fails_even_off_hip(self) -> None:
        cuda_torch = types.SimpleNamespace(version=types.SimpleNamespace(hip=None))
        with mock.patch.dict(os.environ, {rocm.FLA_AUTOTUNE_ENV: "loose"}):
            with self.assertRaises(RuntimeError):
                rocm.prune_fla_configs_if_needed(cuda_torch)

    def test_wrapper_passes_the_policy_through(self) -> None:
        kernel = types.SimpleNamespace(configs=[Config(2, 2), Config(4, 3)])
        module = _fla_module(self.PREFIX + "chunk", kernel)
        hip_torch = types.SimpleNamespace(version=types.SimpleNamespace(hip="7.2"))
        with (
            mock.patch.dict(sys.modules, {module.__name__: module}),
            mock.patch.dict(
                os.environ,
                {"PYTORCH_ROCM_ARCH": "gfx1100", rocm.FLA_AUTOTUNE_ENV: "relaxed"},
            ),
        ):
            self.assertEqual(
                rocm.prune_fla_configs_if_needed(hip_torch, (self.PREFIX,)), 1
            )
        self.assertEqual(len(kernel.configs), 2)


class FakeTable:
    """A 2-D int32 table: just enough of torch.Tensor for the patch."""

    def __init__(self, rows) -> None:
        self.rows = [list(row) for row in rows]

    @property
    def shape(self):
        return (len(self.rows), len(self.rows[0]) if self.rows else 0)

    def stride(self, dim=None):
        strides = (self.shape[1], 1)
        return strides if dim is None else strides[dim]

    def new_zeros(self, shape):
        rows, columns = shape
        return FakeTable([[0] * columns for _ in range(rows)])

    def __setitem__(self, key, value) -> None:
        rows, columns = key
        for index in range(len(self.rows))[rows]:
            self.rows[index][columns] = value.rows[index]


class FakeTorch:
    float32 = "float32"
    OutOfMemoryError = type("OutOfMemoryError", (RuntimeError,), {})
    accelerator = types.SimpleNamespace(empty_cache=lambda: None)

    @staticmethod
    def inference_mode():
        return contextlib.nullcontext()

    @staticmethod
    def empty(shape, dtype=None, device=None):
        return ("workspace", tuple(shape))

    @staticmethod
    def empty_like(tensor):
        return ("output", tensor)

    @staticmethod
    def linspace(*args, **kwargs):
        return "sinks"


def _fake_warmup(workloads=((1, 1, 2048), (1, 512, 32768), (2, 1, 32768))):
    """Mimic upstream's warmup module: same names, same global call chain."""
    m = types.ModuleType(segmented.SEGMENTED_WARMUP_MODULE)
    m.torch = FakeTorch
    m._TABLES = {}
    m.tuned_widths = []
    m.compiled = []

    def _key(
        device,
        dtype,
        kv_dtype,
        heads,
        kv_heads,
        dim,
        page,
        scale,
        sliding_window=-1,
        causal=True,
        has_sinks=False,
    ):
        return (device, dtype, kv_dtype, heads, kv_heads, dim, page, scale)

    def _make_inputs(
        device,
        dtype,
        kv_dtype,
        heads,
        kv_heads,
        dim,
        page,
        max_tokens,
        workload,
        physical_seq_len=None,
    ):
        batch, _, seq_len = workload
        columns = math.ceil(max(seq_len, physical_seq_len or seq_len) / page)
        table = FakeTable(
            [[row * columns + col for col in range(columns)] for row in range(batch)]
        )
        return ("q", "k", "v", "kc", "vc", table, "starts", "lengths", "kv_scale")

    def segmented_query_capacity(query_len):
        return 1 << (query_len - 1).bit_length()

    def segmented_workspace_shapes(batch, qcap, heads, kv_heads, dim, splits):
        return None if splits == 1 else ((batch, splits), (batch, splits, 1))

    def compile_segmented_attention(
        q,
        out,
        kc,
        vc,
        table,
        starts,
        lengths,
        max_query_len,
        k_scale,
        v_scale,
        scale,
        config,
        workspace,
        *,
        sliding_window=-1,
        causal=True,
        sinks=None,
    ):
        m.compiled.append((table.shape[1], max_query_len, config["bm"], workspace))

    def warmup_segmented_attention(
        device,
        dtype,
        heads,
        kv_heads,
        dim,
        page,
        scale,
        max_tokens,
        max_len,
        max_seqs,
        *,
        memory_budget_bytes=None,
        cache_layouts=(),
        cache_budget_bytes=None,
        kv_dtype="bf16",
        sliding_window=-1,
        causal=True,
        max_query_len=8192,
        has_sinks=False,
        physical_max_len=None,
    ):
        key = m._key(device, dtype, kv_dtype, heads, kv_heads, dim, page, scale)
        if key in m._TABLES:
            return
        for workload in workloads:
            inputs = m._make_inputs(
                device,
                dtype,
                kv_dtype,
                heads,
                kv_heads,
                dim,
                page,
                max_tokens,
                workload,
            )
            m.tuned_widths.append(inputs[5].shape[1])
        m._TABLES[key] = {
            "records": [
                {"workload": list(w), "best": {"bm": 16 * (i + 1), "splits": i + 1}}
                for i, w in enumerate(workloads)
            ]
        }

    def _warmup_segmented_attention(layer, device, **limits):
        m.warmup_segmented_attention(
            device,
            "fp16",
            24,
            4,
            256,
            layer.page,
            0.0625,
            2048,
            32768,
            2,
            kv_dtype="fp16",
            physical_max_len=32768,
            **limits,
        )

    def rocm_segmented_attn_autotune_warmup(worker):
        context = worker.vllm_config.compilation_config.static_forward_context
        for layer in context.values():
            if getattr(layer, "segmented", False):
                m._warmup_segmented_attention(layer, "cuda:0", memory_budget_bytes=1)

    for function in (
        _key,
        _make_inputs,
        segmented_query_capacity,
        segmented_workspace_shapes,
        compile_segmented_attention,
        warmup_segmented_attention,
        _warmup_segmented_attention,
        rocm_segmented_attn_autotune_warmup,
    ):
        setattr(m, function.__name__, function)
    return m


class Stride:
    def __init__(self, width: int) -> None:
        self.width = width

    def stride(self, dim):
        return (self.width, 1)[dim]


def _worker(group_widths, *, runner="v2", layers=None):
    """A worker whose runner holds one attention group and one mamba group.

    Both groups use block size 400, as Qwen3.5's hybrid layout does, but the
    attention table is aligned to 128 tokens (88 columns at 32K) and the mamba
    table is not (82): a block-size key could not tell them apart.
    """
    layers = layers or {
        "model.layers.3.self_attn.attn": types.SimpleNamespace(
            page=400, segmented=True
        ),
        "model.layers.7.self_attn.attn": types.SimpleNamespace(
            page=400, segmented=True
        ),
        "model.layers.0.linear_attn": types.SimpleNamespace(page=400),
    }
    groups = [
        types.SimpleNamespace(
            layer_names=[name for name in layers if "self_attn" in name]
        ),
        types.SimpleNamespace(
            layer_names=[name for name in layers if "linear_attn" in name]
        ),
    ]
    if runner == "v2":
        model_runner = types.SimpleNamespace(
            block_tables=types.SimpleNamespace(
                block_sizes=[400] * len(group_widths),
                input_block_tables=[Stride(width) for width in group_widths],
            )
        )
    elif runner == "v1":
        model_runner = types.SimpleNamespace(
            input_batch=types.SimpleNamespace(
                block_table=types.SimpleNamespace(
                    block_tables=[
                        types.SimpleNamespace(
                            block_size=400,
                            block_table=types.SimpleNamespace(gpu=Stride(width)),
                        )
                        for width in group_widths
                    ]
                )
            )
        )
    else:
        model_runner = types.SimpleNamespace()
    model_runner.kv_cache_config = types.SimpleNamespace(kv_cache_groups=groups)
    return types.SimpleNamespace(
        model_runner=model_runner,
        vllm_config=types.SimpleNamespace(
            compilation_config=types.SimpleNamespace(static_forward_context=layers)
        ),
    )


class SegmentedRuntimeTablesTest(unittest.TestCase):
    def setUp(self) -> None:
        segmented._state.__init__()

    def _installed(self, **kwargs):
        warmup = _fake_warmup(**kwargs)
        segmented.install_segmented_runtime_tables(warmup)
        return warmup

    def test_flag_is_strict_and_off_by_default(self) -> None:
        self.assertFalse(segmented.segmented_runtime_tables_enabled({}))
        env = segmented.SEGMENTED_RUNTIME_TABLES_ENV
        self.assertTrue(segmented.segmented_runtime_tables_enabled({env: "1"}))
        self.assertFalse(segmented.segmented_runtime_tables_enabled({env: "0"}))
        with self.assertRaises(RuntimeError) as ctx:
            segmented.segmented_runtime_tables_enabled({env: "true"})
        self.assertIn(env, str(ctx.exception))

    def test_unset_flag_imports_nothing(self) -> None:
        with mock.patch.object(segmented.importlib, "import_module") as importer:
            self.assertFalse(segmented.install_segmented_runtime_tables_if_enabled({}))
        importer.assert_not_called()

    def test_flag_on_a_vllm_without_the_warmup_module_is_fatal(self) -> None:
        env = {segmented.SEGMENTED_RUNTIME_TABLES_ENV: "1"}
        with mock.patch.dict(sys.modules, {segmented.SEGMENTED_WARMUP_MODULE: None}):
            with self.assertRaises(RuntimeError) as ctx:
                segmented.install_segmented_runtime_tables_if_enabled(env)
        self.assertIn(segmented.SEGMENTED_RUNTIME_TABLES_ENV, str(ctx.exception))

    def test_flag_installs_into_the_imported_module(self) -> None:
        warmup = _fake_warmup()
        env = {segmented.SEGMENTED_RUNTIME_TABLES_ENV: "1"}
        with mock.patch.dict(sys.modules, {warmup.__name__: warmup}):
            self.assertTrue(segmented.install_segmented_runtime_tables_if_enabled(env))
        self.assertTrue(getattr(warmup, segmented.PATCH_MARKER))

    def test_a_reshaped_warmup_module_is_fatal(self) -> None:
        warmup = _fake_warmup()
        del warmup._make_inputs
        with self.assertRaises(RuntimeError) as ctx:
            segmented.install_segmented_runtime_tables(warmup)
        self.assertIn("_make_inputs", str(ctx.exception))

        warmup = _fake_warmup()
        original = warmup.warmup_segmented_attention
        warmup.warmup_segmented_attention = lambda device, dtype, page: original
        with self.assertRaises(RuntimeError) as ctx:
            segmented.install_segmented_runtime_tables(warmup)
        self.assertIn("kv_dtype", str(ctx.exception))

    def test_install_is_idempotent(self) -> None:
        warmup = self._installed()
        patched = warmup._make_inputs
        segmented.install_segmented_runtime_tables(warmup)
        self.assertIs(warmup._make_inputs, patched)

    def test_layer_widths_follow_the_layers_kv_cache_group(self) -> None:
        for runner in ("v2", "v1"):
            with self.subTest(runner=runner):
                worker = _worker([88, 82], runner=runner)
                widths = segmented.runtime_layer_widths(worker)
                context = worker.vllm_config.compilation_config.static_forward_context
                self.assertEqual(
                    {name: widths[id(layer)] for name, layer in context.items()},
                    {
                        "model.layers.3.self_attn.attn": 88,
                        "model.layers.7.self_attn.attn": 88,
                        "model.layers.0.linear_attn": 82,
                    },
                )

    def test_unknown_runner_layout_resolves_no_widths(self) -> None:
        self.assertEqual(segmented.runtime_layer_widths(_worker([88], runner="x")), {})
        # A table list that does not line up with the groups is not guessed at.
        self.assertEqual(segmented.runtime_layer_widths(_worker([88])), {})

    def test_tuning_and_winners_use_the_runtime_width(self) -> None:
        warmup = self._installed()
        warmup.rocm_segmented_attn_autotune_warmup(_worker([88, 82]))

        # Upstream would build 6- and 82-column tables (2048 and 32768 / 400).
        self.assertEqual(warmup.tuned_widths, [88, 88, 88])
        # One compile per tuned record, at the runtime width and with the
        # record's winner; the second layer shares the key and adds nothing.
        self.assertEqual(
            warmup.compiled,
            [
                (88, 1, 16, None),
                (88, 512, 32, (("workspace", (1, 2)), ("workspace", (1, 2, 1)))),
                (88, 1, 48, (("workspace", (2, 3)), ("workspace", (2, 3, 1)))),
            ],
        )
        # Outside the autotune call nothing is widened.
        table = warmup._make_inputs(
            "cuda:0", "fp16", "fp16", 24, 4, 256, 400, 2048, (1, 1, 2048)
        )[5]
        self.assertEqual(table.shape, (1, 6))

    def test_widened_table_keeps_the_upstream_entries(self) -> None:
        warmup = self._installed(workloads=((2, 1, 1200),))
        seen = []
        tune_inputs = warmup._make_inputs

        def record(*args, **kwargs):
            inputs = tune_inputs(*args, **kwargs)
            seen.append(inputs[5].rows)
            return inputs

        warmup._make_inputs = record
        warmup.rocm_segmented_attn_autotune_warmup(_worker([8, 3]))
        self.assertEqual(seen[0], [[0, 1, 2, 0, 0, 0, 0, 0], [3, 4, 5, 0, 0, 0, 0, 0]])

    def test_layers_without_a_runtime_table_keep_upstream_width(self) -> None:
        warmup = self._installed()
        stdout = []
        with mock.patch("builtins.print", side_effect=lambda *a, **k: stdout.append(a)):
            warmup.rocm_segmented_attn_autotune_warmup(_worker([88], runner="x"))
        self.assertEqual(warmup.tuned_widths, [6, 82, 82])
        self.assertEqual(warmup.compiled, [])
        self.assertEqual(len(stdout), 1)
        self.assertIn("no runtime block table", stdout[0][0])

    def test_a_failing_winner_compile_does_not_stop_the_rest(self) -> None:
        warmup = self._installed()
        compile_one = warmup.compile_segmented_attention

        def flaky(*args, **kwargs):
            if args[7] == 512:
                raise FakeTorch.OutOfMemoryError("HIP out of memory")
            return compile_one(*args, **kwargs)

        warmup.compile_segmented_attention = flaky
        with mock.patch("builtins.print"):
            warmup.rocm_segmented_attn_autotune_warmup(_worker([88, 82]))
        self.assertEqual([entry[1] for entry in warmup.compiled], [1, 1])

    def test_unpatched_module_tunes_at_upstream_width(self) -> None:
        warmup = _fake_warmup()
        warmup.rocm_segmented_attn_autotune_warmup(_worker([88, 82]))
        self.assertEqual(warmup.tuned_widths, [6, 82, 82])
        self.assertEqual(warmup.compiled, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
