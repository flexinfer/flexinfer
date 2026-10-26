#!/usr/bin/env python3
"""Fail a stacked serving-runtime build whose overlay broke the vLLM stack.

build/Dockerfile.runtime-serving-v032 layers the FlexInfer runtime supervisor,
diffusers and snac onto the upstream vLLM 0.32 ROCm image. The runtime
DaemonSet spawns every backend from that one image (inferCommand in
internal/runtime/manager.go), so the build proves, without a GPU:

- the overlay removed or changed no package of the vLLM stack (the Dockerfile
  snapshots it before the overlay and installs the overlay against it as pip
  constraints; this re-checks the result);
- `pip check` reports nothing the pre-overlay `pip check` did not (the vLLM
  base already fails it on an Ubuntu system package, pygobject without
  pycairo, which nothing FlexInfer runs imports);
- every spawn target resolves: `python` is the image's interpreter, the
  `python -m vllm.entrypoints.openai.api_server` module is runnable, and the
  diffusers and snac server scripts exist;
- every third-party module the servers import outside a try block resolves,
  and every name they import from a diffusers module exists in the installed
  diffusers;
- the supervisor binary and tini are executable.

Every failing check is reported before the build fails once.
"""

from __future__ import annotations

import argparse
import ast
import importlib
import importlib.metadata
import importlib.util
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Callable, Iterable


VLLM_SERVER_MODULE = "vllm.entrypoints.openai.api_server"
MAIN_GUARD_RE = re.compile(r"""^if\s+__name__\s*==\s*["']__main__["']\s*:""", re.M)


def normalize_name(name: str) -> str:
    """PEP 503 name normalization, so snapshot and metadata names compare."""
    return re.sub(r"[-_.]+", "-", name).lower()


def read_snapshot(text: str) -> dict[str, str]:
    """Parse `pip list --format=freeze` output into {name: version}."""
    packages: dict[str, str] = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        name, sep, version = line.partition("==")
        if not sep:
            raise ValueError(f"snapshot line is not name==version: {raw!r}")
        packages[normalize_name(name)] = version.strip()
    return packages


def installed_packages(distributions: Iterable) -> dict[str, str]:
    """{name: version} of the distribution Python would import.

    importlib.metadata yields distributions in sys.path order, and the Ubuntu
    base carries shadowed duplicates (six, zipp, cryptography, ... in
    /usr/lib/python3/dist-packages behind pip's copies in /usr/local), so the
    first one seen wins, as it does for imports and for `pip list`.
    """
    packages: dict[str, str] = {}
    for dist in distributions:
        name = dist.metadata["Name"]
        if name:
            packages.setdefault(normalize_name(name), dist.version)
    return packages


def frozen_stack_errors(
    snapshot: dict[str, str], installed: dict[str, str]
) -> list[str]:
    errors = []
    for name, version in sorted(snapshot.items()):
        got = installed.get(name)
        if got is None:
            errors.append(f"{name} {version} was removed by the overlay")
        elif got != version:
            errors.append(f"{name} changed from {version} to {got} by the overlay")
    return errors


def pip_check_lines(output: str) -> set[str]:
    """The problem lines of `pip check` output (empty when it passes)."""
    return {
        line.strip()
        for line in output.splitlines()
        if line.strip() and line.strip() != "No broken requirements found."
    }


def new_pip_check_problems(baseline: str, current: str) -> list[str]:
    return sorted(pip_check_lines(current) - pip_check_lines(baseline))


def interpreter_error(
    which: Callable[[str], str | None],
    executable: str,
    realpath: Callable[[str], str] = os.path.realpath,
) -> str | None:
    """The runtime spawns `python`; it must be this image's interpreter."""
    found = which("python")
    if found is None:
        return "`python` is not on PATH; the runtime spawns every Python backend as `python`"
    if realpath(found) != realpath(executable):
        return (
            f"`python` resolves to {realpath(found)}, not the interpreter that "
            f"has vLLM ({realpath(executable)})"
        )
    return None


def module_main_error(module: str, find_spec: Callable) -> str | None:
    """`python -m <module>` must find the module and a __main__ guard in it."""
    try:
        spec = find_spec(module)
    except (ImportError, ValueError) as exc:
        return f"{module} cannot be located: {exc}"
    if spec is None or not spec.origin:
        return f"{module} is not importable"
    try:
        source = Path(spec.origin).read_text()
    except OSError as exc:
        return f"{module} source unreadable at {spec.origin}: {exc}"
    if not MAIN_GUARD_RE.search(source):
        return (
            f"{module} has no __main__ guard, so `python -m {module}` would not serve"
        )
    return None


def _inside_try(node: ast.AST, parents: dict) -> bool:
    parent = parents.get(node)
    while parent is not None:
        if isinstance(parent, ast.Try):
            return True
        parent = parents.get(parent)
    return False


