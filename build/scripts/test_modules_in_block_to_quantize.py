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
    "_discover_quantized_module_suffixes",
    "detect_moe_architecture_from_config",
    "dynamic_config_excludes_moe_experts",
    "should_require_moe_expert_quantization",
    "inspect_moe_expert_visibility",
    "discover_saved_moe_expert_quantization",
    "_modules_in_block_shape_for_layout",
    "write_modules_in_block_to_quantize",
)


def _load_helpers() -> dict:
    """Return a namespace with just the metadata helpers loaded.

    Builds a synthetic module from the script's imports + the listed helper
    function defs. Avoids triggering the script's top-level pipeline.
    """
    source = SCRIPT_PATH.read_text()
    tree = ast.parse(source)

    # Only keep stdlib imports — the helpers don't need transformers/datasets/
    # torch/etc., and pulling them in defeats the "no real model" promise.
    allowed_modules = {"os", "re", "json", "sys", "time", "shutil"}

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
                if (
                    isinstance(target, ast.Name)
                    and target.id == "_GPTQ_QUANTIZED_LEAF_NAMES"
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
        return json.loads((self.path.parent / f"{self.path.name}.keys.json").read_text())


def _fake_safe_open(path: str, framework: str = "pt") -> _FakeSafeOpen:
    return _FakeSafeOpen(path, framework=framework)


class _FakeParam:
    shape = (128, 176, 2816)

    def dim(self) -> int:
        return 3


class _FakeMoEModel:
    module_tree = ["model", "layers", {"self_attn": {}, "experts:moe:?": {}}]

    def named_modules(self) -> list[tuple[str, object]]:
        return [
            ("model.layers.0.experts.0.gate_proj", object()),
            ("model.layers.0.experts.0.up_proj", object()),
            ("model.layers.0.experts.0.down_proj", object()),
            ("model.layers.0.self_attn.q_proj", object()),
        ]

    def named_parameters(self) -> list[tuple[str, object]]:
        return [("model.layers.0.experts.gate_up_proj", _FakeParam())]


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

        expected = sorted(
            ["self_attn.q_proj", "self_attn.k_proj", "mlp.down_proj"]
        )
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
                "model_type": "qwen3_5_text",
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

    def test_discovers_saved_moe_expert_quantization_shapes(self) -> None:
        tensor_keys = [
            "model.layers.0.experts.0.gate_proj.qweight",
            "model.layers.0.experts.0.up_proj.qweight",
            "model.layers.0.experts.0.down_proj.qweight",
            "model.layers.1.moe.gate_up_proj.qweight",
            "model.layers.1.moe.down_proj.qweight",
        ]
        self._write_fake_artifact(tensor_keys)

        check = self.helpers["discover_saved_moe_expert_quantization"](
            str(self.save_dir)
        )

        self.assertTrue(check["has_moe_expert_qweights"])
        self.assertEqual(
            check["vllm_modules"], ["moe.down_proj", "moe.gate_up_proj"]
        )
        self.assertIn("experts.0.gate_proj", check["hf_native_modules"])

    def test_inspects_moe_expert_visibility(self) -> None:
        visibility = self.helpers["inspect_moe_expert_visibility"](_FakeMoEModel())

        self.assertTrue(visibility["module_tree_has_moe"])
        self.assertEqual(visibility["defused_expert_module_count"], 3)
        self.assertEqual(visibility["expert_gate_module_count"], 1)
        self.assertEqual(visibility["fused_3d_expert_parameter_count"], 1)


if __name__ == "__main__":
    unittest.main()
