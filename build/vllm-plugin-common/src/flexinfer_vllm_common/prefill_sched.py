"""Two changes to how vLLM's scheduler sizes prefill chunks (opt-in).

``--long-prefill-token-threshold`` caps how many prompt tokens one request may
prefill per engine step, so a long prompt cannot take the whole step budget
while another request waits. On a lane that serves short requests beside long
prompts it leaves two gaps, measured on the 5930k workhorse on 2026-11-25
(64K, cap 416, two sequence slots; one 56K prompt with 2K requests back to
back, the KV-usage gauge sampled 20 times a second):

1. The cap is off whenever one request is the only one eligible. A lone 56K
   prompt therefore prefills in 3,744-token steps that take 3.2-4.4 s, and
   the engine schedules one step ahead, so a request that arrives sits through
   the rest of the step in flight plus one whole uncapped step: 3.8-5.0 s
   before its first chunk runs.
2. The cap applies to every request, so a 2K prompt is cut into five 416-token
   chunks: 3.9-5.1 s of its own, at 0.75-1.10 s a step.

Together that is the lane's 8-10 s time to first token for a 2K request beside
a long prefill. Two environment variables change the two decisions:

``FLEXINFER_PREFILL_SOLO_CAP=<tokens>``
    A request that is the only one eligible is capped at this many prefill
    tokens per step, instead of not at all. 0 (the default) keeps vLLM's rule.

``FLEXINFER_PREFILL_CAP_EXEMPT=<tokens>``
    A request with at most this many tokens left to compute is never capped,
    by the configured threshold or by the solo cap. 0 (the default) keeps
    vLLM's rule.

Set them together: a solo cap without an exemption also cuts a short prompt
that arrives on an idle lane. Both count tokens, and on a hybrid model vLLM
still rounds every chunk down to its state-block boundary, so use multiples
of the lane's block size (416 on the Qwen3.8-27B lanes).

How it is installed. The three lines live in the middle of
``Scheduler.schedule``, a 900-line method with no hook near them, so the file
is rewritten the way ``segmented_fp8_lut`` rewrites the attention kernel:

1. ``vllm/v1/core/sched/scheduler.py`` is read from disk and accepted only by
   sha256. The anchors alone would accept a file that kept those lines and
   changed what surrounds them.
2. The three sites are replaced, the two values are written in as module
   constants, and the result is imported as a sibling module.
3. ``Scheduler.schedule`` is replaced by the sibling's ``schedule``. Nothing
   else of the sibling is used. ``AsyncScheduler``, the class an engine with
   async scheduling runs, inherits the method; the install checks that it
   still does.

The sibling's ``schedule`` runs with the sibling module's globals. That is
sound only because scheduler.py defines nothing at module level except its
logger and the ``Scheduler`` class, and ``schedule`` neither calls ``super()``
nor names its class; the rewrite checks both and refuses otherwise.

With both variables at 0 nothing is read, rewritten or replaced. A vLLM whose
scheduler.py is not one of ``KNOWN_SOURCES`` fails the boot that asked for the
patch. Like the rest of this package, the module imports neither vLLM nor
torch at module scope.
"""

import ast
import hashlib
import importlib
import importlib.util
import os
import sys
import tempfile

PREFILL_SOLO_CAP_ENV = "FLEXINFER_PREFILL_SOLO_CAP"
PREFILL_CAP_EXEMPT_ENV = "FLEXINFER_PREFILL_CAP_EXEMPT"
SCHEDULER_MODULE = "vllm.v1.core.sched.scheduler"
ASYNC_SCHEDULER_MODULE = "vllm.v1.core.sched.async_scheduler"
# Imported under vLLM's sched package so the file's relative imports resolve.
VARIANT_MODULE = "vllm.v1.core.sched._flexinfer_scheduler_prefill"
PATCH_MARKER = "_flexinfer_prefill_sched"
SOLO_CAP_NAME = "_FX_PREFILL_SOLO_CAP"
CAP_EXEMPT_NAME = "_FX_PREFILL_CAP_EXEMPT"
# sha256 of every vllm/v1/core/sched/scheduler.py the rewrite has been read
# against.
KNOWN_SOURCES = {
    "d76cc22257550972137df9c893505d0bd0f5d482374691e6eca4372772de501a": (
        "vLLM 0.33.0+rocm72 (runtime@1a2dd85c and runtime@4e337e5e)"
    ),
}
# The largest value either variable accepts. Anything above the step budget
# behaves like "no cap"; this only catches a value in the wrong unit.
MAX_TOKENS = 1 << 20

