#!/usr/bin/env python3
"""Turn the runtime pin-drift report into promotion proposals (issue #74 S4).

Reads the JSON written by ``scripts/check-runtime-pin-drift.py --json``, picks
every runtime profile whose live consumers pin an older digest than the
profile's newest immutable build, runs ``scripts/promote-runtime-digest.sh``
in dry-run for each, and writes one Markdown proposal per profile plus an
index. The proposals are the exact ``--apply`` command, the rollback digest,
the consumers that would change and the dry-run diff — everything a human
needs to run the kill-test and merge.

With ``--create-mrs`` (and ``FLEXINFER_PROMOTE_MR_TOKEN`` set) it also pushes a
branch per candidate with the promotion applied and a ``pending`` validation
matrix row, and opens a Draft merge request. Profiles whose promotion contract
requires a prewarm gate (``promotion.require_prewarm``) get a proposal only:
``--apply`` for them needs the live prewarm check, which CI cannot run.

Advisory by design: the exit code is 0 unless ``--fail-on-candidates`` is set.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - the CI image installs PyYAML
    yaml = None

SKIP_STATUS_PREFIXES = ("current", "ok", "MISSING")
# check-runtime-pin-drift.py status for a digest tagged `<floating>-<word>...`
# rather than the `<floating>-<sha>` its profile's CI job pushes.
OFF_PROFILE_STATUS = "off-profile build"
BRANCH_PREFIX = "bot/promote-"


def short(digest: str | None) -> str:
    if not digest:
        return "-"
    return digest.replace("sha256:", "")[:12]


def load_report(path: Path) -> dict:
    with path.open(encoding="utf-8") as fh:
        return json.load(fh)


def select_candidates(report: dict, only: set[str] | None = None) -> list[dict]:
    """Group the report's profile rows and keep the ones worth promoting.

    A profile is a candidate when at least one consumer pins a digest that
    differs from the profile's newest immutable build and the row's status is
    not "current"/"ok" (already there) or "MISSING" (nothing to promote to).
    """
    by_profile: dict[str, list[dict]] = {}
    for row in report.get("profiles", []):
        by_profile.setdefault(row["profile"], []).append(row)
    candidates = []
    for profile, rows in sorted(by_profile.items()):
        if only and profile not in only:
            continue
        current = next((r["current"] for r in rows if r.get("current")), None)
        if not current:
            continue
        stale = [
            r
            for r in rows
            if r.get("pinned")
            and r["pinned"] != current
            and not str(r.get("status", "")).startswith(SKIP_STATUS_PREFIXES)
        ]
        if not stale:
            continue
        candidates.append(
            {
                "profile": profile,
                "tag": rows[0].get("tag"),
                "current": current,
                "current_push_time": next(
                    (r.get("current_push_time") for r in rows if r.get("current_push_time")), None
                ),
                "consumers": stale,
                # Consumers on a build the profile's CI job did not produce:
                # promoting replaces whatever that build added.
                "off_profile": [r for r in stale if str(r.get("status", "")).startswith(OFF_PROFILE_STATUS)],
                # Rollback = the digest the first stale consumer pins today.
                "rollback": stale[0]["pinned"],
            }
        )
    return candidates


def off_profile_warning(candidate: dict) -> list[str]:
    """A blockquote warning per consumer that runs an off-profile build."""
    lines: list[str] = []
    for row in candidate.get("off_profile") or []:
        tags = row.get("pinned_tags") or []
        if isinstance(tags, str):
            tags = [tags]
        lines += [
            f"> **WARNING: off-profile build in service.** {row.get('kind')} `{row.get('consumer')}` "
            f"(`{row.get('file')}`) currently runs an off-profile build `{short(row.get('pinned'))}` "
            f"({', '.join(f'`{t}`' for t in tags) or 'no tags'}); promoting `{short(candidate['current'])}` "
            "replaces it — confirm the profile build carries the same components, e.g. llama.cpp version, "
            "before applying.",
            ">",
        ]
    if lines:
        lines[-1] = ""
    return lines


def promotion_contract(runtime_yaml: Path, profile: str) -> dict:
    if yaml is None or not runtime_yaml.exists():
        return {}
    data = yaml.safe_load(runtime_yaml.read_text(encoding="utf-8")) or {}
    return ((data.get("profiles") or {}).get(profile) or {}).get("promotion") or {}


def run_dry_run(promote_script: Path, repo_root: Path, profile: str, digest: str) -> tuple[int, str]:
    proc = subprocess.run(
        ["bash", str(promote_script), profile, "--digest", digest],
        cwd=str(repo_root),
        capture_output=True,
        text=True,
    )
    return proc.returncode, (proc.stdout + proc.stderr)


def validation_row_id(profile: str, today: dt.date) -> str:
    return f"{profile}-{today.isoformat()}"


def branch_name(profile: str, digest: str) -> str:
    return f"{BRANCH_PREFIX}{profile}-{short(digest)[:8]}"


def apply_command(profile: str, digest: str, rollback: str, row: str, require_prewarm: bool) -> str:
    parts = [
        "scripts/promote-runtime-digest.sh",
        profile,
        f"--digest {digest}",
        f"--validation-row {row}",
        f"--rollback-digest {rollback}",
    ]
    if require_prewarm:
        parts.append("--prewarm-profile <imagePrewarm profile per target node>")
    parts.append("--apply")
    return " \\\n  ".join(parts)


def render_proposal(candidate: dict, contract: dict, dry_run_rc: int, dry_run_out: str, today: dt.date) -> str:
    profile = candidate["profile"]
    row = validation_row_id(profile, today)
    require_prewarm = bool(contract.get("require_prewarm"))
    lines = [
        f"# Promotion proposal: `{profile}` → `{short(candidate['current'])}`",
        "",
        *off_profile_warning(candidate),
        f"- Profile tag: `{candidate.get('tag') or '-'}`",
        f"- Newest immutable build: `{candidate['current']}` (pushed {(candidate.get('current_push_time') or '-')[:19]})",
        f"- Rollback digest: `{candidate['rollback']}`",
        f"- Generated: {today.isoformat()} by scripts/propose-runtime-promotions.py",
        "",
        "## Consumers that would change",
        "",
        "| kind | consumer | file | pinned | pinned tags | pinned push time | status |",
        "|---|---|---|---|---|---|---|",
    ]
    for c in candidate["consumers"]:
        tags = c.get("pinned_tags") or []
        if isinstance(tags, str):
            tags = [tags]
        lines.append(
            f"| {c.get('kind')} | `{c.get('consumer')}` | `{c.get('file')}` | `{short(c.get('pinned'))}` | "
            f"{', '.join(tags) or '-'} | {(c.get('pinned_push_time') or '-')[:10]} | {c.get('status')} |"
        )
    lines += [
        "",
        "## Dry run (`promote-runtime-digest.sh`)",
        "",
        f"exit code: {dry_run_rc}",
        "",
        "```",
        dry_run_out.rstrip("\n"),
        "```",
        "",
        "## Apply",
        "",
        "1. Add a `pending` row `" + row + "` to `.loom/60-validation-matrix.md` (digest, canary command, rollback).",
        "2. Run:",
        "",
        "```bash",
        apply_command(profile, candidate["current"], candidate["rollback"], row, require_prewarm),
        "```",
        "",
        "3. Open the MR, let Flux roll, run the profile's kill-test, flip the row to `promote` (or roll back to the digest above).",
    ]
    if require_prewarm:
        lines += [
            "",
            "This profile's contract sets `promotion.require_prewarm`: prewarm the digest on every target node "
            "(`imagePrewarm.profiles` in `deploy/system/values-k3s.yaml`, one MR) before `--apply`, which gates on "
            "the prewarm pods holding it. CI therefore only proposes; it does not open an MR for this profile.",
        ]
    if contract.get("model_manifests"):
        lines += ["", "Model manifests moved by this promotion (`promotion.model_manifests`):", ""]
        lines += [f"- `{m}`" for m in contract["model_manifests"]]
    return "\n".join(lines) + "\n"


def render_index(candidates: list[dict], today: dt.date, mr_results: dict[str, str]) -> str:
    lines = [
        f"# Runtime promotion proposals — {today.isoformat()}",
        "",
        "Generated from `runtime-pin-drift.json` by `scripts/propose-runtime-promotions.py`. "
        "Each proposal is advisory: run the kill-test, then merge.",
        "",
    ]
    if not candidates:
        lines.append("No profile trails its newest immutable build. Nothing to promote.")
        return "\n".join(lines) + "\n"
    lines += [
        "| profile | tag | pinned → newest | consumers | warnings | MR |",
        "|---|---|---|---|---|---|",
    ]
    for c in candidates:
        pinned = ", ".join(sorted({short(x.get("pinned")) for x in c["consumers"]}))
        off = c.get("off_profile") or []
        warning = "-"
        if off:
            warning = "**off-profile build in service**: " + ", ".join(
                f"`{r.get('consumer')}` on `{short(r.get('pinned'))}`" for r in off
            )
        lines.append(
            f"| [`{c['profile']}`]({c['profile']}.md) | `{c.get('tag') or '-'}` | `{pinned}` → `{short(c['current'])}` | "
            f"{len(c['consumers'])} | {warning} | {mr_results.get(c['profile'], 'proposal only')} |"
        )
    if any(c.get("off_profile") for c in candidates):
        lines += [
            "",
            "A profile marked **off-profile build in service** has a consumer on a digest its CI job did not build "
            "(an overlay pushed by hand). Promoting replaces that build; read the warning in its proposal first.",
        ]
    return "\n".join(lines) + "\n"


# ----- optional MR creation ---------------------------------------------------

def _ssl_context(env: dict):
    """GitLab runs behind the homelab CA; the runner exposes it as CI_SERVER_TLS_CA_FILE."""
    import ssl

    ca_file = env.get("CI_SERVER_TLS_CA_FILE") or env.get("GITLAB_CA_BUNDLE")
    if ca_file and Path(ca_file).is_file():
        return ssl.create_default_context(cafile=ca_file)
    return None


def gitlab_api(method: str, url: str, token: str, payload: dict | None = None, env: dict | None = None) -> dict | list:
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("PRIVATE-TOKEN", token)
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=30, context=_ssl_context(env or {})) as resp:  # noqa: S310
        return json.loads(resp.read().decode("utf-8") or "null")


def open_mr_for_candidate(candidate: dict, contract: dict, proposal_md: str, repo_root: Path,
                          promote_script: Path, today: dt.date, env: dict) -> str:
    """Apply the promotion on a bot branch, push it, and open a Draft MR.

    Returns a short status string for the index. Never raises for a single
    candidate: CI reports the failure in the index instead.
    """
    token = env.get("FLEXINFER_PROMOTE_MR_TOKEN", "")
    api = env.get("CI_API_V4_URL", "")
    project = env.get("CI_PROJECT_ID", "")
    server = env.get("CI_SERVER_HOST", "")
    path = env.get("CI_PROJECT_PATH", "")
    if not (token and api and project and server and path):
        return "skipped: FLEXINFER_PROMOTE_MR_TOKEN / CI_* not set"
    if contract.get("require_prewarm"):
        return "proposal only: profile requires a prewarm gate"
    profile, digest, rollback = candidate["profile"], candidate["current"], candidate["rollback"]
    row = validation_row_id(profile, today)
    branch = branch_name(profile, digest)
    try:
        existing = gitlab_api(
            "GET",
            f"{api}/projects/{project}/merge_requests?state=opened&source_branch={urllib.parse.quote(branch, safe='')}",
            token,
            env,
        )
        if existing:
            return f"open: !{existing[0]['iid']}"
        # CI checkouts are detached; remember the commit, not a branch name.
        base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo_root, text=True).strip()
        git_env = dict(env)
        ca_file = env.get("CI_SERVER_TLS_CA_FILE") or env.get("GITLAB_CA_BUNDLE")
        if ca_file:
            git_env["GIT_SSL_CAINFO"] = ca_file
        subprocess.run(["git", "checkout", "-B", branch], cwd=repo_root, check=True, capture_output=True)
        subprocess.run(
            ["bash", str(promote_script), profile, "--digest", digest, "--validation-row", row,
             "--rollback-digest", rollback, "--apply"],
            cwd=repo_root, check=True, capture_output=True, text=True,
        )
        matrix = repo_root / ".loom" / "60-validation-matrix.md"
        if matrix.exists():
            text = matrix.read_text(encoding="utf-8")
            hdr = "|---|---:|---|---|---|---|---|---|---|---|---|---|---|\n"
            if hdr in text and row not in text:
                pending = (
                    f"| `{row}` — bot proposal: `{profile}` → `{short(digest)}` | n/a | {profile} | `runtime` | "
                    f"`pending` | `registry.harbor.lan/flexinfer/runtime@{digest}` | n/a | "
                    f"Proposed by CI from the pin-drift report; fill in the kill-test result before merging. | "
                    f"unknown until the kill-test runs | see the proposal in the pipeline artifacts | `{rollback}` | "
                    f"issue #74 S4 | `pending` |\n"
                )
                matrix.write_text(text.replace(hdr, hdr + pending, 1), encoding="utf-8")
        subprocess.run(["git", "config", "user.name", "flexinfer-promotion-bot"], cwd=repo_root, check=True)
        subprocess.run(["git", "config", "user.email", "promotion-bot@flexinfer.ai"], cwd=repo_root, check=True)
        subprocess.run(["git", "add", "-A"], cwd=repo_root, check=True)
        subprocess.run(
            ["git", "commit", "-q", "-m", f"chore(runtime): propose promoting {profile} to {short(digest)}\n\nGenerated by scripts/propose-runtime-promotions.py from the pin-drift report."],
            cwd=repo_root, check=True, capture_output=True,
        )
        push_url = f"https://oauth2:{token}@{server}/{path}.git"
        subprocess.run(
            ["git", "push", "-f", push_url, f"HEAD:refs/heads/{branch}"],
            cwd=repo_root, check=True, capture_output=True, env=git_env,
        )
        mr = gitlab_api(
            "POST",
            f"{api}/projects/{project}/merge_requests",
            token,
            payload={
                "source_branch": branch,
                "target_branch": "master",
                "title": f"Draft: promote {profile} to {candidate.get('tag') or short(digest)} ({short(digest)})",
                "description": proposal_md + "\n\n🤖 Opened by scripts/propose-runtime-promotions.py (issue #74 S4).",
                "remove_source_branch": True,
                "labels": "runtime-promotion",
            },
            env=env,
        )
        subprocess.run(["git", "checkout", "-q", "--detach", base], cwd=repo_root, check=True, capture_output=True)
        return f"opened: !{mr['iid']}"
    except (subprocess.CalledProcessError, urllib.error.URLError, KeyError, OSError) as exc:
        return f"failed: {type(exc).__name__}: {str(exc)[:120]}"


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--report", required=True, help="JSON from check-runtime-pin-drift.py --json")
    ap.add_argument("--out-dir", default="promotion-proposals")
    ap.add_argument("--repo-root", default=".")
    ap.add_argument("--promote-script", default="scripts/promote-runtime-digest.sh")
    ap.add_argument("--runtime-yaml", default="build/runtime.yaml")
    ap.add_argument("--profile", action="append", default=[], help="only propose for these profiles (repeatable)")
    ap.add_argument("--create-mrs", action="store_true", help="also push a bot branch and open a Draft MR per candidate")
    ap.add_argument("--fail-on-candidates", action="store_true", help="exit 2 when at least one proposal was written")
    args = ap.parse_args(argv)

    repo_root = Path(args.repo_root).resolve()
    promote_script = (repo_root / args.promote_script).resolve()
    runtime_yaml = (repo_root / args.runtime_yaml).resolve()
    out_dir = (repo_root / args.out_dir).resolve() if not Path(args.out_dir).is_absolute() else Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    today = dt.date.today()

    report = load_report(Path(args.report))
    candidates = select_candidates(report, set(args.profile) or None)
    mr_results: dict[str, str] = {}
    for cand in candidates:
        contract = promotion_contract(runtime_yaml, cand["profile"])
        rc, out = run_dry_run(promote_script, repo_root, cand["profile"], cand["current"])
        md = render_proposal(cand, contract, rc, out, today)
        (out_dir / f"{cand['profile']}.md").write_text(md, encoding="utf-8")
        if args.create_mrs:
            mr_results[cand["profile"]] = open_mr_for_candidate(
                cand, contract, md, repo_root, promote_script, today, dict(os.environ)
            )
    (out_dir / "README.md").write_text(render_index(candidates, today, mr_results), encoding="utf-8")
    print(f"{len(candidates)} promotion proposal(s) written to {out_dir}")
    for cand in candidates:
        print(f"  {cand['profile']}: {short(cand['rollback'])} -> {short(cand['current'])}"
              + ("  [off-profile build in service]" if cand.get("off_profile") else "")
              + (f"  [{mr_results[cand['profile']]}]" if cand["profile"] in mr_results else ""))
    if candidates and args.fail_on_candidates:
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
