#!/usr/bin/env python3
"""Unit contract for flexinfer_vllm_common's opt-in ROCm tuning paths.

Covers the FlexInfer changes that sit behind env flags: the relaxed FLA
autotune pruning (``FLEXINFER_FLA_AUTOTUNE``), the segmented-attention
autotune at the runtime block-table width
(``FLEXINFER_SEGMENTED_RUNTIME_TABLES``), the input-embedding table in mapped
host memory (``FLEXINFER_EMBED_HOST``) and fp8 KV on segmented attention for
gfx11 (``FLEXINFER_SEGMENTED_FP8_GFX11``). Neither vLLM nor torch is needed:
the tests drive fake vLLM modules whose functions call each other through
module globals, as upstream's do, so each patch is exercised on the same call
path the engine takes.
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

from flexinfer_vllm_common import (  # noqa: E402
    embed_host,
    rocm,
    segmented,
    segmented_fp8,
)


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


# --- embed_host (FLEXINFER_EMBED_HOST) ---------------------------------------


class _Storage:
    def __init__(self, ptr: int) -> None:
        self.ptr = ptr

    def data_ptr(self) -> int:
        return self.ptr


class EmbedTensor:
    """Just enough of a torch tensor for embed_host: rows are plain values."""

    _next_ptr = 1000

    def __init__(self, rows, *, cuda=True, dtype="torch.float16", storage=None):
        self.rows = list(rows)
        self.shape = (len(self.rows), 4)
        self.dtype = dtype
        self.is_cuda = cuda
        self.device = "cuda:0" if cuda else "cpu"
        if storage is None:
            EmbedTensor._next_ptr += 1
            storage = _Storage(EmbedTensor._next_ptr)
        self._storage = storage

    def untyped_storage(self):
        return self._storage

    def is_floating_point(self) -> bool:
        return "float" in self.dtype

    def numel(self) -> int:
        return self.shape[0] * self.shape[1]

    def element_size(self) -> int:
        return 2

    def to(self, device):
        assert device == "cpu"
        return EmbedTensor(self.rows, cuda=False, dtype=self.dtype)

    def index_select(self, dim, rows):
        assert dim == 0
        return [self.rows[row] for row in rows]


class EmbedParam:
    """A Parameter whose tensor attributes follow ``.data``, as torch's do."""

    def __init__(self, data) -> None:
        self.data = data

    def __getattr__(self, name):
        return getattr(self.data, name)


class FakeEmbedTorch:
    long = "long"

    def __init__(self) -> None:
        self.allocated = 20 * 2**30
        self.empty_cache_calls = 0
        torch = self

        class _Cuda:
            @staticmethod
            def memory_allocated():
                return torch.allocated

            @staticmethod
            def synchronize():
                return None

            @staticmethod
            def empty_cache():
                torch.empty_cache_calls += 1

        self.cuda = _Cuda()
        self.accelerator = types.SimpleNamespace(empty_cache=_Cuda.empty_cache)

    @staticmethod
    def equal(a, b) -> bool:
        return a == b

    @staticmethod
    def tensor(values, dtype=None, device=None):
        return list(values)

    @staticmethod
    def zeros(count, dtype=None, device=None):
        return [0] * count


class Embedding:
    def __init__(self, weight) -> None:
        self.weight = weight


class LMHead(Embedding):
    pass


class Visual:
    pass


class EmbedModel:
    def __init__(self, modules) -> None:
        self._modules = modules

    def named_modules(self):
        return list(self._modules)


def _embed_model(*, tied=False, cuda=True, extra=()):
    table = EmbedParam(EmbedTensor(range(40), cuda=cuda))
    head = EmbedParam(table.data if tied else EmbedTensor(range(100, 140)))
    embed = Embedding(table)
    modules = [
        ("", None),
        ("language_model.model.embed_tokens", embed),
        ("language_model.lm_head", LMHead(head)),
        ("visual.patch_embed", Visual()),
        *extra,
    ]
    return EmbedModel(modules), embed


class _Mapper:
    """Stand-in for get_accelerator_view_from_cpu_tensor."""

    def __init__(self, corrupt: bool = False) -> None:
        self.calls = []
        self.corrupt = corrupt

    def __call__(self, cpu):
        assert not cpu.is_cuda, "the op must receive a host tensor"
        self.calls.append(cpu)
        rows = [-1] * len(cpu.rows) if self.corrupt else cpu.rows
        mapped = EmbedTensor(rows, cuda=True, dtype=cpu.dtype)
        mapped.mapped = True
        return mapped


