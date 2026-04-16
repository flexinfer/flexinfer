#!/usr/bin/env python3
"""Deploy helpers for Flux-backed loom-hub releases.

This keeps the Makefile portable by handling image tag rewrites and
best-effort deploy status inspection in one place.
"""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Dict, List, Optional, Tuple


def read_text(path: Path) -> str:
    try:
        return path.read_text()
    except FileNotFoundError as exc:
        raise SystemExit(f"Flux Kustomization not found: {path}") from exc


def write_text(path: Path, text: str) -> None:
    path.write_text(text)


def replace_tag_line(text: str, label: str, tag: str) -> Tuple[str, int]:
    pattern = re.compile(rf'(^\s*newTag:\s*")[^"]*("\s*#\s*{re.escape(label)}\s*$)', re.M)

    def repl(match: re.Match[str]) -> str:
        return f'{match.group(1)}{tag}{match.group(2)}'

    return pattern.subn(repl, text)


def update_images(path: Path, tag: str) -> int:
    original = read_text(path)
    updated = original
    total_replacements = 0
    for label in ("custom-server", "loom-core"):
        updated, count = replace_tag_line(updated, label, tag)
        if count != 1:
            raise SystemExit(
                f"expected to update exactly one {label} tag in {path}, found {count}"
            )
        total_replacements += count

    if updated != original:
        write_text(path, updated)

    print(f"Updated {total_replacements} Flux image tag(s) in {path}")
    return total_replacements


def run(cmd: List[str]) -> Tuple[int, str, str]:
    proc = subprocess.run(cmd, capture_output=True, text=True)
    return proc.returncode, proc.stdout.strip(), proc.stderr.strip()


def kubectl_json(args: List[str]) -> Tuple[Optional[dict], Optional[str]]:
    if shutil.which("kubectl") is None:
        return None, "kubectl not found in PATH"

    code, stdout, stderr = run(["kubectl", "get", *args, "-o", "json"])
    if code != 0:
        msg = stderr or stdout or f"kubectl {' '.join(args)} failed with exit code {code}"
        return None, msg

    try:
        return json.loads(stdout), None
    except json.JSONDecodeError as exc:
        return None, f"failed to parse kubectl JSON output for {' '.join(args)}: {exc}"


def condition_true(obj: dict, condition_type: str) -> Tuple[bool, str]:
    for cond in obj.get("status", {}).get("conditions", []) or []:
        if cond.get("type") == condition_type:
            status = str(cond.get("status", "")).lower()
            reason = cond.get("reason") or cond.get("message") or ""
            return status == "true", reason
    return False, "condition missing"


def summarize_flux_resource(kind: str, name: str, namespace: str) -> Tuple[str, bool, bool]:
    obj, err = kubectl_json([kind, name, "-n", namespace])
    if err:
        return f"  {kind}/{name}: unavailable ({err})", False, False

    ready, reason = condition_true(obj, "Ready")
    observed = obj.get("status", {}).get("observedGeneration")
    generation = obj.get("metadata", {}).get("generation")
    revision = obj.get("status", {}).get("lastAppliedRevision")
    if not revision:
        revision = obj.get("status", {}).get("artifact", {}).get("revision")

    bits = [f"{kind}/{name}: Ready={'True' if ready else 'False'}"]
    if observed is not None and generation is not None:
        bits.append(f"generation={observed}/{generation}")
    if revision:
        bits.append(f"revision={revision}")
    if reason and not ready:
        bits.append(f"reason={reason}")
    return "  " + ", ".join(bits), ready, True


def image_repo_key(image: str, registry: str) -> Optional[str]:
    prefixes = {
        f"{registry}/mcp/custom-server": "custom-server",
        f"{registry}/mcp/loom-core": "loom-core",
    }
    for prefix, key in prefixes.items():
        if image == prefix or image.startswith(prefix + ":") or image.startswith(prefix + "@"):
            return key
    return None


def image_tag(image: str) -> str:
    if "@" in image and ":" not in image.rsplit("@", 1)[0]:
        return image.rsplit("@", 1)[1]
    if ":" in image:
        return image.rsplit(":", 1)[1]
    return "latest"


