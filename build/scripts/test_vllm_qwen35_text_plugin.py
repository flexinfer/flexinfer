#!/usr/bin/env python3
"""Unit contract for the Qwen3.5 text-only vLLM plugin."""

from __future__ import annotations

import importlib.util
import sys
import types
import unittest
from pathlib import Path


PLUGIN = (
    Path(__file__).parents[1]
    / "vllm-qwen35-text-plugin"
    / "src"
    / "flexinfer_vllm_qwen35_text"
    / "__init__.py"
)


class FakeRegistry:
    def __init__(self) -> None:
        self.models: dict[str, type] = {}

    def register_model(self, architecture: str, model_class: type) -> None:
        self.models[architecture] = model_class


class FakeConfig:
    def __init__(self, bk: int, warps: int, stages: int) -> None:
        self.kwargs = {"BK": bk}
        self.num_warps = warps
        self.num_stages = stages


class FakeKernel:
    fn = types.SimpleNamespace(
        configs=[
            FakeConfig(32, 2, 2),
            FakeConfig(32, 2, 3),
            FakeConfig(32, 2, 4),
        ]
    )


class LinearBase:
    pass


class RoutedExperts:
    def __init__(self) -> None:
        self.moe_config = "moe-config"


class UnquantizedLinearMethod:
    pass


class UnquantizedFusedMoEMethod:
    def __init__(self, moe_config) -> None:
        self.moe_config = moe_config


class AutoGPTQConfig:
    def get_quant_method(self, layer, prefix):
        return ("quantized", layer, prefix)


class QuantizedMTPAutoGPTQConfig:
    def get_quant_method(self, layer, prefix):
        return ("quantized-mtp", layer, prefix)


class Qwen3_5MoeForCausalLM:
    def load_weights(self, weights):
        self.loaded_weights = list(weights)
        return {name for name, _ in self.loaded_weights}


class Qwen3_5Model:
    def load_fused_expert_weights(
        self, name, params_dict, loaded_weight, shard_id, num_experts
    ):
        self.loaded_expert = (name, loaded_weight, shard_id, num_experts)
        return name in params_dict


class Qwen3_5MoeMTP(Qwen3_5Model):
    pass


class Qwen3_5ForConditionalGeneration:
    @classmethod
    def get_mamba_state_shape_from_config(cls, config):
        return (cls, config)

    @classmethod
    def get_mamba_state_dtype_from_config(cls, config):
        return (cls, config)

    @classmethod
    def get_mamba_state_copy_func(cls):
        return cls


class SpeculativeConfig:
    @staticmethod
    def hf_config_override(config):
        config.delegated = True
        return config


class FakeHFConfig:
    def __init__(self, model_type: str) -> None:
        self.model_type = model_type
        self.architectures = ["OriginalArchitecture"]
        self.mtp_num_hidden_layers = 1

    def update(self, values: dict) -> None:
        for name, value in values.items():
            setattr(self, name, value)


