`scripts/wan-decode-ab.py run` takes `--setenv KEY=VALUE` and `--unsetenv KEY`. It applies them in-process
before torch loads, so the runtime image's `runtime.env` cannot override them, and records them in the run
record. `collect` judges the S9 fix arms (`t2v-B-tile`, `t2v-B-noexp`, `t2v-B-fix2`) against arm A on the same
bars, judges a `-fix` cache arm against the fix arm, and reports the single-env `dx-*` diagnostics. Result:
`.loom/70-result-s9-wan-decode-ab-2026-11-05.md`.
