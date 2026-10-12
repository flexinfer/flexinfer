#!/usr/bin/env python3
"""Report drift between GPU runtime/vLLM image pins in git and what Harbor holds.

Three questions, answered from build/runtime.yaml, deploy/, charts/ and the
Harbor v2.0 API (anonymous read of the public `flexinfer` project):

1. Does every `@sha256:` pin under deploy/ and charts/ still exist in Harbor,
   and does it carry an immutable `<tag>-<sha>` tag (Harbor retention keeps
   only tagged manifests, so a floating-tag-only digest is GC bait)?
2. For every promotable profile in build/runtime.yaml, do its consumers (the
   runtime DaemonSet profiles in deploy/system/values-k3s.yaml, the GPUProfile
   `spec.runtime.image`, and `promotion.model_manifests`) pin the profile's
   current build, or a build from another persona entirely?
3. Which consumers still pin a mutable tag instead of a digest?

A consumer pins a build of its profile only when the digest carries the
profile's floating tag or `<floating>-<7-12 hex>`, the tag the profile's CI
publish job pushes. A digest tagged `<floating>-<anything else>` (an off-CI
overlay such as `rocm-gfx906-humanizer-<sha>`) is reported as an off-profile
build: promoting the profile's current build over it would drop whatever the
overlay added.

Default mode prints a Markdown report and exits 0 unless a pinned digest is
missing from Harbor. `--strict` also fails on floating-only digests, persona
mismatches, off-profile builds, and mutable-tag pins; `--max-behind-days N`
fails when a consumer trails its profile's current build by more than N days.
Split pins (a GPUProfile `spec.runtime.image` that differs from the runtime
DaemonSet image for the same gpuArch) are reported but never fail. `--offline`
skips Harbor and prints the inventory only (used as the CI smoke test).

Tracking issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/74
Spec: .loom/20-product-spec-runtime-image-ci-2026-09-13.md
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - surfaced as a clear CI error
    sys.stderr.write(
        "check-runtime-pin-drift: PyYAML is required (pip install pyyaml)\n"
    )
    sys.exit(2)

REGISTRY_HOST = "registry.harbor.lan"
DEFAULT_API = f"https://{REGISTRY_HOST}/api/v2.0"
# Repos whose pins this report cares about. Diffusers/comfyui/mlc pins are
# reported in the inventory but never fail the check.
GPU_REPOS = ("runtime", "vllm")

DIGEST_REF_RE = re.compile(
    r"registry\.harbor\.lan/(?P<project>[a-z0-9_-]+)/(?P<repo>[a-z0-9_./-]+?)"
    r"(?::(?P<tag>[A-Za-z0-9_.-]+))?@(?P<digest>sha256:[0-9a-f]{64})"
)
TAG_REF_RE = re.compile(
    r"registry\.harbor\.lan/(?P<project>[a-z0-9_-]+)/(?P<repo>runtime|vllm)"
    r":(?P<tag>[A-Za-z0-9_.-]+)(?![A-Za-z0-9_.@-])"
)
IMMUTABLE_SUFFIX_RE = re.compile(r"-[0-9a-f]{7,12}$")


class Harbor:
    """Minimal anonymous Harbor v2.0 client with a per-run cache."""

    def __init__(self, api: str, ca_bundle: str | None, allow_insecure: bool):
        self.api = api.rstrip("/")
        self.cache: dict[str, dict | None] = {}
        self.ctx = (
            ssl.create_default_context(cafile=ca_bundle)
            if ca_bundle
            else ssl.create_default_context()
        )
        self.allow_insecure = allow_insecure
        self.insecure_used = False

    def artifact(self, project: str, repo: str, reference: str) -> dict | None:
        key = f"{project}/{repo}@{reference}"
        if key in self.cache:
            return self.cache[key]
        # Repository names with '/' must be double-encoded for the Harbor API.
        repo_enc = urllib.parse.quote(urllib.parse.quote(repo, safe=""), safe="")
        url = f"{self.api}/projects/{project}/repositories/{repo_enc}/artifacts/{reference}?with_tag=true"
        data = self._get(url)
        self.cache[key] = data
        return data

    def _get(self, url: str) -> dict | None:
        req = urllib.request.Request(url, headers={"Accept": "application/json"})
        try:
            with urllib.request.urlopen(req, context=self.ctx, timeout=30) as resp:
                return json.load(resp)
        except urllib.error.HTTPError as exc:
            if exc.code == 404:
                return None
            raise
        except urllib.error.URLError as exc:
            if isinstance(exc.reason, ssl.SSLError):
                if self.allow_insecure and not self.insecure_used:
                    # Internal Harbor CA not trusted by this runner image; the
                    # check only reads public metadata, so degrade with a loud
                    # note in the report.
                    self.insecure_used = True
                    self.ctx = ssl._create_unverified_context()  # noqa: SLF001
                    return self._get(url)
                if not self.allow_insecure:
                    raise SystemExit(
                        "check-runtime-pin-drift: Harbor TLS certificate is not trusted by this "
                        "Python (internal CA). Pass --ca-bundle <harbor-ca.pem> (or set "
                        "HARBOR_CA_BUNDLE), or --allow-insecure for an unverified read-only run."
                    ) from exc
            raise


def load_yaml(path: Path):
    with path.open("r", encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def iter_yaml_files(root: Path, subdirs: tuple[str, ...]) -> list[Path]:
    out: list[Path] = []
    for sub in subdirs:
        base = root / sub
        if base.is_dir():
            out.extend(sorted(p for p in base.rglob("*.y*ml") if p.is_file()))
    return out


def live_manifest_files(root: Path, files: list[Path]) -> set[str]:
    """Return the repo-relative paths that Flux (or an operator) actually applies.

    A manifest counts as live unless it sits under deploy/debug/ (hand-applied
    canaries) or lives in a directory whose kustomization.yaml does not list
    it (retired manifests "kept on disk for restore"). Directories without a
    kustomization.yaml (deploy/system, charts/) are live wholesale.
    """
    listed: dict[str, set[str]] = {}
    for path in files:
        if path.name != "kustomization.yaml":
            continue
        try:
            doc = load_yaml(path) or {}
        except yaml.YAMLError:
            continue
        names = set()
        for res in doc.get("resources") or []:
            if isinstance(res, str) and not res.endswith("/"):
                names.add(Path(res).name)
        listed[path.parent.relative_to(root).as_posix()] = names
    live: set[str] = set()
    for path in files:
        rel = path.relative_to(root).as_posix()
        if rel.startswith("deploy/debug/"):
            continue
        if rel.startswith("deploy/system/"):
            # Helm values (values-k3s.yaml) and the HelmRelease are consumed by
            # Flux's helm-controller, not listed as kustomize resources.
            live.add(rel)
            continue
        parent = path.parent.relative_to(root).as_posix()
        if parent in listed and path.name != "kustomization.yaml":
            if path.name in listed[parent]:
                live.add(rel)
            continue
        live.add(rel)
    return live


def scan_pins(root: Path, files: list[Path], live_files: set[str] | None = None):
    """Return (digest_pins, tag_pins) keyed by full ref, each with file refs.

    Each digest pin carries `live`: True when at least one non-comment
    reference sits in a manifest Flux applies (see live_manifest_files).
    """
    digest_pins: dict[str, dict] = {}
    tag_pins: dict[str, dict] = {}
    live_files = live_files if live_files is not None else set()
    for path in files:
        rel = path.relative_to(root).as_posix()
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for lineno, line in enumerate(text.splitlines(), 1):
            stripped = line.lstrip()
            commented = stripped.startswith("#")
            for m in DIGEST_REF_RE.finditer(line):
                ref = m.group(0)
                entry = digest_pins.setdefault(
                    ref,
                    {
                        **m.groupdict(),
                        "files": [],
                        "commented_only": True,
                        "live": False,
                    },
                )
                entry["files"].append(f"{rel}:{lineno}")
                if not commented:
                    entry["commented_only"] = False
                    if rel in live_files:
                        entry["live"] = True
            for m in TAG_REF_RE.finditer(line):
                if commented:
                    continue
                ref = m.group(0)
                entry = tag_pins.setdefault(ref, {**m.groupdict(), "files": []})
                entry["files"].append(f"{rel}:{lineno}")
    # Drop pins that only ever appear in comments.
    digest_pins = {k: v for k, v in digest_pins.items() if not v["commented_only"]}
    return digest_pins, tag_pins


def parse_tag(tag: str) -> tuple[str, str]:
    """Split 'runtime:rocm-gfx1100-serving' into (repo, floating tag)."""
    repo, _, floating = tag.partition(":")
    return repo, floating


PROFILE_BUILD = "profile build"
OFF_PROFILE_BUILD = "off-profile build"
PERSONA_MISMATCH = "persona mismatch"


def classify_persona(tags: list[str], floating: str, all_floating: set[str]) -> str:
    """Decide whether a digest's tags make it a build of `floating`'s profile.

    Each tag belongs to the longest floating tag in `all_floating` that it
    equals or extends with '-', so rocm-gfx1100-serving-<sha> belongs to
    rocm-gfx1100-serving, not rocm-gfx1100. A tag that is the floating tag
    itself or `<floating>-<7-12 hex>` (what CI pushes) is a profile build; any
    other `<floating>-...` tag is an off-profile build of that profile; a tag
    owned by another floating tag, or by none, is a persona mismatch.
    """
    off_profile = False
    for tag in tags:
        owners = [
            f for f in all_floating | {floating} if tag == f or tag.startswith(f + "-")
        ]
        if not owners or max(owners, key=len) != floating:
            continue
        suffix = tag[len(floating) :]
        if suffix == "" or IMMUTABLE_SUFFIX_RE.fullmatch(suffix):
            return PROFILE_BUILD
        off_profile = True
    return OFF_PROFILE_BUILD if off_profile else PERSONA_MISMATCH


def split_pins(root: Path, values: dict) -> list[dict]:
    """Runtime DaemonSet profiles whose GPUProfile runtime.image differs."""
    out: list[dict] = []
    for entry in (values.get("runtime") or {}).get("profiles") or []:
        arch = entry.get("gpuArch") or ""
        path = root / "deploy/gpuprofiles" / f"{arch}.yaml"
        if not arch or not path.is_file():
            continue
        doc = load_yaml(path) or {}
        gpuprofile_image = ((doc.get("spec") or {}).get("runtime") or {}).get(
            "image"
        ) or ""
        daemonset_image = entry.get("image") or ""
        if not gpuprofile_image or not daemonset_image:
            continue
        m1 = DIGEST_REF_RE.search(gpuprofile_image)
        m2 = DIGEST_REF_RE.search(daemonset_image)
        left = m1.group("digest") if m1 else gpuprofile_image
        right = m2.group("digest") if m2 else daemonset_image
        if left != right:
            out.append(
                {
                    "gpu_arch": arch,
                    "gpuprofile": (doc.get("metadata") or {}).get("name", arch),
                    "gpuprofile_file": path.relative_to(root).as_posix(),
                    "gpuprofile_image": left,
                    "daemonset_profile": entry.get("name"),
                    "daemonset_image": right,
                }
            )
    return out


def profile_consumers(root: Path, name: str, spec: dict, values: dict) -> list[dict]:
    """Mirror scripts/promote-runtime-digest.sh's consumer selection."""
    promotion = spec.get("promotion") or {}
    update_gpuprofile = promotion.get("gpuprofile", True)
    update_values = promotion.get("values_runtime_profiles", True)
    arch = spec.get("gpu_arch", "")
    consumers: list[dict] = []
    if update_values:
        for entry in (values.get("runtime") or {}).get("profiles") or []:
            if entry.get("name") == name or entry.get("gpuArch") == arch:
                consumers.append(
                    {
                        "kind": "runtime DaemonSet",
                        "consumer": entry.get("name"),
                        "file": "deploy/system/values-k3s.yaml",
                        "image": entry.get("image", ""),
                    }
                )
    if update_gpuprofile:
        for candidate in (
            root / "deploy/gpuprofiles" / f"{name}.yaml",
            root / "deploy/gpuprofiles" / f"{arch}.yaml",
        ):
            if candidate.is_file():
                doc = load_yaml(candidate) or {}
                image = ((doc.get("spec") or {}).get("runtime") or {}).get("image", "")
                consumers.append(
                    {
                        "kind": "GPUProfile runtime.image",
                        "consumer": doc.get("metadata", {}).get("name", candidate.stem),
                        "file": candidate.relative_to(root).as_posix(),
                        "image": image,
                    }
                )
                break
    for manifest in promotion.get("model_manifests") or []:
        path = root / manifest
        if path.is_file():
            doc = load_yaml(path) or {}
            consumers.append(
                {
                    "kind": "Model spec.image",
                    "consumer": doc.get("metadata", {}).get("name", path.stem),
                    "file": manifest,
                    "image": (doc.get("spec") or {}).get("image", ""),
                }
            )
    return consumers


