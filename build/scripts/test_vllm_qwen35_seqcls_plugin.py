#!/usr/bin/env python3
"""Unit contract for the Qwen3.5 sequence-classification vLLM plugin.

GPU-free and vLLM-free: the plugin does all vLLM imports inside ``register()``,
so this loads it against fake modules. Mirrors
``test_vllm_qwen35_text_plugin.py``.
"""

from __future__ import annotations

import importlib.util
import os
import sys
import types
import unittest
from pathlib import Path


PLUGIN = (
    Path(__file__).parents[1]
    / "vllm-qwen35-seqcls-plugin"
    / "src"
    / "flexinfer_vllm_qwen35_seqcls"
    / "__init__.py"
)
SIBLING = (
    Path(__file__).parents[1]
    / "vllm-qwen35-text-plugin"
    / "src"
    / "flexinfer_vllm_qwen35_text"
    / "__init__.py"
)
COMMON_SRC = Path(__file__).parents[1] / "vllm-plugin-common" / "src"

# The plugins import the shared package at module scope; in CI it is installed,
# and here we point at the source tree so the test needs no install step.
if str(COMMON_SRC) not in sys.path:
    sys.path.insert(0, str(COMMON_SRC))


class FakeRegistry:
    def __init__(self) -> None:
        self.models: dict[str, type] = {}

    def register_model(self, architecture: str, model_class: type) -> None:
        if architecture in self.models:
            raise AssertionError(f"duplicate registration for {architecture}")
        self.models[architecture] = model_class


class FakeKernel:
    """A loaded FLA autotuner with several candidate configs."""

    class _Cfg:
        def __init__(self, stages: int, warps: int) -> None:
            self.num_stages = stages
            self.num_warps = warps

    fn = types.SimpleNamespace(configs=[_Cfg(4, 8), _Cfg(1, 2), _Cfg(2, 4)])


class FakeWeightsMapper:
    def __init__(self, orig_to_new_prefix=None) -> None:
        self.orig_to_new_prefix = orig_to_new_prefix or {}


class FakeAutoWeightsLoader:
    last: dict = {}

    def __init__(self, model, skip_prefixes=None) -> None:
        self.model = model
        FakeAutoWeightsLoader.last = {"skip_prefixes": list(skip_prefixes or [])}

    def load_weights(self, weights, mapper=None):
        FakeAutoWeightsLoader.last["mapper"] = mapper
        # Drain so the score-weight tap observes the checkpoint.
        return [name for name, _ in weights]


class FakeScore:
    def __init__(self, rows: int) -> None:
        self.output_size = rows


class _Base:
    """Stand-in for a generative Qwen3.5 class."""

    def __init__(self, *, vllm_config, prefix: str = "", **kwargs) -> None:
        self.vllm_config = vllm_config
        self.prefix = prefix
        rows = vllm_config.model_config.hf_config.get_text_config().num_labels
        self.score = FakeScore(rows)

    def load_weights(self, weights):
        return list(weights)


class Qwen3_5ForCausalLM(_Base):
    pass


class Qwen3_5MoeForCausalLM(_Base):
    pass


class Qwen3_5ForConditionalGeneration:
    @classmethod
    def get_mamba_state_shape_from_config(cls, config):
        return ("shape",)

    @classmethod
    def get_mamba_state_dtype_from_config(cls, config):
        return ("dtype",)

    @classmethod
    def get_mamba_state_copy_func(cls):
        return "copy"


def _as_seq_cls_model(base):
    """Stand-in for vLLM's adapter: marks the class as a pooling model."""

    class _SeqCls(base):
        is_pooling_model = True

    _SeqCls.__name__ = f"{base.__name__}SeqCls"
    return _SeqCls


class FakeTextConfig:
    def __init__(self, num_labels: int = 2) -> None:
        self.num_labels = num_labels


class FakeHFConfig:
    """Composite config: labels declared at the top level only."""

    def __init__(self, top_labels: int, text_labels: int = 2) -> None:
        self.num_labels = top_labels
        self._text = FakeTextConfig(text_labels)

    def get_text_config(self):
        return self._text


def _load_plugin():
    spec = importlib.util.spec_from_file_location("qwen35_seqcls_plugin", PLUGIN)
    assert spec and spec.loader
    plugin = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(plugin)
    return plugin