_CLASS_DEF = "\nclass Scheduler(SchedulerInterface):\n"
_SOLO_SITE = (
    "        long_prefill_token_threshold = (\n"
    "            self.scheduler_config.long_prefill_token_threshold\n"
    "            if num_eligible_reqs > 1\n"
    "            else 0\n"
    "        )\n"
)
_RUNNING_SITE = (
    "            if 0 < long_prefill_token_threshold < num_new_tokens:\n"
    "                num_new_tokens = long_prefill_token_threshold\n"
)
_WAITING_SITE = (
    "                    if 0 < long_prefill_token_threshold < num_new_tokens:\n"
    "                        num_new_tokens = long_prefill_token_threshold\n"
)


class RewriteError(RuntimeError):
    """vLLM's scheduler.py is not the shape the rewrite expects."""


def _tokens(environ, name: str) -> int:
    raw = environ.get(name, "0")
    if not (raw.isascii() and raw.isdigit()):
        raise RuntimeError(f"{name} must be a whole number of tokens, got {raw!r}")
    value = int(raw)
    if value > MAX_TOKENS:
        raise RuntimeError(f"{name} must be at most {MAX_TOKENS} tokens, got {value}")
    return value


def prefill_sched_settings(environ=os.environ) -> tuple:
    """Return ``(solo_cap, cap_exempt)`` in tokens; ``(0, 0)`` means off."""
    return (
        _tokens(environ, PREFILL_SOLO_CAP_ENV),
        _tokens(environ, PREFILL_CAP_EXEMPT_ENV),
    )


def source_sha256(source: str) -> str:
    return hashlib.sha256(source.encode()).hexdigest()


def _replace(source: str, old: str, new: str, what: str) -> str:
    found = source.count(old)
    if found != 1:
        raise RewriteError(
            f"{what}: expected 1 occurrence of {old!r}, found {found}; "
            "this vLLM's scheduler.py changed shape"
        )
    return source.replace(old, new)


def _capped(site: str, indent: str) -> str:
    """The cap test of ``site`` with the exemption added."""
    head, body = site.splitlines(keepends=True)
    if head != f"{indent}if 0 < long_prefill_token_threshold < num_new_tokens:\n":
        raise AssertionError(head)
    return (
        f"{indent}if (\n"
        f"{indent}    0 < long_prefill_token_threshold < num_new_tokens\n"
        f"{indent}    and num_new_tokens > {CAP_EXEMPT_NAME}\n"
        f"{indent}):\n"
        f"{body}"
    )


def _check_schedule_is_portable(rewritten: str) -> None:
    """Refuse a ``schedule`` that cannot run with another module's globals."""
    tree = ast.parse(rewritten)
    top = [
        node
        for node in tree.body
        if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef))
    ]
    names = [node.name for node in top]
    if names != ["Scheduler"]:
        raise RewriteError(
            "scheduler.py must define only the Scheduler class at module level "
            f"for its schedule() to run from a sibling module; found {names}"
        )
    schedule = [
        node
        for node in top[0].body
        if isinstance(node, ast.FunctionDef) and node.name == "schedule"
    ]
    if len(schedule) != 1:
        raise RewriteError(
            f"expected one Scheduler.schedule, found {len(schedule)}; "
            "this vLLM's scheduler.py changed shape"
        )
    for node in ast.walk(schedule[0]):
        if isinstance(node, ast.Name) and node.id in {
            "super",
            "__class__",
            "Scheduler",
        }:
            raise RewriteError(
                f"Scheduler.schedule refers to {node.id!r} (line {node.lineno}); "
                "it cannot be moved onto the stock class"
            )


def rewrite_source(source: str, solo_cap: int, cap_exempt: int) -> str:
    """Return ``source`` with the solo cap and the cap exemption written in.

    With both values 0 the rewritten ``schedule`` makes the decisions the
    stock one makes: the solo threshold is 0, and ``num_new_tokens > 0`` is
    true wherever the stock test is.
    """
    for name, value in ((SOLO_CAP_NAME, solo_cap), (CAP_EXEMPT_NAME, cap_exempt)):
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            raise ValueError(f"{name} must be a non-negative int, got {value!r}")
        if name in source:
            raise RewriteError(f"scheduler.py already defines {name}")
    out = _replace(
        source,
        _SOLO_SITE,
        _SOLO_SITE.replace(
            "            else 0\n", f"            else {SOLO_CAP_NAME}\n"
        ),
        "solo threshold",
    )
    out = _replace(
        out, _RUNNING_SITE, _capped(_RUNNING_SITE, " " * 12), "running-request cap"
    )
    out = _replace(
        out, _WAITING_SITE, _capped(_WAITING_SITE, " " * 20), "waiting-request cap"
    )
    constants = f"\n{SOLO_CAP_NAME} = {solo_cap}\n{CAP_EXEMPT_NAME} = {cap_exempt}\n\n"
    out = _replace(out, _CLASS_DEF, constants + _CLASS_DEF, "Scheduler class")
    _check_schedule_is_portable(out)
    return out