def _embed_loader(model):
    module = types.ModuleType("fake_base_loader")

    class BaseModelLoader:
        def __init__(self) -> None:
            self.loads = []

        def load_model(self, vllm_config, model_config, prefix=""):
            self.loads.append((model_config, prefix))
            return model

    module.BaseModelLoader = BaseModelLoader
    return module


class EmbedHostTest(unittest.TestCase):
    def setUp(self) -> None:
        embed_host._state = embed_host._State()
        self.torch = FakeEmbedTorch()

    def _install(self, model, mapper=None):
        loader = _embed_loader(model)
        mapper = mapper or _Mapper()
        embed_host.install_embed_host(loader, self.torch, Embedding, LMHead, mapper)
        return loader, mapper

    def test_flag_is_strict(self) -> None:
        self.assertFalse(embed_host.embed_host_enabled({}))
        self.assertTrue(embed_host.embed_host_enabled({"FLEXINFER_EMBED_HOST": "1"}))
        with self.assertRaises(RuntimeError):
            embed_host.embed_host_enabled({"FLEXINFER_EMBED_HOST": "yes"})
        self.assertFalse(embed_host.install_embed_host_if_enabled({}))

    def test_selects_the_untied_input_table_only(self) -> None:
        model, embed = _embed_model()
        found = list(embed_host.embedding_tables(model, Embedding, LMHead))
        self.assertEqual(found, [("language_model.model.embed_tokens", embed)])

    def test_a_table_tied_to_the_lm_head_stays_on_the_device(self) -> None:
        model, _ = _embed_model(tied=True)
        with mock.patch("builtins.print") as printed:
            found = list(embed_host.embedding_tables(model, Embedding, LMHead))
        self.assertEqual(found, [])
        self.assertIn("tied to an lm_head", printed.call_args[0][0])

    def test_other_embeddings_and_host_tables_are_ignored(self) -> None:
        other = Embedding(EmbedParam(EmbedTensor(range(8))))
        model, _ = _embed_model(cuda=False, extra=[("visual.pos_embed", other)])
        self.assertEqual(
            list(embed_host.embedding_tables(model, Embedding, LMHead)), []
        )

    def test_target_load_swaps_in_the_mapped_view(self) -> None:
        model, embed = _embed_model()
        original = embed.weight.data
        loader, mapper = self._install(model)
        vllm_config = types.SimpleNamespace(model_config=object())
        with mock.patch("builtins.print") as printed:
            loaded = loader.BaseModelLoader().load_model(
                vllm_config=vllm_config, model_config=vllm_config.model_config
            )
        self.assertIs(loaded, model)
        self.assertEqual(len(mapper.calls), 1)
        self.assertTrue(getattr(embed.weight.data, "mapped", False))
        self.assertEqual(embed.weight.data.rows, original.rows)
        self.assertEqual(self.torch.empty_cache_calls, 1)
        record = embed_host._state.moved[0]
        self.assertEqual(record["name"], "language_model.model.embed_tokens")
        self.assertEqual(record["bytes"], 40 * 4 * 2)
        lines = [call[0][0] for call in printed.call_args_list]
        self.assertTrue(
            any("moved language_model.model.embed_tokens" in line for line in lines)
        )
        self.assertTrue(any("device allocated" in line for line in lines))

    def test_draft_loads_are_left_alone(self) -> None:
        model, embed = _embed_model()
        original = embed.weight.data
        loader, mapper = self._install(model)
        vllm_config = types.SimpleNamespace(model_config=object())
        loader.BaseModelLoader().load_model(vllm_config, object(), prefix="draft")
        self.assertEqual(mapper.calls, [])
        self.assertIs(embed.weight.data, original)
        self.assertEqual(embed_host._state.loads, 0)

    def test_a_target_without_an_eligible_table_fails_the_boot(self) -> None:
        model, _ = _embed_model(tied=True)
        loader, _ = self._install(model)
        vllm_config = types.SimpleNamespace(model_config=object())
        with mock.patch("builtins.print"), self.assertRaises(RuntimeError):
            loader.BaseModelLoader().load_model(vllm_config, vllm_config.model_config)

    def test_a_view_that_reads_back_wrong_is_refused(self) -> None:
        model, embed = _embed_model()
        original = embed.weight.data
        loader, _ = self._install(model, _Mapper(corrupt=True))
        vllm_config = types.SimpleNamespace(model_config=object())
        with self.assertRaises(RuntimeError):
            loader.BaseModelLoader().load_model(vllm_config, vllm_config.model_config)
        self.assertIs(embed.weight.data, original)

    def test_install_is_idempotent_and_checks_the_loader(self) -> None:
        model, _ = _embed_model()
        loader, mapper = self._install(model)
        wrapped = loader.BaseModelLoader.load_model
        embed_host.install_embed_host(loader, self.torch, Embedding, LMHead, mapper)
        self.assertIs(loader.BaseModelLoader.load_model, wrapped)
        with self.assertRaises(RuntimeError):
            embed_host.install_embed_host(
                types.ModuleType("empty"), self.torch, Embedding, LMHead, mapper
            )

    def test_spot_rows_span_the_table(self) -> None:
        rows = embed_host._spot_rows(FakeEmbedTorch(), 248320, "cuda:0")
        self.assertEqual(rows[0], 0)
        self.assertEqual(rows[-1], 248319)
        self.assertEqual(len(rows), 8)


