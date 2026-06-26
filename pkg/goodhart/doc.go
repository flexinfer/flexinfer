// Package goodhart detects overoptimization — a proxy metric improving while
// the true objective silently regresses (Goodhart's Law). It is a Go port of
// the detector ideas in the RewardSpy project (https://github.com/AvAdiii/rewardspy),
// a debugger for RL reward functions, retargeted from reward functions onto
// FlexInfer's serving-config tuning.
//
// FlexInfer has no RL training, but it has the same failure class: the autotune
// loop (agents/autotune) accepts a config purely on aggregate tok/s, so a config
// that games the throughput proxy can ship while the real objective craters. The
// 2026-06-26 kill-test proved this live — enabling n-gram speculative decoding
// lifted the aggregate proxy +26.7% while long-form decode throughput fell 47.6%
// (.loom/killtest-autotune-goodhart-2026-06-26.md). n-gram SD is lossless, so the
// harmed objective is workload-stratified throughput, not generation quality.
//
// # Detectors
//
// Two flavors, both pure and deterministic (no I/O, no clock, no randomness —
// the same discipline as pkg/gauntlet's Evaluate):
//
//   - Comparator: WorkloadRegression — the autotune guard's primary detector.
//     Compares a candidate config's per-workload-class throughput to a baseline
//     and trips when any protected class regresses beyond tolerance, even if the
//     aggregate improved. This is the n-gram-SD pattern.
//   - Online (RewardSpy ports): VarianceCollapse, CUSUM (slope-break /
//     change-point), CeilingSaturation, ComponentDominance, LengthDrift, and
//     Degeneracy. O(1) rolling state over a stream of observations.
//
// Every detector returns a Finding{Detector, Tripped, Reason, Value}; Aggregate
// composes Findings into a Verdict, mirroring gauntlet.CheckResult/Verdict.
//
// # Consumption modes
//
//  1. Autotune veto — feed a candidate's per-class throughput to WorkloadRegression
//     so a Goodhart-gaming config is rejected (the reason this package exists).
//  2. Eval-trend monitoring — stream eval/quant JSONL (per-layer cosine, perplexity,
//     reward components) through the online detectors to catch drift across runs.
//  3. CI audit — run a stream offline and exit non-zero if any detector trips.
package goodhart