class PluginTest(unittest.TestCase):
    def test_registers_text_architecture_with_hybrid_contract(self) -> None:
        registry = FakeRegistry()
        vllm = types.ModuleType("vllm")
        vllm.ModelRegistry = registry
        models = types.ModuleType("vllm.model_executor.models.qwen3_5")
        models.Qwen3_5MoeForCausalLM = Qwen3_5MoeForCausalLM
        models.Qwen3_5ForConditionalGeneration = Qwen3_5ForConditionalGeneration
        models.Qwen3_5Model = Qwen3_5Model
        mtp_models = types.ModuleType("vllm.model_executor.models.qwen3_5_mtp")
        mtp_models.Qwen3_5MoeMTP = Qwen3_5MoeMTP
        config_package = types.ModuleType("vllm.config")
        speculative = types.ModuleType("vllm.config.speculative")
        speculative.SpeculativeConfig = SpeculativeConfig

        torch = types.ModuleType("torch")
        torch.version = types.SimpleNamespace(hip="7.2")
        fla = types.ModuleType(
            "vllm.model_executor.layers.fla.ops.chunk_scaled_dot_kkt"
        )
        fla.chunk_scaled_dot_kkt_fwd_kernel = FakeKernel
        auto_gptq = types.ModuleType(
            "vllm.model_executor.layers.quantization.auto_gptq"
        )
        auto_gptq.AutoGPTQConfig = AutoGPTQConfig
        fused_moe = types.ModuleType("vllm.model_executor.layers.fused_moe")
        fused_moe.RoutedExperts = RoutedExperts
        fused_moe.UnquantizedFusedMoEMethod = UnquantizedFusedMoEMethod
        linear = types.ModuleType("vllm.model_executor.layers.linear")
        linear.LinearBase = LinearBase
        linear.UnquantizedLinearMethod = UnquantizedLinearMethod

        fake_modules = {
            "torch": torch,
            "vllm": vllm,
            config_package.__name__: config_package,
            speculative.__name__: speculative,
            models.__name__: models,
            mtp_models.__name__: mtp_models,
            fla.__name__: fla,
            auto_gptq.__name__: auto_gptq,
            fused_moe.__name__: fused_moe,
            linear.__name__: linear,
        }
        saved = {name: sys.modules.get(name) for name in fake_modules}
        sys.modules.update(fake_modules)
        try:
            spec = importlib.util.spec_from_file_location("qwen35_text_plugin", PLUGIN)
            assert spec and spec.loader
            plugin = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(plugin)
            plugin.register()
        finally:
            for name, module in saved.items():
                if module is None:
                    sys.modules.pop(name, None)
                else:
                    sys.modules[name] = module

        model_class = registry.models["Qwen3_5MoeForCausalLM"]
        self.assertTrue(model_class.is_hybrid)
        self.assertEqual(
            model_class.get_mamba_state_shape_from_config("cfg"),
            (model_class, "cfg"),
        )
        self.assertIs(model_class.get_mamba_state_copy_func(), model_class)
        self.assertEqual(len(FakeKernel.fn.configs), 1)
        self.assertEqual(FakeKernel.fn.configs[0].num_stages, 2)
        instance = model_class()
        instance.load_weights(
            [
                ("model.layers.0.moe.gate_up_proj.qweight", 1),
                ("model.layers.0.moe.down_proj.scales", 2),
            ]
        )
        self.assertEqual(
            [name for name, _ in instance.loaded_weights],
            [
                "model.layers.0.mlp.experts.gate_up_proj.qweight",
                "model.layers.0.mlp.experts.down_proj.scales",
            ],
        )

        qwen_model = Qwen3_5Model()
        self.assertTrue(
            qwen_model.load_fused_expert_weights(
                "model.layers.0.mlp.experts.w2_weight.qweight",
                {"model.layers.0.mlp.experts.w2_qweight": object()},
                "tensor",
                "w2",
                256,
            )
        )
        self.assertEqual(
            qwen_model.loaded_expert[0],
            "model.layers.0.mlp.experts.w2_qweight",
        )
        mtp_model = Qwen3_5MoeMTP()
        self.assertTrue(
            mtp_model.load_fused_expert_weights(
                "mtp.layers.0.mlp.experts.w13_weight.scales",
                {"mtp.layers.0.mlp.experts.w13_scales": object()},
                "tensor",
                "w13",
                256,
            )
        )
        self.assertEqual(
            mtp_model.loaded_expert[0],
            "mtp.layers.0.mlp.experts.w13_scales",
        )

        mtp_config = SpeculativeConfig.hf_config_override(
            FakeHFConfig("qwen3_5_moe_text")
        )
        self.assertEqual(mtp_config.model_type, "qwen3_5_mtp")
        self.assertEqual(mtp_config.n_predict, 1)
        self.assertEqual(mtp_config.architectures, ["Qwen3_5MoeMTP"])

        delegated = SpeculativeConfig.hf_config_override(FakeHFConfig("other"))
        self.assertTrue(delegated.delegated)

        quant_config = AutoGPTQConfig()
        mtp_linear = quant_config.get_quant_method(LinearBase(), "mtp.fc")
        self.assertIsInstance(mtp_linear, UnquantizedLinearMethod)
        mtp_experts = quant_config.get_quant_method(
            RoutedExperts(), "mtp.layers.0.mlp.experts"
        )
        self.assertIsInstance(mtp_experts, UnquantizedFusedMoEMethod)
        self.assertEqual(mtp_experts.moe_config, "moe-config")
        target_linear = LinearBase()
        self.assertEqual(
            quant_config.get_quant_method(target_linear, "model.layers.0.mlp"),
            ("quantized", target_linear, "model.layers.0.mlp"),
        )

        plugin._patch_qwen35_mtp_gptq(
            QuantizedMTPAutoGPTQConfig,
            LinearBase,
            RoutedExperts,
            UnquantizedLinearMethod,
            UnquantizedFusedMoEMethod,
            quantized_mtp_experts=True,
        )
        quantized_mtp_config = QuantizedMTPAutoGPTQConfig()
        quantized_mtp_linear = quantized_mtp_config.get_quant_method(
            LinearBase(), "mtp.fc"
        )
        self.assertIsInstance(quantized_mtp_linear, UnquantizedLinearMethod)
        quantized_mtp_experts = RoutedExperts()
        self.assertEqual(
            quantized_mtp_config.get_quant_method(
                quantized_mtp_experts, "mtp.layers.0.mlp.experts"
            ),
            (
                "quantized-mtp",
                quantized_mtp_experts,
                "mtp.layers.0.mlp.experts",
            ),
        )


if __name__ == "__main__":
    unittest.main()