def age_days(push_time: str | None, now: dt.datetime) -> int | None:
    if not push_time:
        return None
    try:
        pushed = dt.datetime.fromisoformat(push_time.replace("Z", "+00:00"))
    except ValueError:
        return None
    return (now - pushed).days


def short(digest: str) -> str:
    return digest[:19] if digest.startswith("sha256:") else digest


def main() -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument(
        "--repo-root",
        default=None,
        help="Repository root (default: parent of scripts/).",
    )
    ap.add_argument("--api", default=os.environ.get("HARBOR_API_URL", DEFAULT_API))
    ap.add_argument("--ca-bundle", default=os.environ.get("HARBOR_CA_BUNDLE") or None)
    ap.add_argument(
        "--allow-insecure",
        action="store_true",
        help="Fall back to unverified TLS if the Harbor CA is untrusted.",
    )
    ap.add_argument(
        "--offline", action="store_true", help="Inventory only; do not query Harbor."
    )
    ap.add_argument("--strict", action="store_true")
    ap.add_argument("--max-behind-days", type=int, default=None)
    ap.add_argument(
        "--markdown",
        default=None,
        help="Write the Markdown report here as well as stdout.",
    )
    ap.add_argument("--json", dest="json_out", default=None)
    args = ap.parse_args()

    root = (
        Path(args.repo_root).resolve()
        if args.repo_root
        else Path(__file__).resolve().parent.parent
    )
    runtime_cfg = load_yaml(root / "build/runtime.yaml") or {}
    values = load_yaml(root / "deploy/system/values-k3s.yaml") or {}
    registry = runtime_cfg.get("registry", f"{REGISTRY_HOST}/flexinfer")
    project = registry.split("/", 1)[1] if "/" in registry else "flexinfer"

    files = iter_yaml_files(root, ("deploy", "charts"))
    live_files = live_manifest_files(root, files)
    digest_pins, tag_pins = scan_pins(root, files, live_files)
    now = dt.datetime.now(dt.timezone.utc)
    harbor = (
        None if args.offline else Harbor(args.api, args.ca_bundle, args.allow_insecure)
    )

    findings: list[str] = []
    hard: list[str] = []
    lines: list[str] = []
    report: dict = {
        "generated": now.isoformat(),
        "offline": args.offline,
        "pins": [],
        "profiles": [],
        "off_profile_builds": [],
        "mutable_tag_pins": [],
        "split_pins": [],
    }
    # Every profile's floating tag per repo, so a sibling persona's build
    # (rocm-gfx1100-serving-<sha>) is never mistaken for an off-profile build
    # of rocm-gfx1100.
    floating_tags: dict[str, set[str]] = {}
    for spec in (runtime_cfg.get("profiles") or {}).values():
        repo, floating = parse_tag((spec or {}).get("tag", ""))
        if floating:
            floating_tags.setdefault(repo, set()).add(floating)

    lines.append("# Runtime pin drift report")
    lines.append("")
    lines.append(
        "GPUProfile `spec.runtime.image` is not read by any Go code today (only "
        "`spec.runtime.bundledBackends` is, via `RuntimeProfileBundlesBackend` in "
        "`pkg/runtime/payload.go`); the runtime DaemonSets run the "
        "`deploy/system/values-k3s.yaml` image, so the GPUProfile pin is documentation."
    )
    lines.append("")

    # ---- 1. Pin inventory -------------------------------------------------
    lines.append("## Digest pins under deploy/ and charts/")
    lines.append("")
    lines.append(
        "| Digest | Repo | Refs | Tags in Harbor | Pushed | Age (d) | Status |"
    )
    lines.append("|---|---|---|---|---|---|---|")
    for ref, pin in sorted(digest_pins.items(), key=lambda kv: (kv[1]["repo"], kv[0])):
        status = "not checked"
        tags: list[str] = []
        pushed = None
        if harbor is not None:
            art = harbor.artifact(pin["project"], pin["repo"], pin["digest"])
            if art is None:
                if pin.get("live"):
                    status = "MISSING"
                    if pin["repo"] in GPU_REPOS:
                        hard.append(
                            f"{pin['repo']}@{short(pin['digest'])} is not in Harbor (files: {', '.join(pin['files'][:3])})"
                        )
                else:
                    status = "MISSING (inactive manifest)"
                    findings.append(
                        f"inactive manifest pins {pin['repo']}@{short(pin['digest'])}, which is not in Harbor (files: {', '.join(pin['files'][:3])})"
                    )
            else:
                tags = [t.get("name", "") for t in art.get("tags") or []]
                pushed = art.get("push_time")
                immutable = [t for t in tags if IMMUTABLE_SUFFIX_RE.search(t)]
                if not tags:
                    status = "untagged (GC hazard)"
                    findings.append(
                        f"{pin['repo']}@{short(pin['digest'])} has no tag left in Harbor"
                    )
                elif not immutable:
                    status = "floating-only (GC hazard)"
                    findings.append(
                        f"{pin['repo']}@{short(pin['digest'])} is only reachable through a floating tag ({', '.join(tags)})"
                    )
                else:
                    status = "ok"
        row = {
            "ref": ref,
            "repo": pin["repo"],
            "digest": pin["digest"],
            "files": pin["files"],
            "tags": tags,
            "push_time": pushed,
            "age_days": age_days(pushed, now),
            "status": status,
        }
        report["pins"].append(row)
        lines.append(
            f"| `{short(pin['digest'])}` | {pin['repo']} | {len(pin['files'])} | {', '.join(tags) or '-'} | "
            f"{(pushed or '-')[:10]} | {row['age_days'] if row['age_days'] is not None else '-'} | {status} |"
        )
    lines.append("")

    # ---- 2. Profile → consumer drift --------------------------------------
    lines.append(
        "## Profile consumers vs. current build (build/runtime.yaml promotion targets)"
    )
    lines.append("")
    lines.append(
        "| Profile | Floating tag | Current build | Pushed | Consumer | Pinned | Pinned tags | Status |"
    )
    lines.append("|---|---|---|---|---|---|---|---|")
    for name, spec in (runtime_cfg.get("profiles") or {}).items():
        promotion = spec.get("promotion") or {}
        arch = spec.get("gpu_arch", "")
        if "promotion" not in spec and name != arch:
            # Experimental/canary profiles without a promotion block would
            # otherwise be matched to the arch's DaemonSets by gpuArch (the
            # same latent footgun scripts/promote-runtime-digest.sh has); only
            # the canonical per-arch profile owns consumers by default.
            continue
        if (
            promotion.get("gpuprofile", True) is False
            and promotion.get("values_runtime_profiles", True) is False
            and not promotion.get("model_manifests")
        ):
            continue  # canary-only lane; nothing to promote into
        repo, floating = parse_tag(spec.get("tag", ""))
        consumers = profile_consumers(root, name, spec, values)
        if not consumers:
            continue
        current = None
        current_pushed = None
        if harbor is not None:
            art = harbor.artifact(project, repo, floating)
            if art:
                current = art.get("digest")
                current_pushed = art.get("push_time")
        for c in consumers:
            if c["kind"] == "Model spec.image" and c["file"] not in live_files:
                continue  # retired manifest kept on disk; inventory section reports it
            m = DIGEST_REF_RE.search(c["image"] or "")
            pinned = m.group("digest") if m else None
            pinned_tags: list[str] = []
            pinned_pushed = None
            status = "not checked"
            if not pinned:
                status = "mutable tag" if c["image"] else "unset"
                findings.append(
                    f"{c['kind']} {c['consumer']} pins '{c['image'] or '<unset>'}' (no digest)"
                )
            elif harbor is not None:
                art = harbor.artifact(project, repo, pinned)
                if art is None:
                    status = "MISSING"
                    hard.append(
                        f"{c['kind']} {c['consumer']} pins {short(pinned)} which is not in Harbor"
                    )
                else:
                    pinned_tags = [t.get("name", "") for t in art.get("tags") or []]
                    pinned_pushed = art.get("push_time")
                    persona = classify_persona(
                        pinned_tags, floating, floating_tags.get(repo, set())
                    )
                    if persona == PERSONA_MISMATCH:
                        status = f"persona mismatch (expects {floating})"
                        findings.append(
                            f"{c['kind']} {c['consumer']} pins a build tagged {pinned_tags} but its profile '{name}' publishes {repo}:{floating}"
                        )
                    elif persona == OFF_PROFILE_BUILD:
                        status = OFF_PROFILE_BUILD
                        report["off_profile_builds"].append(
                            {
                                "profile": name,
                                "tag": spec.get("tag"),
                                "consumer": c["consumer"],
                                "kind": c["kind"],
                                "file": c["file"],
                                "pinned": pinned,
                                "pinned_tags": pinned_tags,
                                "current": current,
                            }
                        )
                        findings.append(
                            f"{c['kind']} {c['consumer']} pins {short(pinned)} tagged {', '.join(pinned_tags)}: "
                            f"an off-profile build, not produced by the {name} profile's CI job "
                            f"(which tags {repo}:{floating}-<commit sha>); promoting "
                            f"{short(current) if current else 'the current build'} would replace it"
                        )
                    elif current and pinned == current:
                        status = "current"
                    else:
                        behind = None
                        if current_pushed and pinned_pushed:
                            behind = age_days(pinned_pushed, now) - age_days(current_pushed, now)  # type: ignore[operator]
                        status = (
                            f"behind by {behind}d" if behind is not None else "behind"
                        )
                        if (
                            args.max_behind_days is not None
                            and behind is not None
                            and behind > args.max_behind_days
                        ):
                            hard.append(
                                f"{c['kind']} {c['consumer']} trails {repo}:{floating} by {behind} days (> {args.max_behind_days})"
                            )
                        else:
                            findings.append(
                                f"{c['kind']} {c['consumer']} trails {repo}:{floating}: {status}"
                            )
            report["profiles"].append(
                {
                    "profile": name,
                    "tag": spec.get("tag"),
                    "current": current,
                    "current_push_time": current_pushed,
                    "consumer": c["consumer"],
                    "kind": c["kind"],
                    "file": c["file"],
                    "pinned": pinned,
                    "pinned_tags": pinned_tags,
                    "pinned_push_time": pinned_pushed,
                    "status": status,
                }
            )
            lines.append(
                f"| {name} | `{repo}:{floating}` | `{short(current) if current else '-'}` | {(current_pushed or '-')[:10]} | "
                f"{c['kind']} `{c['consumer']}` | `{short(pinned) if pinned else c['image'] or '-'}` | {', '.join(pinned_tags) or '-'} | {status} |"
            )
    lines.append("")

    # ---- 3. Mutable-tag pins ---------------------------------------------
    lines.append("## Mutable-tag pins (runtime/vllm without a digest)")
    lines.append("")
    if tag_pins:
        lines.append("| Ref | Files |")
        lines.append("|---|---|")
        for ref, pin in sorted(tag_pins.items()):
            report["mutable_tag_pins"].append({"ref": ref, "files": pin["files"]})
            findings.append(f"mutable tag pin {ref} ({len(pin['files'])} refs)")
            lines.append(
                f"| `{ref}` | {', '.join(pin['files'][:4])}{' …' if len(pin['files']) > 4 else ''} |"
            )
    else:
        lines.append("none")
    lines.append("")

    # ---- 4. Split pins (report-only) -------------------------------------
    lines.append("## Split pins (GPUProfile vs. runtime DaemonSet, report-only)")
    lines.append("")
    owner = {(r["kind"], r["consumer"]): r["profile"] for r in report["profiles"]}
    split_lines: list[str] = []
    for split in split_pins(root, values):
        report["split_pins"].append(split)
        line = (
            f"split pin: GPUProfile {split['gpuprofile']} runtime.image {short(split['gpuprofile_image'])} "
            f"≠ DaemonSet profile {split['daemonset_profile']} {short(split['daemonset_image'])}"
        )
        gp_owner = owner.get(("GPUProfile runtime.image", split["gpuprofile"]))
        ds_owner = owner.get(("runtime DaemonSet", split["daemonset_profile"]))
        if gp_owner and ds_owner and gp_owner != ds_owner:
            line += f" (promoted from different profiles: {gp_owner} vs. {ds_owner})"
        split_lines.append(line)
        lines.append(f"- WARNING: {line}")
    if not split_lines:
        lines.append("none")
    lines.append("")

    # ---- Summary ---------------------------------------------------------
    lines.append("## Summary")
    lines.append("")
    lines.append(
        f"- digest pins scanned: {len(digest_pins)} ({sum(1 for p in digest_pins.values() if p.get('live'))} in live manifests); profiles with consumers: {len({r['profile'] for r in report['profiles']})}"
    )
    if harbor is not None and harbor.insecure_used:
        lines.append(
            "- NOTE: Harbor TLS could not be verified on this runner; metadata was read without verification (--allow-insecure)."
        )
    for item in hard:
        lines.append(f"- FAIL: {item}")
    for item in findings:
        lines.append(f"- warn: {item}")
    report["hard"] = hard
    report["findings"] = findings

    text = "\n".join(lines) + "\n"
    sys.stdout.write(text)
    if args.markdown:
        Path(args.markdown).write_text(text, encoding="utf-8")
    if args.json_out:
        Path(args.json_out).write_text(json.dumps(report, indent=2), encoding="utf-8")

    if hard:
        return 1
    if args.strict and findings:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
