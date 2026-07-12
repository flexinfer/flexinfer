"""Tests for the modules_in_block_to_quantize discovery + write helpers in
build/scripts/quantize_gptq.py.

The script is not directly importable (top-level execution requires runtime
env like MODEL_DIR plus transformers/torch model loading), so we extract just
the helper functions by parsing the AST and exec'ing them into an isolated
namespace. This keeps the test fast (<10s, CPU-only, no real model).
"""

from __future__ import annotations

import ast
import json
import os
import sys
import tempfile
import unittest
import types
from pathlib import Path


SCRIPT_PATH = Path(__file__).parent / "quantize_gptq.py"

# Helpers we need from the script. Order matters: dependencies first.
_HELPER_NAMES = (
    "_indexed_safetensors",
    "_parse_moe_expert_tensor_key",
    "_moe_fused_prefix",
    "_discover_quantized_module_suffixes",
    "detect_moe_architecture_from_config",
    "dynamic_config_excludes_moe_experts",
    "should_require_moe_expert_quantization",
    "inspect_moe_expert_visibility",
    "module_tree_declares_moe",
    "_gptqmodel_version",
    "_import_moe_lifecycle_hooks",
    "normalize_gptqmodel_model_type",
    "apply_model_policy",
    "patch_qwen35_text_processor_loading",
    "patch_moe_module_tree",
    "discover_saved_moe_expert_quantization",
    "_modules_in_block_shape_for_layout",
    "write_modules_in_block_to_quantize",
    "refuse_moe_expert_tensors",
)

try:
    import torch as _torch  # noqa: F401
    from safetensors.torch import load_file as _load_file  # noqa: F401
    from safetensors.torch import save_file as _save_file  # noqa: F401

    _TORCH_AVAILABLE = True
except Exception:  # noqa: BLE001 - torch/safetensors optional at test time.
    _TORCH_AVAILABLE = False


def _load_helpers() -> dict:
    """Return a namespace with just the metadata helpers loaded.

    Builds a synthetic module from the script's imports + the listed helper
    function defs. Avoids triggering the script's top-level pipeline.
    """
    source = SCRIPT_PATH.read_text()
    tree = ast.parse(source)

    # Only keep stdlib imports — the helpers don't need transformers/datasets/
    # torch/etc., and pulling them in defeats the "no real model" promise.
    allowed_modules = {"os", "re", "json", "sys", "time", "shutil", "gc", "copy"}

    keep_nodes: list[ast.stmt] = []
    for node in tree.body:
        if isinstance(node, ast.Import):
            allowed = [a for a in node.names if a.name.split(".")[0] in allowed_modules]
            if allowed:
                node.names = allowed
                keep_nodes.append(node)
            continue
        if isinstance(node, ast.ImportFrom):
            root = (node.module or "").split(".")[0]
            if root in allowed_modules:
                keep_nodes.append(node)
            continue
        if isinstance(node, ast.FunctionDef) and node.name in _HELPER_NAMES:
            keep_nodes.append(node)
            continue
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name) and target.id in (
                    "_GPTQ_QUANTIZED_LEAF_NAMES",
                    "_MOE_EXPERT_TENSOR_RE",
                    "_GPTQMODEL_MODEL_TYPE_ALIASES",
                ):
                    keep_nodes.append(node)
                    break

    # Stub emit_progress so we don't need FLEXINFER_TELEMETRY plumbing.
    stub_src = (
        "def emit_progress(event_type, **kwargs):\n"
        "    EMITTED_EVENTS.append({'event': event_type, **kwargs})\n"
        "EMITTED_EVENTS = []\n"
    )
    stub_tree = ast.parse(stub_src)
    keep_nodes = stub_tree.body + keep_nodes

    module = ast.Module(body=keep_nodes, type_ignores=[])
    ast.fix_missing_locations(module)

    namespace: dict = {"__name__": "_quantize_gptq_helpers"}
    code = compile(module, str(SCRIPT_PATH), "exec")
    exec(code, namespace)  # noqa: S102 - test-time, controlled source.
    return namespace