def _register():
    """Load the plugin against a fake vLLM and run register()."""
    registry = FakeRegistry()
    vllm = types.ModuleType("vllm")
    vllm.ModelRegistry = registry
    models = types.ModuleType("vllm.model_executor.models.qwen3_5")
    models.Qwen3_5ForCausalLM = Qwen3_5ForCausalLM
    models.Qwen3_5MoeForCausalLM = Qwen3_5MoeForCausalLM
    models.Qwen3_5ForConditionalGeneration = Qwen3_5ForConditionalGeneration
    adapters = types.ModuleType("vllm.model_executor.models.adapters")
    adapters.as_seq_cls_model = _as_seq_cls_model
    utils = types.ModuleType("vllm.model_executor.models.utils")
    utils.AutoWeightsLoader = FakeAutoWeightsLoader
    utils.WeightsMapper = FakeWeightsMapper
    torch = types.ModuleType("torch")
    torch.version = types.SimpleNamespace(hip="7.2")
    fla = types.ModuleType("vllm.model_executor.layers.fla.ops.chunk_scaled_dot_kkt")
    fla.chunk_scaled_dot_kkt_fwd_kernel = FakeKernel
    fake_modules = {
        "torch": torch,
        "vllm": vllm,
        models.__name__: models,
        adapters.__name__: adapters,
        utils.__name__: utils,
        fla.__name__: fla,
    }
    saved = {name: sys.modules.get(name) for name in fake_modules}
    sys.modules.update(fake_modules)
    old_arch = os.environ.get("PYTORCH_ROCM_ARCH")
    os.environ["PYTORCH_ROCM_ARCH"] = "gfx1100"
    try:
        plugin = _load_plugin()
        plugin.register()
    finally:
        if old_arch is None:
            os.environ.pop("PYTORCH_ROCM_ARCH", None)
        else:
            os.environ["PYTORCH_ROCM_ARCH"] = old_arch
        for name, module in saved.items():
            if module is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = module
    return plugin, registry


class RegistrationTest(unittest.TestCase):
    def test_registers_both_architectures_as_pooling_models(self) -> None:
        _, registry = _register()
        self.assertEqual(
            sorted(registry.models),
            ["Qwen3_5ForSequenceClassification", "Qwen3_5MoeForSequenceClassification"],
        )
        for name, cls in registry.models.items():
            self.assertTrue(
                getattr(cls, "is_pooling_model", False),
                f"{name} must report is_pooling_model so --runner auto picks pooling",
            )

    def test_applies_hybrid_contract_idempotently(self) -> None:
        _register()
        for cls in (Qwen3_5ForCausalLM, Qwen3_5MoeForCausalLM):
            self.assertTrue(cls.is_hybrid)
            self.assertEqual(cls.get_mamba_state_copy_func(), "copy")
        # A second pass must not raise or change anything.
        _register()
        self.assertTrue(Qwen3_5ForCausalLM.is_hybrid)

    def test_prunes_rocm_fla_autotuners(self) -> None:
        FakeKernel.fn.configs = [
            FakeKernel._Cfg(4, 8),
            FakeKernel._Cfg(1, 2),
            FakeKernel._Cfg(2, 4),
        ]
        _register()
        self.assertEqual(len(FakeKernel.fn.configs), 1)
        self.assertEqual(FakeKernel.fn.configs[0].num_stages, 1)


