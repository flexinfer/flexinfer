# diarize_conformance fixtures

A two-minute clip with three speakers, scored with `--collar 0`:

| File | Turns | Against `reference.rttm` (`clip`: A 0-40 s, B 40-80 s, C 80-120 s) |
|---|---|---|
| `baseline.json` | the standalone `/diarize` answer: SPEAKER_00 0-41, SPEAKER_01 41-80, SPEAKER_02 80-120 | 1 s confusion (40-41 s) = DER 1/120 |
| `job.json` | a diarize job's content; its `diarization.segments`: SPEAKER_01 0-41, SPEAKER_00 41-80, SPEAKER_02 80-118 | 1 s confusion + 2 s missed (118-120 s) = DER 3/120 |

So the job is 1.67 points worse than the standalone Deployment (pass at ≤ 5), and all six words
carry a speaker. The `other` lines in the RTTM belong to another recording and are skipped by
`--uri clip`. The 30-minute pass and fail cases are generated in `diarize_conformance_test.go`.
