#!/usr/bin/env python3
"""Unit contract for grafting a native Qwen3.5 MTP head onto GPTQ."""

from __future__ import annotations

import importlib.util
import json
import os
import struct
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("graft_qwen35_mtp.py")
SPEC = importlib.util.spec_from_file_location("graft_qwen35_mtp", SCRIPT)
assert SPEC and SPEC.loader
graft_mod = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(graft_mod)


def write_safetensors(path: Path, keys: list[str]) -> None:
    offset = 0
    header: dict[str, object] = {"__metadata__": {"format": "pt"}}
    payload = bytearray()
    for index, key in enumerate(keys):
        raw = struct.pack("<H", index)
        header[key] = {
            "dtype": "BF16",
            "shape": [1],
            "data_offsets": [offset, offset + len(raw)],
        }
        payload.extend(raw)
        offset += len(raw)
    encoded = json.dumps(header, separators=(",", ":")).encode()
    encoded += b" " * ((-len(encoded)) % 8)
    path.write_bytes(struct.pack("<Q", len(encoded)) + encoded + payload)


class GraftQwen35MTPTest(unittest.TestCase):
    def build_target(self, root: Path) -> Path:
        target = root / "target"
        target.mkdir()
        shard = target / "model-00001-of-00001.safetensors"
        shard.write_bytes(b"target-weights")
        index = {
            "metadata": {"total_size": len(b"target-weights")},
            "weight_map": {"model.layers.0.weight": shard.name},
        }
        (target / "model.safetensors.index.json").write_text(json.dumps(index))
        (target / "config.json").write_text(
            json.dumps(
                {
                    "architectures": ["Qwen3_5ForCausalLM"],
                    "model_type": "qwen3_5_text",
                    "hidden_size": 8,
                }
            )
        )
        (target / "quantize_config.json").write_text(
            json.dumps({"bits": 4, "group_size": 128})
        )
        return target

    def test_graft_is_atomic_complete_and_preserves_target(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = self.build_target(root)
            mtp = root / "mtp.safetensors"
            write_safetensors(mtp, sorted(graft_mod.EXPECTED_MTP_KEYS))
            output = root / "target-with-mtp"

            marker = graft_mod.graft(
                target,
                mtp,
                output,
                source_artifact_digest="sha256:" + "1" * 64,
            )

            index = json.loads(
                (output / "model.safetensors.index.json").read_text()
            )
            self.assertEqual(
                {key for key in index["weight_map"] if key.startswith("mtp.")},
                {f"mtp.{key}" for key in graft_mod.EXPECTED_MTP_KEYS},
            )
            header, _ = graft_mod.read_safetensors_header(
                output / graft_mod.OUTPUT_SHARD
            )
            self.assertEqual(
                {key for key in header if key != "__metadata__"},
                {f"mtp.{key}" for key in graft_mod.EXPECTED_MTP_KEYS},
            )
            config = json.loads((output / "config.json").read_text())
            self.assertEqual(config["mtp_num_hidden_layers"], 1)
            self.assertFalse(config["mtp_use_dedicated_embeddings"])
            self.assertEqual(marker["mtp_tensor_count"], 15)
            self.assertTrue((output / graft_mod.MARKER_NAME).is_file())
            self.assertEqual(
                os.stat(target / "model-00001-of-00001.safetensors").st_ino,
                os.stat(output / "model-00001-of-00001.safetensors").st_ino,
            )
            source_index = json.loads(
                (target / "model.safetensors.index.json").read_text()
            )
            self.assertFalse(
                any(key.startswith("mtp.") for key in source_index["weight_map"])
            )

    def test_rejects_incomplete_mtp_head(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = self.build_target(root)
            mtp = root / "mtp.safetensors"
            keys = sorted(graft_mod.EXPECTED_MTP_KEYS)[:-1]
            write_safetensors(mtp, keys)
            with self.assertRaisesRegex(ValueError, "MTP tensor contract mismatch"):
                graft_mod.graft(
                    target,
                    mtp,
                    root / "output",
                    source_artifact_digest="sha256:" + "2" * 64,
                )

    def test_rejects_output_shard_collision_without_mutating_source(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = self.build_target(root)
            collision = target / graft_mod.OUTPUT_SHARD
            collision.write_bytes(b"do-not-truncate")
            source_mtp = root / "source-mtp.safetensors"
            write_safetensors(source_mtp, sorted(graft_mod.EXPECTED_MTP_KEYS))

            with self.assertRaisesRegex(ValueError, "already contains mtp.safetensors"):
                graft_mod.graft(
                    target,
                    source_mtp,
                    root / "output",
                    source_artifact_digest="sha256:" + "3" * 64,
                )

            self.assertEqual(collision.read_bytes(), b"do-not-truncate")
            self.assertFalse((root / "output").exists())


if __name__ == "__main__":
    unittest.main()
