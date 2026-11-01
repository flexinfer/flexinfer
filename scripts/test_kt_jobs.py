"""KT-JOBS harness: verdict arithmetic over a synthetic run."""

import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("ktjobs", Path(__file__).with_name("kt-jobs.py"))
ktjobs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ktjobs)

T0 = 1_000_000.0  # jobs submitted


def job(i, status="completed", done=T0 + 200):
    return {
        "id": f"job-{i}", "status": status, "started_at": T0 + 10, "completed_at": done,
        "observed_at": done + 2, "flexinfer": {"lane": "qwen-image-21-gfx1100"},
    }


def passing_run():
    ids = [f"job-{i}" for i in range(4)]
    return {
        "args": {"cooldown": 300},
        "jobs_submitted_at": T0,
        "job_ids": ids,
        "jobs": {i: job(n) for n, i in enumerate(ids)},
        "contents": {i: {"status": 200, "bytes": 1000} for i in ids},
        "last_job_done_at": T0 + 202,
        "calls": [{"kind": "submit", "status": 200, "seconds": 0.2}, {"kind": "chat", "status": 200, "seconds": 3.0}],
        "envelope_misses": [],
        "hooks": [{"t": T0 + 203, "job": i, "event": "job.completed", "signature_ok": True} for i in ids],
        # 1.0 s TTFT before submission, 1.1 s until the flip at T0 + 60, slow after.
        "chat": [{"t": T0 - 100 + k, "ttft": 1.0, "status": 200} for k in range(20)]
        + [{"t": T0 + k, "ttft": 1.1, "status": 200} for k in range(0, 60, 3)]
        + [{"t": T0 + 70 + k, "ttft": 9.0, "status": 200} for k in range(10)],
        "warm_timeline": [(T0 - 10, "Ready", None), (T0 + 60, "Preempted", "Preempted"),
                          (T0 + 400, "Ready", None), (T0 + 450, "Preempted", None), (T0 + 600, "Ready", None)],
        "cancel": {"cancelled_at": T0 + 450, "final": {"status": "cancelled"}},
        "cold_probe": {"status": 503, "error": {"code": "model_cold_starting"}},
    }


class Verdict(unittest.TestCase):
    def test_pass(self):
        ok, checks, detail = ktjobs.verdict(passing_run(), 300)
        self.assertTrue(ok, checks)
        self.assertEqual(detail["flip_after_submit_s"], 60)
        self.assertAlmostEqual(detail["warm_back_after_last_job_s"], 198)

    def test_ttft_after_the_flip_does_not_count(self):
        r = passing_run()
        ok, checks, _ = ktjobs.verdict(r, 300)
        self.assertTrue(checks["chat_ttft_p95_within_20pct_until_flip"])

    def test_each_rule_fails_alone(self):
        cases = {
            "all_jobs_complete_with_output": lambda r: r["jobs"]["job-1"].update(status="failed"),
            "no_connection_over_30s": lambda r: r["calls"].append({"kind": "poll", "status": 200, "seconds": 31}),
            "zero_504_524": lambda r: r["calls"].append({"kind": "chat", "status": 504, "seconds": 1}),
            "chat_ttft_p95_within_20pct_until_flip": lambda r: [s.update(ttft=1.5) for s in r["chat"] if T0 <= s["t"] < T0 + 60],
            "warm_back_within_5min": lambda r: r.update(warm_timeline=[(T0 - 10, "Ready", None), (T0 + 60, "Preempted", None)]),
            "webhooks_3_of_4_within_10s": lambda r: r.update(hooks=r["hooks"][:2]),
            "cancel_frees_card_within_cooldown": lambda r: r["cancel"].update(final={"status": "completed"}),
            "typed_envelope_on_every_503": lambda r: r["envelope_misses"].append({"kind": "chat"}),
            "cold_probe_typed": lambda r: r.update(cold_probe={"status": 503, "error": {"code": "service_unavailable"}}),
        }
        for name, breaks in cases.items():
            r = copy.deepcopy(passing_run())
            breaks(r)
            ok, checks, _ = ktjobs.verdict(r, 300)
            self.assertFalse(ok, name)
            self.assertFalse(checks[name], name)

    def test_p95(self):
        self.assertIsNone(ktjobs.p95([None]))
        self.assertEqual(ktjobs.p95(list(range(1, 101))), 95)


if __name__ == "__main__":
    unittest.main()
