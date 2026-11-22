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

    # Owner decision 2026-11-20: bar (2) does not score chat requests waiting
    # for the warm lane's own return after the last job. In passing_run the
    # last job completes at T0 + 200 and the warm lane is next Ready at
    # T0 + 400, so with the 20 s preempted wait the window is [T0 + 179,
    # T0 + 400). A call's t is its end; it is scored by its start.
    def held_chat(self, r, start, seconds=100.0, kind="chat"):
        r["calls"].append({"t": start + seconds, "kind": kind, "status": 200, "seconds": seconds})

    def test_reload_window_excludes_chat_waiting_for_the_warm_lane(self):
        for start in (T0 + 179, T0 + 200, T0 + 399):
            r = passing_run()
            self.held_chat(r, start)
            ok, checks, detail = ktjobs.verdict(r, 300)
            self.assertTrue(checks["no_connection_over_30s"], start - T0)
            self.assertTrue(ok, checks)
            self.assertEqual(detail["reload_excluded_over_30s"], 1)
            self.assertEqual(detail["reload_window_s"], [179.0, 400.0])

    def test_reload_window_scores_everything_else(self):
        cases = {
            "chat held before the hand-back": dict(start=T0 + 178),
            "chat held after the warm lane is Ready": dict(start=T0 + 400),
            "a job call held inside the window": dict(start=T0 + 250, kind="poll"),
        }
        for name, kw in cases.items():
            r = passing_run()
            self.held_chat(r, **kw)
            ok, checks, detail = ktjobs.verdict(r, 300)
            self.assertFalse(checks["no_connection_over_30s"], name)
            self.assertEqual(detail["connections_over_30s"], 1, name)

    def test_reload_window_follows_the_preempted_wait(self):
        r = passing_run()
        r["args"]["preempted_wait"] = 45
        self.held_chat(r, T0 + 160)
        _, checks, detail = ktjobs.verdict(r, 300)
        self.assertTrue(checks["no_connection_over_30s"])
        self.assertEqual(detail["reload_window_s"], [154.0, 400.0])

    def test_no_reload_window_without_a_completed_job_or_a_return(self):
        for name, breaks in {
            "no completed job": lambda r: [j.update(status="failed") for j in r["jobs"].values()],
            "warm lane never back": lambda r: r.update(warm_timeline=[(T0 - 10, "Ready", None), (T0 + 60, "Preempted", None)]),
        }.items():
            r = passing_run()
            breaks(r)
            self.held_chat(r, T0 + 250)
            _, checks, detail = ktjobs.verdict(r, 300)
            self.assertFalse(checks["no_connection_over_30s"], name)
            self.assertIsNone(detail["reload_window_s"], name)

    def test_p95(self):
        self.assertIsNone(ktjobs.p95([None]))
        self.assertEqual(ktjobs.p95(list(range(1, 101))), 95)


if __name__ == "__main__":
    unittest.main()