# --- segmented_fp8 (FLEXINFER_SEGMENTED_FP8_GFX11) --------------------------


def _fake_segmented(arch: str = "gfx11"):
    """Fake platform, ops and backend modules with upstream's three gates."""
    platform = types.ModuleType("fake_platforms_rocm")
    platform.on_gfx11 = lambda: arch == "gfx11"
    platform.on_gfx1x = lambda: arch in ("gfx11", "gfx12")
    platform.on_gfx12x = lambda: arch == "gfx12"
    fp8 = ("fp8", "fp8_e4m3")

    ops = types.ModuleType("fake_ops_segmented_attention")
    ops.launched = []
    ops.eligible = True

    def run_segmented_attention(*args, **kwargs):
        ops.launched.append(kwargs.get("tag"))
        return "segmented"

    def segmented_attention(query=None, *, kv_cache_dtype, tag=None):
        # Upstream reads the platform at call time, then calls the runner
        # through its module global.
        is_fp8 = kv_cache_dtype in fp8
        if ops.eligible and (platform.on_gfx12x() if is_fp8 else platform.on_gfx1x()):
            return ops.run_segmented_attention(tag=tag)
        return "unified"

    ops.run_segmented_attention = run_segmented_attention
    ops.segmented_attention = segmented_attention

    backend_module = types.ModuleType("fake_rocm_segmented_attn")

    class RocmSegmentedAttentionBackend:
        @classmethod
        def supports_combination(
            cls, head_size, dtype, kv_cache_dtype, block_size, use_mla
        ):
            if kv_cache_dtype in fp8 and not platform.on_gfx12x():
                return "FP8 segmented attention requires gfx12"
            return None

    class RocmSegmentedAttentionImpl:
        def __init__(self, num_heads, head_size, kv_cache_dtype, sinks=None):
            supported = (
                platform.on_gfx12x() if kv_cache_dtype in fp8 else platform.on_gfx1x()
            )
            if not supported:
                raise ValueError("ROCM_SEGMENTED_ATTN requires gfx12 for FP8")
            self.kv_cache_dtype = kv_cache_dtype

        def forward(self, query):
            return backend_module.segmented_attention(
                query, kv_cache_dtype=self.kv_cache_dtype, tag="forward"
            )

    backend_module.RocmSegmentedAttentionBackend = RocmSegmentedAttentionBackend
    backend_module.RocmSegmentedAttentionImpl = RocmSegmentedAttentionImpl
    backend_module.segmented_attention = segmented_attention
    return platform, ops, backend_module