class LabelResolutionTest(unittest.TestCase):
    def test_prefers_top_level_label_space(self) -> None:
        plugin = _load_plugin()
        cfg = FakeHFConfig(top_labels=3, text_labels=2)
        self.assertEqual(plugin.resolve_num_labels(cfg, cfg.get_text_config()), 3)

    def test_falls_back_to_text_config_when_top_level_is_default(self) -> None:
        plugin = _load_plugin()
        cfg = FakeHFConfig(top_labels=2, text_labels=5)
        self.assertEqual(plugin.resolve_num_labels(cfg, cfg.get_text_config()), 5)

    def test_non_composite_config_uses_its_own_count(self) -> None:
        plugin = _load_plugin()
        cfg = FakeHFConfig(top_labels=7)
        self.assertEqual(plugin.resolve_num_labels(cfg, cfg), 7)

    def test_repairs_text_config_and_builds_a_three_row_head(self) -> None:
        _, registry = _register()
        cls = registry.models["Qwen3_5ForSequenceClassification"]
        cfg = FakeHFConfig(top_labels=3, text_labels=2)
        vllm_config = types.SimpleNamespace(
            model_config=types.SimpleNamespace(hf_config=cfg)
        )
        model = cls(vllm_config=vllm_config)
        self.assertEqual(cfg.get_text_config().num_labels, 3)
        self.assertEqual(model.score.output_size, 3)

    def test_rejects_checkpoint_whose_score_head_disagrees(self) -> None:
        _, registry = _register()
        cls = registry.models["Qwen3_5ForSequenceClassification"]
        cfg = FakeHFConfig(top_labels=3, text_labels=2)
        model = cls(
            vllm_config=types.SimpleNamespace(
                model_config=types.SimpleNamespace(hf_config=cfg)
            )
        )
        bad = [("score.weight", types.SimpleNamespace(shape=(2, 1024)))]
        with self.assertRaises(RuntimeError) as caught:
            model.load_weights(iter(bad))
        self.assertIn("score.weight", str(caught.exception))

    def test_accepts_matching_checkpoint(self) -> None:
        _, registry = _register()
        cls = registry.models["Qwen3_5ForSequenceClassification"]
        cfg = FakeHFConfig(top_labels=3, text_labels=2)
        model = cls(
            vllm_config=types.SimpleNamespace(
                model_config=types.SimpleNamespace(hf_config=cfg)
            )
        )
        good = [("score.weight", types.SimpleNamespace(shape=(3, 1024)))]
        model.load_weights(iter(good))


class WeightMappingTest(unittest.TestCase):
    def test_remaps_language_prefix_and_skips_vision_tower(self) -> None:
        _, registry = _register()
        cls = registry.models["Qwen3_5ForSequenceClassification"]
        cfg = FakeHFConfig(top_labels=3, text_labels=2)
        model = cls(
            vllm_config=types.SimpleNamespace(
                model_config=types.SimpleNamespace(hf_config=cfg)
            )
        )
        model.load_weights(
            iter([("score.weight", types.SimpleNamespace(shape=(3, 8)))])
        )
        mapper = FakeAutoWeightsLoader.last["mapper"]
        self.assertEqual(mapper.orig_to_new_prefix, {"model.language_model.": "model."})
        self.assertEqual(
            FakeAutoWeightsLoader.last["skip_prefixes"], ["mtp.", "model.visual."]
        )


class SharedHelperTest(unittest.TestCase):
    """Both plugins must use the SAME shared helpers, not copies."""

    def test_plugins_share_one_rocm_implementation(self) -> None:
        from flexinfer_vllm_common import rocm

        plugin = _load_plugin()
        sibling_src = SIBLING.read_text()

        # The seqcls plugin routes through the shared wrapper.
        self.assertIs(plugin.prune_fla_configs_if_needed, rocm.prune_fla_configs_if_needed)
        # The text plugin imports the same module rather than redefining it.
        self.assertIn("from flexinfer_vllm_common", sibling_src)
        self.assertNotIn("def _select_rocm_safe_fla_configs", sibling_src)
        self.assertNotIn("def _needs_safe_fla_config", sibling_src)

    def test_shared_package_imports_without_vllm_or_torch(self) -> None:
        import flexinfer_vllm_common as common

        for name in (
            "apply_hybrid_state",
            "repair_text_config_labels",
            "resolve_num_labels",
            "select_rocm_safe_fla_configs",
        ):
            self.assertTrue(hasattr(common, name), f"{name} missing from the shared API")

    def test_hybrid_application_is_idempotent(self) -> None:
        from flexinfer_vllm_common import apply_hybrid_state

        class Donor:
            @classmethod
            def get_mamba_state_shape_from_config(cls, config):
                return "shape"

            @classmethod
            def get_mamba_state_dtype_from_config(cls, config):
                return "dtype"

            @classmethod
            def get_mamba_state_copy_func(cls):
                return "copy"

        class Target:
            pass

        first = apply_hybrid_state((Target,), Donor)
        second = apply_hybrid_state((Target,), Donor)
        self.assertEqual(first, 3)
        self.assertEqual(second, 0, "re-application must be a no-op")
        self.assertTrue(Target.is_hybrid)

    def test_rocm_pruner_is_skipped_off_hip(self) -> None:
        from flexinfer_vllm_common.rocm import prune_fla_configs_if_needed

        fake_torch = types.SimpleNamespace(version=types.SimpleNamespace(hip=None))
        self.assertEqual(prune_fla_configs_if_needed(fake_torch), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