def load_variant_module(rewritten: str):
    """Import ``rewritten`` as ``VARIANT_MODULE`` from a private temp file.

    A real file keeps tracebacks through the rewritten ``schedule`` readable.
    """
    directory = tempfile.mkdtemp(prefix="flexinfer-prefill-sched-")
    path = os.path.join(directory, "flexinfer_scheduler_prefill.py")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(rewritten)
    spec = importlib.util.spec_from_file_location(VARIANT_MODULE, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[VARIANT_MODULE] = module
    try:
        spec.loader.exec_module(module)
    except BaseException:
        del sys.modules[VARIANT_MODULE]
        raise
    return module


def install_prefill_sched(
    scheduler,
    async_scheduler,
    solo_cap: int,
    cap_exempt: int,
    *,
    load_variant=load_variant_module,
) -> bool:
    """Replace ``Scheduler.schedule`` with the rewritten one; return True.

    A second call with the same values is a no-op; with different values it
    fails, since one process cannot hold two rewrites of one method.
    """
    for module, name in ((scheduler, "Scheduler"), (async_scheduler, "AsyncScheduler")):
        if not hasattr(module, name):
            raise RuntimeError(
                f"flexinfer_vllm_common.prefill_sched: {module.__name__} lacks "
                f"{name}; this vLLM's scheduler changed shape"
            )
    stock_class = scheduler.Scheduler
    if "schedule" not in vars(stock_class):
        raise RuntimeError(
            "flexinfer_vllm_common.prefill_sched: Scheduler does not define "
            "schedule itself; this vLLM's scheduler changed shape"
        )
    settings = (solo_cap, cap_exempt)
    installed = getattr(scheduler, PATCH_MARKER, None)
    if installed is not None:
        if installed != settings:
            raise RuntimeError(
                f"prefill scheduling is already installed with {installed}, "
                f"asked for {settings}"
            )
        return True
    if "schedule" in vars(async_scheduler.AsyncScheduler):
        raise RuntimeError(
            "flexinfer_vllm_common.prefill_sched: AsyncScheduler overrides "
            "schedule, so replacing Scheduler.schedule would not reach it"
        )
    with open(scheduler.__file__, encoding="utf-8") as handle:
        source = handle.read()
    digest = source_sha256(source)
    if digest not in KNOWN_SOURCES:
        raise RuntimeError(
            f"{PREFILL_SOLO_CAP_ENV}/{PREFILL_CAP_EXEMPT_ENV} set but "
            f"{scheduler.__file__} (sha256 {digest}) is not a scheduler.py the "
            f"rewrite was written against; known: {sorted(KNOWN_SOURCES)}"
        )
    rewritten = rewrite_source(source, solo_cap, cap_exempt)
    variant = load_variant(rewritten)
    schedule = vars(variant.Scheduler)["schedule"]
    schedule.__wrapped__ = vars(stock_class)["schedule"]
    stock_class.schedule = schedule
    if async_scheduler.AsyncScheduler.schedule is not schedule:
        raise RuntimeError(
            "flexinfer_vllm_common.prefill_sched: AsyncScheduler.schedule is not "
            "the replaced method"
        )
    setattr(scheduler, PATCH_MARKER, settings)
    print(
        "flexinfer: prefill sched: "
        f"solo cap={solo_cap or 'off'} tokens, cap-exempt<={cap_exempt or 'off'} "
        f"tokens (vLLM source {digest[:12]} = {KNOWN_SOURCES[digest]}; rewritten "
        f"{source_sha256(rewritten)[:12]}; module {variant.__file__})",
        flush=True,
    )
    return True


def install_prefill_sched_if_enabled(environ=os.environ) -> tuple:
    """Install when either variable is set; return ``(solo_cap, cap_exempt)``.

    ``(0, 0)`` means nothing was installed.
    """
    solo_cap, cap_exempt = prefill_sched_settings(environ)
    if not (solo_cap or cap_exempt):
        return (0, 0)
    try:
        scheduler = importlib.import_module(SCHEDULER_MODULE)
        async_scheduler = importlib.import_module(ASYNC_SCHEDULER_MODULE)
    except ModuleNotFoundError as exc:
        raise RuntimeError(
            f"{PREFILL_SOLO_CAP_ENV}/{PREFILL_CAP_EXEMPT_ENV} set but this vLLM "
            f"lacks {exc.name}"
        ) from exc
    install_prefill_sched(scheduler, async_scheduler, solo_cap, cap_exempt)
    return (solo_cap, cap_exempt)