class SegmentedFp8Test(unittest.TestCase):
    def setUp(self) -> None:
        segmented_fp8._state = segmented_fp8._State()

    def _installed(self, arch="gfx11"):
        platform, ops, backend = _fake_segmented(arch)
        active = segmented_fp8.install_segmented_fp8(backend, ops, platform)
        return active, platform, ops, backend

    def test_flag_is_strict(self) -> None:
        env = segmented_fp8.SEGMENTED_FP8_ENV
        self.assertFalse(segmented_fp8.segmented_fp8_enabled({}))
        self.assertTrue(segmented_fp8.segmented_fp8_enabled({env: "1"}))
        with self.assertRaises(RuntimeError):
            segmented_fp8.segmented_fp8_enabled({env: "true"})
        self.assertFalse(segmented_fp8.install_segmented_fp8_if_enabled({}))

    def test_upstream_gates_fp8_to_gfx12(self) -> None:
        platform, ops, backend = _fake_segmented("gfx11")
        b, impl = (
            backend.RocmSegmentedAttentionBackend,
            backend.RocmSegmentedAttentionImpl,
        )
        self.assertIsNotNone(b.supports_combination(256, "half", "fp8_e4m3", 16, False))
        with self.assertRaises(ValueError):
            impl(24, 256, "fp8_e4m3")
        self.assertEqual(
            backend.segmented_attention(kv_cache_dtype="fp8_e4m3"), "unified"
        )

    def test_gfx11_fp8_takes_the_segmented_kernel(self) -> None:
        active, platform, ops, backend = self._installed()
        self.assertTrue(active)
        b = backend.RocmSegmentedAttentionBackend
        self.assertIsNone(b.supports_combination(256, "half", "fp8_e4m3", 16, False))
        self.assertIsNone(
            b.supports_combination(
                head_size=256,
                dtype="half",
                kv_cache_dtype="fp8",
                block_size=16,
                use_mla=False,
            )
        )
        layer = backend.RocmSegmentedAttentionImpl(24, 256, "fp8_e4m3")
        with mock.patch("builtins.print") as printed:
            self.assertEqual(layer.forward("q"), "segmented")
        self.assertEqual(ops.launched, ["forward"])
        self.assertEqual(
            segmented_fp8.fp8_dispatch_counts(), {"segmented": 1, "fallback": 0}
        )
        self.assertIn("segmented=1 fallback=0", printed.call_args[0][0])
        # The scope closes: everything else still sees a gfx11 host.
        self.assertFalse(platform.on_gfx12x())
        self.assertIs(ops.run_segmented_attention.__name__, "run_segmented_attention")

    def test_fp16_paths_are_untouched(self) -> None:
        _, platform, ops, backend = self._installed()
        layer = backend.RocmSegmentedAttentionImpl(24, 256, "auto")
        self.assertEqual(layer.forward("q"), "segmented")
        self.assertEqual(
            segmented_fp8.fp8_dispatch_counts(), {"segmented": 0, "fallback": 0}
        )
        self.assertIsNone(
            backend.RocmSegmentedAttentionBackend.supports_combination(
                256, "half", "auto", 16, False
            )
        )

    def test_an_ineligible_fp8_shape_is_counted_as_fallback(self) -> None:
        _, platform, ops, backend = self._installed()
        ops.eligible = False
        layer = backend.RocmSegmentedAttentionImpl(24, 256, "fp8")
        with mock.patch("builtins.print"):
            self.assertEqual(layer.forward("q"), "unified")
        self.assertEqual(
            segmented_fp8.fp8_dispatch_counts(), {"segmented": 0, "fallback": 1}
        )

    def test_the_gate_closes_when_the_call_raises(self) -> None:
        _, platform, ops, backend = self._installed()

        def boom(*args, **kwargs):
            raise RuntimeError("kernel failed")

        ops.run_segmented_attention = boom
        layer = backend.RocmSegmentedAttentionImpl(24, 256, "fp8_e4m3")
        with self.assertRaises(RuntimeError):
            layer.forward("q")
        self.assertFalse(platform.on_gfx12x())
        self.assertIs(ops.run_segmented_attention, boom)

    def test_counts_print_at_powers_of_ten(self) -> None:
        self.assertEqual(
            [n for n in range(1, 1001) if segmented_fp8._is_power_of_ten(n)],
            [1, 10, 100, 1000],
        )

    def test_gfx12_and_non_rdna_hosts_are_not_patched(self) -> None:
        for arch in ("gfx12", "cdna"):
            active, platform, ops, backend = self._installed(arch)
            self.assertFalse(active)
            self.assertFalse(hasattr(backend, segmented_fp8.PATCH_MARKER))

    def test_install_is_idempotent(self) -> None:
        _, platform, ops, backend = self._installed()
        wrapped = backend.segmented_attention
        self.assertTrue(segmented_fp8.install_segmented_fp8(backend, ops, platform))
        self.assertIs(backend.segmented_attention, wrapped)

    def test_a_reshaped_vllm_fails_the_boot(self) -> None:
        platform, ops, backend = _fake_segmented()
        del ops.run_segmented_attention
        with self.assertRaises(RuntimeError):
            segmented_fp8.install_segmented_fp8(backend, ops, platform)


if __name__ == "__main__":
    unittest.main(verbosity=2)