class _FakeSafeOpen:
    def __init__(self, path: str, framework: str = "pt") -> None:
        self.path = Path(path)

    def __enter__(self) -> "_FakeSafeOpen":
        return self

    def __exit__(self, *args: object) -> None:
        return None

    def keys(self) -> list[str]:
        return json.loads(
            (self.path.parent / f"{self.path.name}.keys.json").read_text()
        )


def _fake_safe_open(path: str, framework: str = "pt") -> _FakeSafeOpen:
    return _FakeSafeOpen(path, framework=framework)


class _FakeParam:
    shape = (128, 176, 2816)

    def dim(self) -> int:
        return 3


class _FakeMoEModel:
    module_tree = [
        "model",
        "layers",
        "#",
        {
            "self_attn": {},
            "mlp:moe:?": {
                "shared_expert:0": ("gate_proj:0", "up_proj:0", "down_proj:1"),
                "experts:0": {"#": ("gate_proj:0", "up_proj:0", "down_proj:1")},
            },
        },
    ]

    def named_modules(self) -> list[tuple[str, object]]:
        return [
            ("model.layers.0.mlp.experts.0.gate_proj", object()),
            ("model.layers.0.mlp.experts.0.up_proj", object()),
            ("model.layers.0.mlp.experts.0.down_proj", object()),
            ("model.layers.0.self_attn.q_proj", object()),
        ]

    def named_parameters(self) -> list[tuple[str, object]]:
        return [("model.layers.0.mlp.experts.gate_up_proj", _FakeParam())]


class ModulesInBlockToQuantizeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.helpers = _load_helpers()

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.save_dir = Path(self._tmp.name)
        self._orig_safetensors = sys.modules.get("safetensors")
        fake_safetensors = types.ModuleType("safetensors")
        fake_safetensors.safe_open = _fake_safe_open
        sys.modules["safetensors"] = fake_safetensors

    def tearDown(self) -> None:
        if self._orig_safetensors is None:
            sys.modules.pop("safetensors", None)
        else:
            sys.modules["safetensors"] = self._orig_safetensors
        self._tmp.cleanup()

    def _write_fake_artifact(
        self,
        tensor_keys: list[str],
        config_qcfg: dict | None = None,
        quantize_cfg: dict | None = None,
    ) -> None:
        (self.save_dir / "model.safetensors").write_bytes(b"fake")
        (self.save_dir / "model.safetensors.keys.json").write_text(
            json.dumps(tensor_keys)
        )

        config_data = {
            "_name_or_path": "fake/qwen3-test",
            "model_type": "qwen3",
            "quantization_config": config_qcfg if config_qcfg is not None else {},
        }
        (self.save_dir / "config.json").write_text(json.dumps(config_data, indent=2))

        if quantize_cfg is None:
            quantize_cfg = {"bits": 4, "group_size": 128, "sym": True}
        (self.save_dir / "quantize_config.json").write_text(
            json.dumps(quantize_cfg, indent=2)
        )

    def _read_modules(self, rel_path: str, key_path: tuple[str, ...]) -> object:
        data = json.loads((self.save_dir / rel_path).read_text())
        for key in key_path:
            if not isinstance(data, dict):
                return None
            data = data.get(key)
        return data

    def test_discover_walks_shards_and_writes_both_files(self) -> None:
        tensor_keys = [
            "model.layers.0.self_attn.q_proj.qweight",
            "model.layers.0.self_attn.q_proj.qzeros",
            "model.layers.0.self_attn.q_proj.scales",
            "model.layers.0.self_attn.q_proj.g_idx",
            "model.layers.0.self_attn.k_proj.qweight",
            "model.layers.0.self_attn.v_proj.qweight",
            "model.layers.0.self_attn.o_proj.qweight",
            "model.layers.0.mlp.gate_proj.qweight",
            "model.layers.0.mlp.up_proj.qweight",
            "model.layers.0.mlp.down_proj.qweight",
            # Non-quantized tensors that must NOT be picked up.
            "model.layers.0.self_attn.q_proj.weight",
            "model.embed_tokens.weight",
            "lm_head.weight",
        ]
        self._write_fake_artifact(tensor_keys)

        result = self.helpers["write_modules_in_block_to_quantize"](str(self.save_dir))

        expected = sorted(
            [
                "self_attn.q_proj",
                "self_attn.k_proj",
                "self_attn.v_proj",
                "self_attn.o_proj",
                "mlp.gate_proj",
                "mlp.up_proj",
                "mlp.down_proj",
            ]
        )
        self.assertEqual(result, expected)

        quantize_modules = self._read_modules(
            "quantize_config.json", ("modules_in_block_to_quantize",)
        )
        config_modules = self._read_modules(
            "config.json", ("quantization_config", "modules_in_block_to_quantize")
        )
        self.assertEqual(quantize_modules, expected)
        self.assertEqual(config_modules, expected)

        # Other quantize_config keys preserved.
        quantize_data = json.loads((self.save_dir / "quantize_config.json").read_text())
        self.assertEqual(quantize_data["bits"], 4)
        self.assertEqual(quantize_data["group_size"], 128)
        self.assertEqual(quantize_data["sym"], True)

    def test_idempotent_when_already_set(self) -> None:
        preset = ["self_attn.q_proj", "mlp.gate_proj"]
        tensor_keys = [
            "model.layers.0.self_attn.q_proj.qweight",
            "model.layers.0.mlp.up_proj.qweight",
        ]
        self._write_fake_artifact(
            tensor_keys,
            quantize_cfg={
                "bits": 4,
                "group_size": 128,
                "sym": True,
                "modules_in_block_to_quantize": list(preset),
            },
        )

        result = self.helpers["write_modules_in_block_to_quantize"](str(self.save_dir))

        self.assertEqual(result, preset)
        # Field on disk left exactly as the operator/policy set it.
        self.assertEqual(
            self._read_modules(
                "quantize_config.json", ("modules_in_block_to_quantize",)
            ),
            preset,
        )

    def test_hf_native_layout_writes_nested_shape(self) -> None:
        tensor_keys = [
            "model.layers.0.self_attn.q_proj.qweight",
            "model.layers.0.self_attn.k_proj.qweight",
            "model.layers.0.mlp.down_proj.qweight",
        ]
        self._write_fake_artifact(tensor_keys)

        result = self.helpers["write_modules_in_block_to_quantize"](
            str(self.save_dir), layout="hf-native"
        )

        expected = sorted(["self_attn.q_proj", "self_attn.k_proj", "mlp.down_proj"])
        self.assertEqual(result, expected)
        self.assertEqual(
            self._read_modules(
                "quantize_config.json", ("modules_in_block_to_quantize",)
            ),
            [expected],
        )
        self.assertEqual(
            self._read_modules(
                "config.json", ("quantization_config", "modules_in_block_to_quantize")
            ),
            [expected],
        )

    def test_overwrite_converts_nested_native_to_flat_vllm_shape(self) -> None:
        tensor_keys = ["model.layers.0.self_attn.q_proj.qweight"]
        self._write_fake_artifact(
            tensor_keys,
            quantize_cfg={
                "bits": 4,
                "group_size": 128,
                "sym": True,
                "modules_in_block_to_quantize": [["self_attn.q_proj"]],
            },
            config_qcfg={
                "quant_method": "gptq",
                "modules_in_block_to_quantize": [["self_attn.q_proj"]],
            },
        )

        result = self.helpers["write_modules_in_block_to_quantize"](
            str(self.save_dir),
            layout="vllm-gptq",
            modules=["self_attn.q_proj", "moe.gate_up_proj"],
            overwrite=True,
        )

        expected = ["self_attn.q_proj", "moe.gate_up_proj"]
        self.assertEqual(result, expected)
        self.assertEqual(
            self._read_modules(
                "quantize_config.json", ("modules_in_block_to_quantize",)
            ),
            expected,
        )
        self.assertEqual(
            self._read_modules(
                "config.json", ("quantization_config", "modules_in_block_to_quantize")
            ),
            expected,
        )

    def test_idempotent_when_only_config_qcfg_has_field(self) -> None:
        preset = ["mlp.down_proj"]
        tensor_keys = ["model.layers.0.mlp.down_proj.qweight"]
        self._write_fake_artifact(
            tensor_keys,
            config_qcfg={
                "quant_method": "gptq",
                "modules_in_block_to_quantize": list(preset),
            },
        )

        result = self.helpers["write_modules_in_block_to_quantize"](str(self.save_dir))

        self.assertEqual(result, preset)
        self.assertEqual(
            self._read_modules(
                "config.json", ("quantization_config", "modules_in_block_to_quantize")
            ),
            preset,
        )
        # quantize_config.json was NOT mutated to insert the field.
        quantize_data = json.loads((self.save_dir / "quantize_config.json").read_text())
        self.assertNotIn("modules_in_block_to_quantize", quantize_data)

    def test_no_quantized_tensors_skips_write(self) -> None:
        tensor_keys = [
            "model.embed_tokens.weight",
            "model.layers.0.self_attn.q_proj.weight",
            "lm_head.weight",
        ]
        self._write_fake_artifact(tensor_keys)

        result = self.helpers["write_modules_in_block_to_quantize"](str(self.save_dir))

        self.assertEqual(result, [])
        # Neither file was mutated to add the field.
        quantize_data = json.loads((self.save_dir / "quantize_config.json").read_text())
        config_data = json.loads((self.save_dir / "config.json").read_text())
        self.assertNotIn("modules_in_block_to_quantize", quantize_data)
        self.assertNotIn(
            "modules_in_block_to_quantize",
            config_data.get("quantization_config", {}),
        )

    def test_moe_requirement_auto_enabled_unless_experts_excluded(self) -> None:
        config = {
            "text_config": {
                "model_type": "qwen3_5_moe_text",
                "num_experts": 128,
                "num_experts_per_tok": 8,
            }
        }

        required, summary, reason = self.helpers[
            "should_require_moe_expert_quantization"
        ](config, None)
        self.assertTrue(required)
        self.assertTrue(summary["has_moe"])
        self.assertIn("MoE config", reason)

        required, _, reason = self.helpers["should_require_moe_expert_quantization"](
            config, {"-:.*experts.*": {}}
        )
        self.assertFalse(required)
        self.assertIn("full precision", reason)

    def test_normalize_gptqmodel_model_type_maps_moe_text_alias(self) -> None:
        normalize = self.helpers["normalize_gptqmodel_model_type"]
        # gptqmodel MODEL_MAP has no qwen3_5_moe_text key -> must map to the
        # registered MoE definition key so experts are not silently dropped.
        self.assertEqual(normalize("qwen3_5_moe_text"), "qwen3_5_moe")
        # Registered keys and unrelated types pass through unchanged.
        self.assertEqual(normalize("qwen3_5_moe"), "qwen3_5_moe")
        self.assertEqual(normalize("qwen3_5_text"), "qwen3_5_text")
        self.assertEqual(normalize("gemma4_text"), "gemma4_text")

    def test_text_policy_synthesizes_missing_pad_token_from_eos(self) -> None:
        apply_policy = self.helpers["apply_model_policy"]
        cfg = {
            "model_type": "qwen3_5_moe",
            "eos_token_id": 248044,
            "text_config": {
                "model_type": "qwen3_5_moe_text",
                "vocab_size": 248320,
            },
        }
        policy = {
            "name": "qwen3.5-moe-text",
            "extract_text_config": True,
            "copy_root_keys": ["eos_token_id", "pad_token_id"],
            "remap_model_type": "qwen3_5_moe",
        }

        active, _ = apply_policy(cfg, policy, {})

        self.assertEqual(active["eos_token_id"], 248044)
        self.assertEqual(active["pad_token_id"], 248044)
        self.assertEqual(active["model_type"], "qwen3_5_moe")

    def test_qwen_text_policy_disables_vlm_processor_loading(self) -> None:
        class FakeQwenDefinition:
            require_load_processor = True

        module_names = (
            "gptqmodel.models.definitions.qwen3_5",
            "gptqmodel.models.definitions.qwen3_5_moe",
        )
        previous = {name: sys.modules.get(name) for name in module_names}
        try:
            for name in module_names:
                sys.modules[name] = types.SimpleNamespace(
                    FakeQwenDefinition=FakeQwenDefinition
                )
            self.helpers["patch_qwen35_text_processor_loading"]()
        finally:
            for name, old_module in previous.items():
                if old_module is None:
                    sys.modules.pop(name, None)
                else:
                    sys.modules[name] = old_module

        self.assertFalse(FakeQwenDefinition.require_load_processor)

    def test_discovers_saved_moe_expert_quantization_shapes(self) -> None:
        tensor_keys = [
            "model.layers.0.mlp.experts.0.gate_proj.qweight",
            "model.layers.0.mlp.experts.0.up_proj.qweight",
            "model.layers.0.mlp.experts.0.down_proj.qweight",
            "model.layers.1.moe.gate_up_proj.qweight",
            "model.layers.1.moe.down_proj.qweight",
        ]
        self._write_fake_artifact(tensor_keys)

        check = self.helpers["discover_saved_moe_expert_quantization"](
            str(self.save_dir)
        )

        self.assertTrue(check["has_moe_expert_qweights"])
        self.assertEqual(check["vllm_modules"], ["moe.down_proj", "moe.gate_up_proj"])
        self.assertIn("mlp.experts.0.gate_proj", check["hf_native_modules"])

    def test_inspects_moe_expert_visibility(self) -> None:
        visibility = self.helpers["inspect_moe_expert_visibility"](_FakeMoEModel())

        self.assertTrue(visibility["module_tree_has_moe"])
        self.assertEqual(visibility["defused_expert_module_count"], 3)
        self.assertEqual(visibility["expert_gate_module_count"], 1)
        self.assertEqual(visibility["fused_3d_expert_parameter_count"], 1)

    def test_parses_qwen_mlp_expert_tensors_for_refuse(self) -> None:
        parse = self.helpers["_parse_moe_expert_tensor_key"]
        fused_prefix = self.helpers["_moe_fused_prefix"]

        parsed = parse("model.layers.7.mlp.experts.12.gate_proj.qweight")

        self.assertEqual(
            parsed,
            {
                "prefix": "model.layers.7.mlp.experts",
                "layer_idx": 7,
                "expert_idx": 12,
                "proj_type": "gate_proj",
                "tensor_type": "qweight",
            },
        )
        self.assertEqual(fused_prefix(parsed["prefix"]), "model.layers.7.moe")

        parsed = parse("model.layers.8.experts.2.down_proj.scales")
        self.assertEqual(parsed["prefix"], "model.layers.8.experts")
        self.assertEqual(parsed["proj_type"], "down_proj")
        self.assertEqual(fused_prefix(parsed["prefix"]), "model.layers.8.moe")

    def test_module_tree_declares_qwen_moe(self) -> None:
        declares = self.helpers["module_tree_declares_moe"]

        self.assertTrue(declares(_FakeMoEModel.module_tree))
        self.assertFalse(declares(["model", "layers", {"self_attn": {}}]))


