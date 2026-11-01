`flexinfer-bench tool_call_conformance`, the D4 gauntlet gate and KT-SO harness. It drives
schema-constrained tool-call turns (200 by default, `required` / `named` `tool_choice`,
`response_format` json_schema and vLLM `structured_outputs` envelopes) through `internal/agentloop`
against any lane URL, sorts every reply into a parse-failure taxonomy (`invalid_json`,
`schema_violation`, `wrong_tool_name`, `truncated`, `request_error`), repeats the turns unguided,
reads decode tokens/s and MTP position-0 acceptance off the lane's `/metrics`, checks greedy parity
with the `scripts/verify-vllm-mtp.py` prompts, and prints a JSON report plus a ready-to-paste
validation-matrix row (exit 0 on PASS). It registers through the `flexinfer-bench`
subcommand registry (from the `swap` profiler); flag invocations of the benchmark are unchanged. `agentloop.ChatClient`
gains `CompleteTurn` (explicit `tool_choice`, `response_format`, `structured_outputs`, seed,
temperature override; completion tokens and latency in the result); `Complete` sends the same body
as before.
