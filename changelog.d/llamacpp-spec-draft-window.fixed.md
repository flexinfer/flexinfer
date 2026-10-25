The llama.cpp backend now spells the speculative draft window for the image's
llama.cpp build. llama.cpp b8955 (2026-04-28) replaced `--draft-max` /
`--draft-min` with `--spec-draft-n-max` / `--spec-draft-n-min` for draft-model
spec types and moved the n-gram types to their own `--spec-ngram-*` flags; the
old names abort llama-server from that build on, so `draftMax` / `draftMin`
could not be used on any current image (gfx1100 b11847, the gfx906 b11335
runtime). `specType: draft-mtp` always renders the new spelling; other spec
types follow the new `spec.config.llamacppBuild` key, and an unset build keeps
the legacy spelling of the certified gfx906 b8173 ngram-simple envelope. The
controller now certifies an explicit draft-mtp window as its own envelope,
`draft-mtp-draft<N>-min<M>` (draftMax 1–16, draftMin 0–draftMax), and refuses
the ngram-simple 16/1 envelope on a declared b8955+ build, where the window
would be silently ignored. No lane changes behaviour: none sets these keys
today, and no window certificate exists yet.