def server_imports(source: str) -> tuple[set[str], dict[str, set[str]]]:
    """Return the required third-party modules and diffusers names of a script.

    Imports inside a try block are optional by construction (the server
    handles their absence) and are skipped. Relative and stdlib imports are
    skipped. The second value maps each diffusers module to the names imported
    from it, at any scope.
    """
    tree = ast.parse(source)
    parents: dict = {}
    for node in ast.walk(tree):
        for child in ast.iter_child_nodes(node):
            parents[child] = node
    modules: set[str] = set()
    diffusers_names: dict[str, set[str]] = {}
    for node in ast.walk(tree):
        if not isinstance(node, (ast.Import, ast.ImportFrom)):
            continue
        if _inside_try(node, parents):
            continue
        if isinstance(node, ast.ImportFrom):
            if node.level or not node.module:
                continue
            names = [node.module]
            if node.module.split(".")[0] == "diffusers":
                diffusers_names.setdefault(node.module, set()).update(
                    alias.name for alias in node.names if alias.name != "*"
                )
        else:
            names = [alias.name for alias in node.names]
        for name in names:
            top = name.split(".")[0]
            if top not in sys.stdlib_module_names:
                modules.add(top)
    return modules, diffusers_names


def missing_modules(modules: Iterable[str], find_spec: Callable) -> list[str]:
    missing = []
    for module in sorted(modules):
        try:
            if find_spec(module) is None:
                missing.append(module)
        except (ImportError, ValueError):
            missing.append(module)
    return missing


def missing_names(names: dict[str, set[str]], import_module: Callable) -> list[str]:
    missing = []
    for module, wanted in sorted(names.items()):
        try:
            loaded = import_module(module)
        except Exception as exc:  # a broken submodule is a failure, not a crash
            missing.append(f"{module} ({type(exc).__name__}: {exc})")
            continue
        for name in sorted(wanted):
            try:
                getattr(loaded, name)
            except Exception:  # diffusers resolves names lazily on access
                missing.append(f"{module}.{name}")
    return missing


def non_executable(paths: Iterable[str], access: Callable = os.access) -> list[str]:
    return [path for path in paths if not access(path, os.X_OK)]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--snapshot",
        required=True,
        help="`pip list --format=freeze` of the vLLM stack before the overlay",
    )
    parser.add_argument(
        "--pip-check-baseline",
        required=True,
        help="`pip check` output of the vLLM stack before the overlay",
    )
    parser.add_argument(
        "--server",
        action="append",
        default=[],
        help="a Python server script the runtime spawns (repeatable)",
    )
    parser.add_argument(
        "--executable",
        action="append",
        default=[],
        help="a binary that must be executable (repeatable)",
    )
    args = parser.parse_args(argv)
    failures: list[str] = []

    def report(label: str, errors: list[str], detail: str = "") -> None:
        if errors:
            for error in errors:
                failures.append(f"{label}: {error}")
                print(f"FAIL {label}: {error}", flush=True)
        else:
            print(f"ok   {label}{': ' + detail if detail else ''}", flush=True)

    snapshot = read_snapshot(Path(args.snapshot).read_text())
    installed = installed_packages(importlib.metadata.distributions())
    added = sorted(set(installed) - set(snapshot))
    report(
        "frozen vLLM stack",
        frozen_stack_errors(snapshot, installed),
        f"{len(snapshot)} packages unchanged, {len(added)} added: {', '.join(added)}",
    )

    baseline = Path(args.pip_check_baseline).read_text()
    current = subprocess.run(
        [sys.executable, "-m", "pip", "check"],
        capture_output=True,
        text=True,
        check=False,
    ).stdout
    known = sorted(pip_check_lines(baseline))
    report(
        "pip check",
        [
            f"{line} (new since the overlay)"
            for line in new_pip_check_problems(baseline, current)
        ],
        f"no new problems; inherited from the base: {'; '.join(known) or 'none'}",
    )

    error = interpreter_error(shutil.which, sys.executable)
    report("python interpreter", [error] if error else [], sys.executable)

    error = module_main_error(VLLM_SERVER_MODULE, importlib.util.find_spec)
    report("vLLM server module", [error] if error else [], VLLM_SERVER_MODULE)

    for server in args.server:
        path = Path(server)
        if not path.is_file():
            report(f"server {path.name}", [f"{path} is missing"])
            continue
        modules, diffusers_names = server_imports(path.read_text())
        report(
            f"server {path.name} imports",
            [
                f"{module} does not resolve"
                for module in missing_modules(modules, importlib.util.find_spec)
            ],
            ", ".join(sorted(modules)),
        )
        if diffusers_names:
            count = sum(len(names) for names in diffusers_names.values())
            report(
                f"server {path.name} diffusers names",
                [
                    f"{name} is not in the installed diffusers"
                    for name in missing_names(diffusers_names, importlib.import_module)
                ],
                f"{count} names",
            )

    report(
        "executables",
        [f"{path} is not executable" for path in non_executable(args.executable)],
        ", ".join(args.executable),
    )

    if failures:
        print(f"{len(failures)} stacked-runtime check(s) failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
