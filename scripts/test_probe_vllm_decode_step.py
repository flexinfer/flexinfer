"""The S4 decode-step probe must cut steps at the draft heads and count idle time."""

import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    "decode_step", Path(__file__).with_name("probe-vllm-decode-step.py")
)
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


def kernel(name, ts, dur):
    return {"ph": "X", "cat": "kernel", "name": name, "ts": ts, "dur": dur}


def synthetic_trace(steps=5):
    """Steps of 10 GPTQ GEMMs (1 ms each, 0.1 ms apart), a 3 ms target head,
    a 0.4 ms MTP layer, a 2.5 ms draft head, the same again, and 1 ms of host
    time before the next step. Period = 10*1.1 + 3 + 0.4 + 2.5 + 0.4 + 2.5 + 1
    + small kernels."""
    events, t = [], 0.0
    # A partial leading step so the first boundary is not the first kernel.
    events.append(
        kernel("void wvSplitK_hf_big_<__half, 32, 2, 16, 8, 2, 1>(int)", t, 2500)
    )
    t += 2500 + 1000
    for _ in range(steps):
        for _ in range(10):
            events.append(kernel("void gemm_q4_kernel_rdna3<__half, 2>(...)", t, 1000))
            t += 1100
        events.append(
            kernel("void wvSplitK_hf_big_<__half, 32, 2, 16, 8, 2, 3>(int)", t, 3000)
        )
        t += 3000
        events.append(kernel("_segmented_attention_stage", t, 300))
        t += 400
        events.append(
            kernel("void wvSplitK_hf_big_<__half, 32, 2, 16, 8, 2, 1>(int)", t, 2500)
        )
        t += 2500
        events.append(kernel("triton_red_fused_argmax_0", t, 100))
        t += 400
        events.append(
            kernel("void wvSplitK_hf_big_<__half, 32, 2, 16, 8, 2, 1>(int)", t, 2500)
        )
        t += 2500
        events.append(kernel("triton_red_fused_argmax_0", t, 100))
        t += 1000
    # The next step's body, so the last full step has a closing boundary.
    events.append(kernel("void gemm_q4_kernel_rdna3<__half, 2>(...)", t, 1000))
    # Host-side noise must be ignored.
    events.append(
        {"ph": "X", "cat": "cpu_op", "name": "aten::copy_", "ts": 10.0, "dur": 50000}
    )
    return events


class Analyze(unittest.TestCase):
    def test_cuts_steps_and_assigns_head_roles(self):
        result = probe.analyze_events(synthetic_trace(steps=5))
        self.assertEqual(result["steps"], 5)
        self.assertAlmostEqual(result["target_lm_head_ms"], 3.0)
        self.assertAlmostEqual(result["draft_lm_heads_ms"], 5.0)
        self.assertEqual(result["by_class_ms"]["gptq_gemm"], 10.0)
        self.assertEqual(result["head_samples"]["lm_head_target"][0]["n"], 3)
        self.assertEqual(
            [h["n"] for h in result["head_samples"]["lm_head_draft"]], [1, 1]
        )

    def test_idle_is_period_minus_union_of_kernels(self):
        result = probe.analyze_events(synthetic_trace(steps=4))
        # Busy: 10 + 3 + 0.3 + 2.5 + 0.1 + 2.5 + 0.1 = 18.5 ms.
        self.assertAlmostEqual(result["median_gpu_busy_ms"], 18.5)
        # Period: 10*1.1 + 3 + 0.4 + 2.5 + 0.4 + 2.5 + 1.0 = 20.8 ms.
        self.assertAlmostEqual(result["median_period_ms"], 20.8)
        self.assertAlmostEqual(result["median_idle_ms"], 2.3)
        self.assertAlmostEqual(
            result["draft_lm_heads_pct_of_busy"], round(100 * 5 / 18.5, 2)
        )

    def test_idle_is_charged_to_the_kernel_that_waited(self):
        result = probe.analyze_events(synthetic_trace(steps=4))
        charged = {name: ms for name, ms, _ in result["idle_before_kernel_per_step"]}
        # Steps 2-4: 0.9 ms of host time before the first GEMM and 0.1 ms
        # before each of the other nine; step 1 starts with 1.0 ms instead.
        self.assertAlmostEqual(charged["void gemm_q4_kernel_rdna3<__half, 2>(...)"], 1.825)
        # 0.1 ms before the first draft head, 0.3 ms before the second.
        self.assertAlmostEqual(charged["void wvSplitK_hf_big_<__half, 32, 2, 16, 8, 2, 1>(int)"], 0.4)
        self.assertAlmostEqual(charged["void wvSplitK_hf_big_<__half, 32, 2, 16, 8, 2, 3>(int)"], 0.1)
        total = sum(ms for _, ms, _ in result["idle_before_kernel_per_step"])
        # Mean idle over the four steps: (2.4 + 3 * 2.3) / 4.
        self.assertAlmostEqual(total, 2.325)

    def test_no_kernels_is_reported_not_raised(self):
        result = probe.analyze_events(
            [{"ph": "X", "cat": "cpu_op", "name": "x", "ts": 0, "dur": 5}]
        )
        self.assertEqual(result["kernel_count"], 0)

    def test_overlapping_kernels_count_once(self):
        self.assertEqual(probe.merged_busy([(0, 10), (5, 15), (20, 25)]), 20)


class Plan(unittest.TestCase):
    def test_jit_sweep_is_exactly_100_requests_with_pairs(self):
        plan = probe.jit_sweep_plan()
        self.assertEqual(sum(len(slot) for slot in plan), 100)
        self.assertTrue(any(len(slot) == 2 for slot in plan))
        sizes = {size for slot in plan for size in slot}
        self.assertEqual(sizes, set(probe.JIT_SIZES))

    def test_plan_is_deterministic(self):
        self.assertEqual(probe.jit_sweep_plan(), probe.jit_sweep_plan())


class Names(unittest.TestCase):
    def test_template_n(self):
        self.assertEqual(
            probe.template_n("void wvSplitK_hf_<__half, 32, 1, 16, 8, 4, 5>(int)"), 5
        )
        self.assertIsNone(probe.template_n("Cijk_Alik_Bljk_HHS_BH"))

    def test_classify(self):
        self.assertEqual(
            probe.classify("void gemm_q4_kernel_rdna3<__half, 4>"), "gptq_gemm"
        )
        self.assertEqual(probe.classify("_segmented_attention_reduce"), "attention")
        self.assertEqual(
            probe.classify("fused_sigmoid_gating_delta_rule_update_kernel"), "gdn"
        )
        self.assertEqual(probe.classify("triton_poi_fused_add_mul_0"), "elementwise")


if __name__ == "__main__":
    unittest.main()
