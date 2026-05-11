from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

import validate_quantized_artifact as validator


class ValidateQuantizedArtifactTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.artifact = Path(self.temp_dir.name)

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def _write_json(self, relative_path: str, payload: dict) -> None:
        target = self.artifact / relative_path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(payload, indent=2))

    def _touch(self, relative_path: str) -> None:
        target = self.artifact / relative_path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(b"")

    def _write_safetensors(self, relative_path: str, tensors: dict) -> None:
        try:
            import torch
            from safetensors.torch import save_file
        except Exception as exc:  # noqa: BLE001 - optional test dependency.
            self.skipTest(f"safetensors/torch unavailable: {exc}")

        normalized = {}
        for key, value in tensors.items():
            if hasattr(value, "detach"):
                normalized[key] = value
            else:
                normalized[key] = torch.tensor(value, dtype=torch.int32)

        target = self.artifact / relative_path
        target.parent.mkdir(parents=True, exist_ok=True)
        save_file(normalized, str(target))

    def _seed_base_config(
        self,
        model_name: str = "google/gemma-4-26b-a4b-it",
        quantization_config: dict | None = None,
        extra_config: dict | None = None,
    ) -> None:
        config: dict = {"_name_or_path": model_name}
        if quantization_config is not None:
            config["quantization_config"] = quantization_config
        if extra_config is not None:
            config.update(extra_config)
        self._write_json("config.json", config)

    def test_detect_repeated_token_runs(self) -> None:
        repeated = validator.detect_repeated_token_runs(["tok", "tok", "tok", "x"])
        self.assertTrue(repeated["has_repetition"])
        self.assertEqual(repeated["max_run"], 3)
        self.assertEqual(repeated["token"], "tok")

        unique = validator.detect_repeated_token_runs(["a", "b", "c"], min_run=2)
        self.assertFalse(unique["has_repetition"])
        self.assertEqual(unique["max_run"], 0)

    def test_missing_shard_fails_validation(self) -> None:
        self._seed_base_config(
            quantization_config={"modules_in_block_to_quantize": [["moe.gate_up_proj"]]}
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.moe.gate_up_proj.qweight": "model-00001-of-00002.safetensors"
                }
            },
        )
        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq"
        )
        self.assertFalse(result["ok"])
        self.assertTrue(
            any("missing shard files" in error for error in result["errors"])
        )

    def test_vllm_flat_modules_pass_silently(self) -> None:
        """Flat is the expected shape for vllm-gptq; no warning should fire."""
        self._seed_base_config(
            quantization_config={
                "modules_in_block_to_quantize": ["moe.gate_up_proj", "moe.down_proj"]
            }
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.moe.gate_up_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.gate_up_proj.scales": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.gate_up_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(self.artifact, requested_layout="auto")
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["layout"], "vllm-gptq")
        self.assertEqual(result["family"], "gemma4-26b-a4b")
        self.assertEqual(
            result["checks"]["quantized_module_counts"], {"moe.gate_up_proj": 1}
        )
        self.assertFalse(
            any("flat string list" in warning for warning in result["warnings"]),
            result["warnings"],
        )

    def test_family_autodetect_from_model_type_and_layer_count(self) -> None:
        """Gemma4 serving artifacts strip _name_or_path; detect via model_type + num_hidden_layers."""
        self._write_json(
            "config.json",
            {
                "model_type": "gemma4_text",
                "architectures": ["Gemma4ForCausalLM"],
                "num_hidden_layers": 30,
                "num_experts": 128,
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "moe.gate_up_proj",
                        "moe.down_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.moe.gate_up_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.gate_up_proj.scales": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.gate_up_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(self.artifact, requested_layout="auto")
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["family"], "gemma4-26b-a4b")
        self.assertEqual(result["checks"]["detected_family"], "gemma4-26b-a4b")

    def test_family_autodetect_variant_discrimination_by_layer_count(self) -> None:
        """Same model_type but num_hidden_layers=42 must resolve to gemma4-31b."""
        self._write_json(
            "config.json",
            {
                "model_type": "gemma4_text",
                "architectures": ["Gemma4ForCausalLM"],
                "num_hidden_layers": 42,
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "moe.gate_up_proj",
                        "moe.down_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.moe.gate_up_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.gate_up_proj.scales": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.gate_up_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(self.artifact, requested_layout="auto")
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["family"], "gemma4-31b")
        self.assertEqual(result["checks"]["detected_family"], "gemma4-31b")

    def test_gemma4_31b_repeated_qweights_fail_validation(self) -> None:
        self._write_json(
            "config.json",
            {
                "model_type": "gemma4_text",
                "architectures": ["Gemma4ForCausalLM"],
                "num_hidden_layers": 42,
                "quantization_config": {
                    "modules_in_block_to_quantize": ["self_attn.q_proj"]
                },
            },
        )
        keys = {
            "model.layers.40.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
            "model.layers.40.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
            "model.layers.41.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
            "model.layers.41.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
        }
        self._write_json("model.safetensors.index.json", {"weight_map": keys})
        self._write_safetensors(
            "model-00001-of-00001.safetensors",
            {
                "model.layers.40.self_attn.q_proj.qweight": [[1, 2], [3, 4]],
                "model.layers.40.self_attn.q_proj.qzeros": [[0, 0]],
                "model.layers.41.self_attn.q_proj.qweight": [[1, 2], [3, 4]],
                "model.layers.41.self_attn.q_proj.qzeros": [[0, 0]],
            },
        )

        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq", requested_family="gemma4-31b"
        )

        self.assertFalse(result["ok"], result)
        self.assertTrue(
            any("repeated qweight tensors" in error for error in result["errors"]),
            result["errors"],
        )
        duplicate_groups = result["checks"]["repeated_tensor_guard"][
            "duplicate_groups"
        ]
        self.assertEqual(duplicate_groups[0]["module"], "self_attn.q_proj")
        self.assertEqual(duplicate_groups[0]["layers"], [40, 41])

    def test_gemma4_31b_distinct_qweights_pass_repeated_guard(self) -> None:
        self._write_json(
            "config.json",
            {
                "model_type": "gemma4_text",
                "architectures": ["Gemma4ForCausalLM"],
                "num_hidden_layers": 42,
                "quantization_config": {
                    "modules_in_block_to_quantize": ["self_attn.q_proj"]
                },
            },
        )
        keys = {
            "model.layers.40.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
            "model.layers.40.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
            "model.layers.41.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
            "model.layers.41.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
        }
        self._write_json("model.safetensors.index.json", {"weight_map": keys})
        self._write_safetensors(
            "model-00001-of-00001.safetensors",
            {
                "model.layers.40.self_attn.q_proj.qweight": [[1, 2], [3, 4]],
                "model.layers.40.self_attn.q_proj.qzeros": [[0, 0]],
                "model.layers.41.self_attn.q_proj.qweight": [[1, 2], [3, 5]],
                "model.layers.41.self_attn.q_proj.qzeros": [[0, 0]],
            },
        )

        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq", requested_family="gemma4-31b"
        )

        self.assertTrue(result["ok"], result)
        self.assertEqual(
            result["checks"]["repeated_tensor_guard"]["duplicate_groups"], []
        )

    def test_qwen36_gdn_qweights_warn_validation(self) -> None:
        self._write_json(
            "config.json",
            {
                "model_type": "qwen3_5",
                "architectures": ["Qwen3_5ForConditionalGeneration"],
                "num_hidden_layers": 64,
                "vocab_size": 248320,
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "self_attn.q_proj",
                        "self_attn.k_proj",
                        "self_attn.v_proj",
                        "self_attn.o_proj",
                        "mlp.gate_proj",
                        "mlp.up_proj",
                        "mlp.down_proj",
                        "linear_attn.in_proj_qkv",
                        "linear_attn.in_proj_z",
                        "linear_attn.out_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.linear_attn.in_proj_qkv.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.linear_attn.in_proj_qkv.qzeros": "model-00001-of-00001.safetensors",
                    "model.layers.0.linear_attn.in_proj_z.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.linear_attn.out_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(self.artifact, requested_layout="auto")

        self.assertTrue(result["ok"], result)
        self.assertEqual(result["layout"], "vllm-gptq")
        self.assertEqual(result["family"], "qwen36-27b")
        self.assertTrue(
            any("GDN GPTQ policy warning" in warning for warning in result["warnings"]),
            result["warnings"],
        )
        self.assertEqual(
            result["checks"]["gdn_gptq_policy"]["quantized_gdn_modules"],
            {
                "linear_attn.in_proj_qkv": 1,
                "linear_attn.in_proj_z": 1,
                "linear_attn.out_proj": 1,
            },
        )

    def test_qwen36_gdn_policy_passes_when_linear_attention_is_fp(self) -> None:
        self._write_json(
            "config.json",
            {
                "text_config": {
                    "model_type": "qwen3_5_text",
                    "architectures": ["Qwen3_5ForConditionalGeneration"],
                    "num_hidden_layers": 64,
                    "vocab_size": 248320,
                },
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "self_attn.q_proj",
                        "self_attn.k_proj",
                        "self_attn.v_proj",
                        "self_attn.o_proj",
                        "mlp.gate_proj",
                        "mlp.up_proj",
                        "mlp.down_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.linear_attn.in_proj_qkv.weight": "model-00001-of-00001.safetensors",
                    "model.layers.0.linear_attn.in_proj_z.weight": "model-00001-of-00001.safetensors",
                    "model.layers.0.linear_attn.out_proj.weight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                    "model.layers.3.mlp.down_proj.qweight": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq"
        )

        self.assertTrue(result["ok"], result)
        self.assertEqual(result["family"], "qwen36-27b")
        self.assertFalse(
            any("GDN GPTQ policy warning" in warning for warning in result["warnings"]),
            result["warnings"],
        )
        self.assertEqual(
            result["checks"]["gdn_gptq_policy"]["quantized_gdn_modules"], {}
        )

    def test_qwen36_moe_expert_gate_fails_when_expert_qweights_missing(
        self,
    ) -> None:
        self._write_json(
            "config.json",
            {
                "text_config": {
                    "model_type": "qwen3_5_text",
                    "architectures": ["Qwen3_5ForConditionalGeneration"],
                    "num_hidden_layers": 64,
                    "vocab_size": 248320,
                    "num_experts": 128,
                    "num_experts_per_tok": 8,
                },
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "self_attn.q_proj",
                        "self_attn.k_proj",
                        "self_attn.v_proj",
                        "self_attn.o_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.3.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq"
        )

        self.assertFalse(result["ok"], result)
        self.assertTrue(
            any("Qwen MoE expert quantization gate failed" in e for e in result["errors"]),
            result["errors"],
        )
        self.assertEqual(
            result["checks"]["qwen_moe_expert_quantization"]["missing_modules"],
            ["moe.gate_up_proj", "moe.down_proj"],
        )

    def test_qwen36_moe_expert_gate_passes_with_fused_expert_qweights(self) -> None:
        self._write_json(
            "config.json",
            {
                "model_type": "qwen3_5",
                "architectures": ["Qwen3_5ForConditionalGeneration"],
                "num_hidden_layers": 64,
                "vocab_size": 248320,
                "num_local_experts": 128,
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "moe.gate_up_proj",
                        "moe.down_proj",
                        "self_attn.q_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.moe.gate_up_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.down_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.3.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq"
        )

        self.assertTrue(result["ok"], result)
        self.assertEqual(result["family"], "qwen36-27b")
        self.assertEqual(
            result["checks"]["qwen_moe_expert_quantization"]["present_modules"],
            {"moe.gate_up_proj": 1, "moe.down_proj": 1},
        )

    def test_qwen35_moe_expert_gate_runs_without_registered_variant(self) -> None:
        self._write_json(
            "config.json",
            {
                "model_type": "qwen3_5",
                "architectures": ["Qwen3_5ForConditionalGeneration"],
                "num_hidden_layers": 32,
                "vocab_size": 151936,
                "num_experts": 64,
                "quantization_config": {
                    "modules_in_block_to_quantize": ["self_attn.q_proj"]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(
            self.artifact, requested_layout="vllm-gptq"
        )

        self.assertFalse(result["ok"], result)
        self.assertEqual(result["family"], "auto")
        self.assertIn("qwen_moe_expert_quantization", result["checks"])

    def test_qwen35_metadata_without_qwen36_shape_does_not_autodetect_qwen36(
        self,
    ) -> None:
        self._write_json(
            "config.json",
            {
                "model_type": "qwen3_5",
                "architectures": ["Qwen3_5ForConditionalGeneration"],
                "num_hidden_layers": 32,
                "vocab_size": 151936,
                "quantization_config": {
                    "modules_in_block_to_quantize": [
                        "self_attn.q_proj",
                        "mlp.down_proj",
                    ]
                },
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(self.artifact, requested_layout="auto")

        self.assertTrue(result["ok"], result)
        self.assertEqual(result["family"], "auto")
        self.assertNotIn("gdn_gptq_policy", result["checks"])

    def test_required_and_forbidden_quantized_modules(self) -> None:
        self._seed_base_config(
            quantization_config={
                "modules_in_block_to_quantize": [
                    "moe.gate_up_proj",
                    "moe.down_proj",
                    "self_attn.q_proj",
                ]
            }
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.moe.gate_up_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.1.moe.gate_up_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.moe.down_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.self_attn.q_proj.qweight": "model-00001-of-00001.safetensors",
                    "model.layers.0.self_attn.q_proj.scales": "model-00001-of-00001.safetensors",
                    "model.layers.0.self_attn.q_proj.qzeros": "model-00001-of-00001.safetensors",
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(
            self.artifact,
            requested_layout="vllm-gptq",
            required_quantized_modules=["moe.gate_up_proj", "self_attn.q_proj"],
            min_quantized_modules=3,
        )
        self.assertTrue(result["ok"], result)
        self.assertEqual(
            result["checks"]["quantized_module_counts"],
            {
                "moe.down_proj": 1,
                "moe.gate_up_proj": 2,
                "self_attn.q_proj": 1,
            },
        )

        forbidden = validator.validate_artifact(
            self.artifact,
            requested_layout="vllm-gptq",
            forbidden_quantized_modules=["self_attn.q_proj"],
        )
        self.assertFalse(forbidden["ok"])
        self.assertTrue(
            any("forbidden quantized module" in error for error in forbidden["errors"]),
            forbidden["errors"],
        )

        missing = validator.validate_artifact(
            self.artifact,
            requested_layout="vllm-gptq",
            required_quantized_modules=["mlp.down_proj"],
        )
        self.assertFalse(missing["ok"])
        self.assertTrue(
            any("required quantized module" in error for error in missing["errors"]),
            missing["errors"],
        )

    def test_flat_modules_rejected_for_hf_layout(self) -> None:
        self._seed_base_config(
            quantization_config={"modules_in_block_to_quantize": ["self_attn.q_proj"]}
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.self_attn.q_proj.weight": "model-00001-of-00001.safetensors"
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(
            self.artifact, requested_layout="hf-native"
        )
        self.assertFalse(result["ok"])
        self.assertTrue(
            any(
                "only accepted for layout=vllm-gptq" in error
                for error in result["errors"]
            ),
            result["errors"],
        )

    def test_compressed_tensors_layout_detected(self) -> None:
        self._seed_base_config(
            model_name="google/gemma-4-31b-it",
            quantization_config={
                "format": "compressed-tensors",
                "modules_in_block_to_quantize": [
                    ["self_attn.q_proj", "self_attn.k_proj"]
                ],
            },
        )
        self._write_json(
            "model.safetensors.index.json",
            {
                "weight_map": {
                    "model.layers.0.self_attn.q_proj.weight": "model-00001-of-00001.safetensors"
                }
            },
        )
        self._touch("model-00001-of-00001.safetensors")

        result = validator.validate_artifact(self.artifact, requested_layout="auto")
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["layout"], "compressed-tensors")
        self.assertEqual(result["family"], "gemma4-31b")

    def test_single_file_safetensors_fallback(self) -> None:
        self._seed_base_config(
            quantization_config={
                "modules_in_block_to_quantize": [
                    ["self_attn.q_proj", "self_attn.k_proj"]
                ]
            }
        )
        self._touch("model.safetensors")
        result = validator.validate_artifact(
            self.artifact, requested_layout="hf-native"
        )
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["checks"]["shard_mode"], "single")


if __name__ == "__main__":
    unittest.main()