def deployment_convergence(namespace: str, registry: str, expected_tags: Dict[str, str]) -> Tuple[List[str], bool, bool]:
    obj, err = kubectl_json(["deploy", "-n", namespace])
    if err:
        return [f"  Kubernetes deployment check unavailable: {err}"], False, False

    per_repo = {
        key: {"total": 0, "converged": 0, "drift": []}
        for key in expected_tags
    }
    overall_ok = True

    for item in obj.get("items", []) or []:
        name = item.get("metadata", {}).get("name", "<unknown>")
        generation = item.get("metadata", {}).get("generation")
        status = item.get("status", {}) or {}
        spec = item.get("spec", {}) or {}
        replicas = int(spec.get("replicas") or 1)
        observed = status.get("observedGeneration")
        updated = int(status.get("updatedReplicas") or 0)
        available = int(status.get("availableReplicas") or 0)
        available_condition, reason = condition_true(item, "Available")

        tracked_images = []
        for container in spec.get("template", {}).get("spec", {}).get("containers", []) or []:
            image = container.get("image", "")
            repo_key = image_repo_key(image, registry)
            if repo_key:
                tracked_images.append((repo_key, image_tag(image)))

        if not tracked_images:
            continue

        for repo_key, tag in tracked_images:
            per_repo[repo_key]["total"] += 1
            expected = expected_tags.get(repo_key)
            if expected is None:
                overall_ok = False
                per_repo[repo_key]["drift"].append(
                    f"deployment/{name}: missing expected tag marker for {repo_key}"
                )
                continue
            converged = (
                tag == expected
                and observed == generation
                and updated >= replicas
                and available >= replicas
                and available_condition
            )
            if converged:
                per_repo[repo_key]["converged"] += 1
            else:
                overall_ok = False
                detail = (
                    f"deployment/{name}: image={tag} expected={expected}, "
                    f"replicas={available}/{replicas}, updated={updated}/{replicas}, "
                    f"observed={observed}/{generation}"
                )
                if reason and not available_condition:
                    detail += f", reason={reason}"
                per_repo[repo_key]["drift"].append(detail)

    lines = []
    for repo_key in ("custom-server", "loom-core"):
        info = per_repo.get(repo_key)
        if info is None:
            continue
        lines.append(
            f"  {repo_key}: {info['converged']}/{info['total']} deployments converged"
        )
        for drift in info["drift"]:
            lines.append(f"    ! {drift}")
    return lines, overall_ok, True


def status(path: Path, tag: str, registry: str, namespace: str, flux_namespace: str) -> int:
    print("=== Deployment Status ===")
    print()
    print("Local:")
    print(f"  Image tag: {tag}")
    print(f"  Registry:  {registry}")
    print(f"  Flux file: {path}")
    print()

    if not path.exists():
        print(f"GitOps: missing Flux Kustomization: {path}")
        return 1

    text = read_text(path)
    expected_tags: Dict[str, str] = {}
    for label in ("custom-server", "loom-core"):
        match = re.search(rf'^\s*newTag:\s*"([^"]*)"\s*#\s*{label}\s*$', text, re.M)
        if match:
            expected_tags[label] = match.group(1)

    print("GitOps:")
    for label in ("custom-server", "loom-core"):
        current = expected_tags.get(label)
        if current is None:
            print(f"  {label}: unavailable (tag marker missing)")
        else:
            print(f"  {label}: {current}")
    print()

    missing_markers = [label for label in ("custom-server", "loom-core") if label not in expected_tags]
    if missing_markers:
        print("Kubernetes:")
        print(f"  Flux image tag markers missing: {', '.join(missing_markers)}")
        return 1

    kubectl_available = shutil.which("kubectl") is not None
    if not kubectl_available:
        print("Flux / Kubernetes:")
        print("  kubectl not found in PATH; skipping Flux readiness and rollout convergence checks")
        return 0

    print("Flux:")
    flux_objects = [
        ("gitrepository", "loom-core", flux_namespace),
        ("gitrepository", "gitops-gitlab", flux_namespace),
        ("kustomization", "apps", flux_namespace),
        ("kustomization", "loom-hub-servers", flux_namespace),
    ]
    flux_ok = True
    flux_available = True
    for kind, name, ns in flux_objects:
        line, ok, available = summarize_flux_resource(kind, name, ns)
        print(line)
        flux_ok = flux_ok and ok
        flux_available = flux_available and available
    print()

    print("Kubernetes:")
    deployment_lines, deploy_ok, deploy_available = deployment_convergence(namespace, registry, expected_tags)
    for line in deployment_lines:
        print(line)
    print()

    if not flux_available or not deploy_available:
        print("Status: cluster data unavailable; convergence verdict deferred")
        return 0

    if flux_ok and deploy_ok:
        print("Status: Flux resources and tracked deployments are converged")
        return 0

    print("Status: convergence check incomplete or drift detected")
    return 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="flux_deploy.py")
    sub = parser.add_subparsers(dest="command", required=True)

    update = sub.add_parser("update-images", help="Update tracked Flux image tags in-place")
    update.add_argument("--file", required=True, type=Path)
    update.add_argument("--tag", required=True)
    update.set_defaults(func=lambda args: (update_images(args.file, args.tag), 0)[1])

    status_cmd = sub.add_parser("status", help="Show deploy status and convergence")
    status_cmd.add_argument("--file", required=True, type=Path)
    status_cmd.add_argument("--tag", required=True)
    status_cmd.add_argument("--registry", required=True)
    status_cmd.add_argument("--namespace", default="loom-hub")
    status_cmd.add_argument("--flux-namespace", default="flux-system")
    status_cmd.set_defaults(
        func=lambda args: status(args.file, args.tag, args.registry, args.namespace, args.flux_namespace)
    )

    return parser


def main(argv: Optional[List[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    result = args.func(args)
    return int(result or 0)


if __name__ == "__main__":
    raise SystemExit(main())
