from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

import gemma4_validate_artifact as validator


class Gemma4ValidateArtifactTests(unittest.TestCase):
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

    def _seed_base_config(
        self,
        model_name: str = "google/gemma-4-26b-a4b-it",
        quantization_config: dict | None = None,
    ) -> None:
        config = {"_name_or_path": model_name}
        if quantization_config is not None:
            config["quantization_config"] = quantization_config
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
        result = validator.validate_artifact(self.artifact, requested_layout="vllm-gptq")
        self.assertFalse(result["ok"])
        self.assertTrue(any("missing shard files" in error for error in result["errors"]))

    def test_vllm_flat_modules_warn_and_pass(self) -> None:
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
        self.assertTrue(
            any("flat string list" in warning for warning in result["warnings"]),
            result["warnings"],
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

        result = validator.validate_artifact(self.artifact, requested_layout="hf-native")
        self.assertFalse(result["ok"])
        self.assertTrue(
            any("only accepted for layout=vllm-gptq" in error for error in result["errors"]),
            result["errors"],
        )

    def test_compressed_tensors_layout_detected(self) -> None:
        self._seed_base_config(
            model_name="google/gemma-4-31b-it",
            quantization_config={
                "format": "compressed-tensors",
                "modules_in_block_to_quantize": [["self_attn.q_proj", "self_attn.k_proj"]],
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
                "modules_in_block_to_quantize": [["self_attn.q_proj", "self_attn.k_proj"]]
            }
        )
        self._touch("model.safetensors")
        result = validator.validate_artifact(self.artifact, requested_layout="hf-native")
        self.assertTrue(result["ok"], result)
        self.assertEqual(result["checks"]["shard_mode"], "single")


if __name__ == "__main__":
    unittest.main()