class PatchMoEModuleTreeTests(unittest.TestCase):
    """patch_moe_module_tree decouples the module_tree injection from the
    lifecycle-hook import so a GPTQModel version bump can't silently skip it."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.helpers = _load_helpers()

    @staticmethod
    def _make_model(module_tree: list, module_names: list[str]) -> object:
        # Fresh class per call: patch_moe_module_tree mutates class attrs.
        class _Model:
            pass

        _Model.module_tree = module_tree

        def named_modules(self) -> list[tuple[str, object]]:
            return [(name, object()) for name in module_names]

        _Model.named_modules = named_modules
        return _Model()

    def test_injects_moe_entry_when_hooks_import_fails(self) -> None:
        # Regression: a failed lifecycle-hook import must NOT skip the
        # import-independent module_tree injection (the original ~70-min
        # silent-failure bug).
        self.helpers["_import_moe_lifecycle_hooks"] = lambda: (None, None)
        model = self._make_model(
            ["model", "layers", "#", {"self_attn": {}, "mlp": {}}],
            [
                "model.layers.0.experts.0.gate_proj",
                "model.layers.0.self_attn.q_proj",
            ],
        )
        status = self.helpers["patch_moe_module_tree"](model)

        self.assertTrue(status["module_tree_patched"])
        self.assertTrue(status["module_tree_has_moe"])
        self.assertFalse(status["hooks_attached"])
        self.assertEqual(status["expert_layer_count"], 1)
        entry = next(e for e in type(model).module_tree if isinstance(e, dict))
        self.assertIn("experts:moe:?", entry)

    def test_attaches_hooks_when_import_succeeds(self) -> None:
        class _FakeHooks:
            pass

        self.helpers["_import_moe_lifecycle_hooks"] = lambda: (
            _FakeHooks,
            "fake.path",
        )
        model = self._make_model(
            ["model", "layers", "#", {"self_attn": {}}],
            ["model.layers.0.experts.0.gate_proj"],
        )
        status = self.helpers["patch_moe_module_tree"](model)

        self.assertTrue(status["module_tree_patched"])
        self.assertTrue(status["hooks_attached"])
        self.assertEqual(status["hooks_import_path"], "fake.path")
        self.assertEqual(type(model).dynamic_expert_index, "num_experts")
        self.assertIsInstance(type(model).moe_lifecycle_hooks, _FakeHooks)

    def test_respects_existing_moe_tree(self) -> None:
        self.helpers["_import_moe_lifecycle_hooks"] = lambda: (None, None)
        model = self._make_model(
            [
                "model",
                "layers",
                "#",
                {"self_attn": {}, "mlp:moe:?": {"experts:0": {}}},
            ],
            ["model.layers.0.mlp.experts.0.gate_proj"],
        )
        status = self.helpers["patch_moe_module_tree"](model)

        self.assertFalse(status["module_tree_patched"])
        self.assertTrue(status["module_tree_has_moe"])

    def test_no_self_attn_entry_reports_reason(self) -> None:
        self.helpers["_import_moe_lifecycle_hooks"] = lambda: (None, None)
        model = self._make_model(
            ["model", "layers", "#", {"mlp": {}}],
            ["model.layers.0.experts.0.gate_proj"],
        )
        status = self.helpers["patch_moe_module_tree"](model)

        self.assertFalse(status["module_tree_patched"])
        self.assertFalse(status["module_tree_has_moe"])
        self.assertIn("self_attn", status["reason"])


@unittest.skipUnless(_TORCH_AVAILABLE, "torch + safetensors required")
class MoERefuseRoundTripTests(unittest.TestCase):
    """End-to-end re-fuse of per-expert 2D GPTQ tensors → vLLM fused 3D."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.helpers = _load_helpers()

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.save_dir = Path(self._tmp.name)

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def _write_min_config(self) -> None:
        (self.save_dir / "config.json").write_text(
            json.dumps({"model_type": "qwen3_5_moe", "quantization_config": {}})
        )
        (self.save_dir / "quantize_config.json").write_text(
            json.dumps({"bits": 4, "group_size": 128, "sym": True})
        )

    def _save_shard(self, tensors: dict) -> None:
        from safetensors.torch import save_file

        save_file(tensors, str(self.save_dir / "model.safetensors"))
        index = {
            "metadata": {"total_size": 0},
            "weight_map": {k: "model.safetensors" for k in tensors},
        }
        (self.save_dir / "model.safetensors.index.json").write_text(json.dumps(index))

    def _load_result(self) -> tuple[dict, dict]:
        from safetensors.torch import load_file

        index = json.loads((self.save_dir / "model.safetensors.index.json").read_text())
        out: dict = {}
        for shard in sorted(set(index["weight_map"].values())):
            out.update(load_file(str(self.save_dir / shard)))
        return out, index["weight_map"]

    def test_refuse_round_trip_fuses_and_drops_g_idx(self) -> None:
        import torch

        n_exp = 4
        tensors = {
            "model.layers.0.self_attn.q_proj.qweight": torch.zeros(
                4, 4, dtype=torch.int32
            ),
        }
        for e in range(n_exp):
            p = f"model.layers.0.mlp.experts.{e}"
            tensors[f"{p}.gate_proj.qweight"] = torch.full((4, 8), e, dtype=torch.int32)
            tensors[f"{p}.up_proj.qweight"] = torch.full(
                (4, 8), e + 10, dtype=torch.int32
            )
            tensors[f"{p}.down_proj.qweight"] = torch.full(
                (8, 4), e + 20, dtype=torch.int32
            )
            tensors[f"{p}.gate_proj.scales"] = torch.zeros(4, 8, dtype=torch.float16)
            tensors[f"{p}.up_proj.scales"] = torch.zeros(4, 8, dtype=torch.float16)
            tensors[f"{p}.down_proj.scales"] = torch.zeros(8, 4, dtype=torch.float16)
            tensors[f"{p}.gate_proj.g_idx"] = torch.zeros(8, dtype=torch.int32)
        self._write_min_config()
        self._save_shard(tensors)

        ok = self.helpers["refuse_moe_expert_tensors"](str(self.save_dir))
        self.assertTrue(ok)

        out, _ = self._load_result()
        gate_up = out["model.layers.0.moe.gate_up_proj.qweight"]
        self.assertEqual(list(gate_up.shape), [n_exp, 8, 8])
        down = out["model.layers.0.moe.down_proj.qweight"]
        self.assertEqual(list(down.shape), [n_exp, 8, 4])
        self.assertIn("model.layers.0.moe.gate_up_proj.scales", out)
        self.assertIn("model.layers.0.moe.down_proj.scales", out)

        # Per-expert keys removed.
        self.assertNotIn("model.layers.0.mlp.experts.0.gate_proj.qweight", out)
        # g_idx dropped entirely (vLLM MoeWNA16 ignores it).
        self.assertNotIn("model.layers.0.moe.gate_up_proj.g_idx", out)
        self.assertNotIn("model.layers.0.mlp.experts.0.gate_proj.g_idx", out)
        # Non-expert tensor preserved.
        self.assertIn("model.layers.0.self_attn.q_proj.qweight", out)
        # Expert 0 fused gate_up: top half == gate (0), bottom == up (10).
        self.assertTrue(bool((gate_up[0, :4, :] == 0).all()))
        self.assertTrue(bool((gate_up[0, 4:, :] == 10).all()))

    def test_refuse_keeps_incomplete_expert_set_intact(self) -> None:
        import torch

        n_exp = 4
        tensors: dict = {}
        for e in range(n_exp):
            p = f"model.layers.0.mlp.experts.{e}"
            tensors[f"{p}.gate_proj.qweight"] = torch.zeros(4, 8, dtype=torch.int32)
            tensors[f"{p}.up_proj.qweight"] = torch.zeros(4, 8, dtype=torch.int32)
        # down_proj for only 3 of 4 experts → incomplete; the guard must
        # leave the per-expert down keys intact rather than silently drop them.
        for e in range(n_exp - 1):
            p = f"model.layers.0.mlp.experts.{e}"
            tensors[f"{p}.down_proj.qweight"] = torch.zeros(8, 4, dtype=torch.int32)
        self._write_min_config()
        self._save_shard(tensors)

        ok = self.helpers["refuse_moe_expert_tensors"](str(self.save_dir))
        self.assertTrue(ok)  # gate/up still fuse

        out, _ = self._load_result()
        self.assertIn("model.layers.0.moe.gate_up_proj.qweight", out)
        # Incomplete down set: NOT fused, per-expert keys preserved (no loss).
        self.assertNotIn("model.layers.0.moe.down_proj.qweight", out)
        self.assertIn("model.layers.0.mlp.experts.0.down_proj.qweight", out)
        self.assertIn("model.layers.0.mlp.experts.2.down_proj.qweight", out)


if __name__ == "__main__":
    unittest.main()
