#!/usr/bin/env python3
from __future__ import annotations

import base64
import json
import sys
from dataclasses import asdict
from pathlib import Path


def _err(msg: str) -> None:
    sys.stdout.write(json.dumps({"ok": False, "error": msg}) + "\n")


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        _err("expected a single JSON argument")
        return 2

    try:
        req = json.loads(argv[1])
        if not isinstance(req, dict):
            raise ValueError("request must be a JSON object")
    except Exception as e:
        _err(f"invalid JSON request: {e}")
        return 2

    try:
        from browser_kit.browser import BrowserConfig, BrowserManager
    except Exception as e:
        # Keep this as a simple message; Go wrapper adds install hints.
        _err(str(e))
        return 3

    url = str(req.get("url") or "").strip()
    if not url:
        _err("url is required")
        return 2

    selector = str(req.get("selector") or "").strip() or None
    full_page = bool(req.get("full_page", True))
    fmt = str(req.get("format") or "png").strip().lower()
    if fmt == "jpg":
        fmt = "jpeg"

    quality = int(req.get("quality", 85))
    viewport_width = int(req.get("viewport_width", 1440))
    viewport_height = int(req.get("viewport_height", 900))

    wait_until = str(req.get("wait_until") or "load").strip().lower()
    wait_ms = int(req.get("wait_ms", 0))
    timeout_ms = int(req.get("timeout_ms", 30000))

    user_agent = str(req.get("user_agent") or "").strip() or None
    session_id = str(req.get("session_id") or "").strip() or None
    storage_dir = str(req.get("storage_dir") or "").strip() or None
    stealth = bool(req.get("stealth", True))
    omit_background = bool(req.get("omit_background", False))

    block_resources = req.get("block_resources", [])
    if block_resources is None:
        block_resources = []
    if not isinstance(block_resources, list):
        _err("block_resources must be an array of strings")
        return 2
    block_resources = [str(x).strip().lower() for x in block_resources if str(x).strip()]

    if storage_dir:
        Path(storage_dir).mkdir(parents=True, exist_ok=True)

    cfg = BrowserConfig(
        headless=True,
        browser_name="chromium",
        user_agent=user_agent,
        viewport={"width": viewport_width, "height": viewport_height},
        stealth=stealth,
        # For screenshots, default to not blocking resources.
        block_resources=block_resources,
        storage_dir=storage_dir,
    )

    mgr = BrowserManager(cfg)
    try:
        with mgr.new_context(session_id=session_id) as context:
            page = context.new_page()

            page.goto(url, wait_until=wait_until, timeout=timeout_ms)
            if wait_ms > 0:
                page.wait_for_timeout(wait_ms)

            title = ""
            try:
                title = page.title() or ""
            except Exception:
                title = ""

            final_url = page.url or url

            ss_kwargs: dict[str, object] = {"type": fmt}
            if fmt == "jpeg":
                ss_kwargs["quality"] = quality
            if fmt == "png":
                ss_kwargs["omit_background"] = omit_background

            if selector:
                el = page.wait_for_selector(selector, timeout=timeout_ms)
                if el is None:
                    raise RuntimeError(f"selector not found: {selector}")
                img_bytes = el.screenshot(**ss_kwargs)  # type: ignore[arg-type]
            else:
                img_bytes = page.screenshot(full_page=full_page, **ss_kwargs)  # type: ignore[arg-type]

            b64 = base64.b64encode(img_bytes).decode("ascii")

            sys.stdout.write(
                json.dumps(
                    {
                        "ok": True,
                        "title": title,
                        "final_url": final_url,
                        "format": fmt,
                        "base64": b64,
                    }
                )
                + "\n"
            )
            return 0
    except Exception as e:
        _err(str(e))
        return 1
    finally:
        try:
            mgr.close()
        except Exception:
            pass


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
