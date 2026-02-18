#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
import time
from pathlib import Path


VOYAGER_BASE_URL = "https://www.linkedin.com/voyager/api"
VOYAGER_HEALTH_PATH = "/me"
MAX_WARNING_LEN = 240


def _err(msg: str, state: str = "error", warnings: list[str] | None = None) -> None:
    payload = {
        "ok": False,
        "state": state,
        "error": msg,
    }
    if warnings:
        payload["warnings"] = warnings
    sys.stdout.write(json.dumps(payload) + "\n")


def _extract_cookie_value(context, name: str) -> str:
    for cookie in context.cookies("https://www.linkedin.com"):
        if cookie.get("name") == name:
            return str(cookie.get("value") or "")
    return ""


def _seed_missing_cookies(context, li_at: str, jsessionid: str) -> None:
    existing_li_at = _extract_cookie_value(context, "li_at")
    existing_jsessionid = _extract_cookie_value(context, "JSESSIONID")

    cookies = []
    if li_at and not existing_li_at:
        cookies.append(
            {
                "name": "li_at",
                "value": li_at,
                "domain": ".linkedin.com",
                "path": "/",
                "httpOnly": True,
                "secure": True,
            }
        )
    if jsessionid and not existing_jsessionid:
        cookies.append(
            {
                "name": "JSESSIONID",
                "value": jsessionid.strip('"'),
                "domain": ".linkedin.com",
                "path": "/",
                "httpOnly": False,
                "secure": True,
            }
        )
    if cookies:
        context.add_cookies(cookies)


def _contains_any(text: str, markers: list[str]) -> bool:
    lower = (text or "").lower()
    return any(marker in lower for marker in markers)


def _truncate_text(text: str, max_len: int) -> str:
    if max_len <= 0:
        return ""
    if len(text) <= max_len:
        return text
    if max_len <= 3:
        return text[:max_len]
    return text[: max_len - 3] + "..."


def _sanitize_error_message(raw: str) -> str:
    raw = (raw or "").strip()
    if not raw:
        return ""
    lower = raw.lower()
    if _contains_any(lower, ["max redirect count exceeded", "err_too_many_redirects", "too many redirects"]):
        return "voyager request redirect loop (max redirect count exceeded)"
    if "execution context was destroyed" in lower:
        return "voyager request failed: execution context was destroyed"

    call_log_idx = lower.find("call log:")
    if call_log_idx >= 0:
        raw = raw[:call_log_idx].strip()

    for line in raw.splitlines():
        line = line.strip()
        if line:
            return _truncate_text(line, MAX_WARNING_LEN)
    return _truncate_text(raw, MAX_WARNING_LEN)


def _append_warning(warnings: list[str], message: str) -> None:
    cleaned = _sanitize_error_message(message)
    if cleaned and cleaned not in warnings:
        warnings.append(cleaned)


def _classify_state(page, has_li_at: bool) -> str:
    page_url = page.url if page else ""
    u = (page_url or "").lower()
    if _contains_any(u, ["/checkpoint", "/challenge", "/security", "captcha"]):
        return "challenge"
    if _contains_any(u, ["/login", "/uas/login", "authwall", "/signup"]):
        return "logged_out"
    try:
        if page.locator("#global-nav").count() > 0:
            return "healthy"
    except Exception:
        pass
    if has_li_at and "/feed" in u:
        # Presence of cookies alone is not proof of auth; feed can still redirect/challenge.
        return "logged_out"
    if has_li_at and _contains_any(u, ["/messaging", "/in/"]):
        return "healthy"
    return "logged_out"


def _classify_exception_state(err: Exception) -> str:
    msg = str(err).lower()
    if _contains_any(msg, ["err_too_many_redirects", "max redirect count exceeded", "too many redirects"]):
        return "logged_out"
    if _contains_any(msg, ["checkpoint", "challenge", "captcha", "security verification"]):
        return "challenge"
    if _contains_any(msg, ["login", "authwall", "session expired"]):
        return "logged_out"
    return "error"


def _wait_for_post_login_state(page, timeout_ms: int) -> str:
    deadline = time.time() + max(timeout_ms, 1000) / 1000.0
    while time.time() < deadline:
        try:
            if page.locator("#global-nav").count() > 0:
                return "healthy"
        except Exception:
            pass
        state = _classify_state(page, True)
        if state in ("challenge", "healthy"):
            return state
        page.wait_for_timeout(1000)
    return "logged_out"


def _parse_json_maybe(text: str):
    if not text:
        return None
    try:
        return json.loads(text)
    except Exception:
        return None


def _voyager_probe(context, method: str, path: str, body, jsessionid: str, timeout_ms: int) -> dict:
    method = (method or "GET").upper()
    if not path.startswith("/"):
        path = "/" + path
    url = VOYAGER_BASE_URL + path
    headers = {
        "Accept": "application/json",
        "X-RestLi-Protocol-Version": "2.0.0",
    }
    payload = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        payload = json.dumps(body)
    if jsessionid:
        headers["csrf-token"] = jsessionid.strip('"')

    try:
        response = context.request.fetch(
            url,
            method=method,
            headers=headers,
            data=payload,
            timeout=max(timeout_ms, 1000),
        )
        return {
            "status": response.status,
            "ok": response.ok,
            "url": response.url,
            "redirected": False,
            "text": response.text(),
            "headers": {k.lower(): v for k, v in response.headers.items()},
        }
    except Exception as error:
        return {
            "status": 0,
            "ok": False,
            "url": url,
            "redirected": False,
            "text": "",
            "error": _sanitize_error_message(str(error)),
            "headers": {},
        }


def _classify_voyager_state(probe: dict) -> str:
    status = int(probe.get("status") or 0)
    url = str(probe.get("url") or "")
    text = str(probe.get("text") or "")
    error = str(probe.get("error") or "")
    headers = probe.get("headers") or {}
    content_type = str(headers.get("content-type") or "")

    if _contains_any(url, ["/checkpoint", "/challenge", "/security", "captcha"]):
        return "challenge"
    if _contains_any(url, ["/login", "/uas/login", "authwall", "/signup"]):
        return "logged_out"

    if status in (401, 403):
        if _contains_any(text, ["checkpoint", "challenge", "captcha", "security"]):
            return "challenge"
        return "logged_out"
    if 300 <= status < 400:
        return "logged_out"
    if status == 0:
        if _contains_any(error, ["err_too_many_redirects", "max redirect count exceeded", "too many redirects", "login", "authwall"]):
            return "logged_out"
        return _classify_exception_state(Exception(error or "voyager request failed"))
    if 200 <= status < 300:
        if "application/json" in content_type.lower():
            return "healthy"
        if _contains_any(text, ["checkpoint", "challenge", "authwall"]):
            return "challenge"
        return "healthy"
    if status >= 400:
        if _contains_any(text, ["login", "authwall", "session expired"]):
            return "logged_out"
        return "error"

    return "unknown"


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        _err("expected a single JSON argument")
        return 2

    try:
        req = json.loads(argv[1])
        if not isinstance(req, dict):
            raise ValueError("request must be object")
    except Exception as e:
        _err(f"invalid JSON request: {e}")
        return 2

    try:
        from browser_kit.browser import BrowserConfig, BrowserManager
    except Exception as e:
        _err(str(e))
        return 3

    action = str(req.get("action") or "health").strip().lower()
    mode = str(req.get("mode") or "silent").strip().lower()
    url = str(req.get("url") or "https://www.linkedin.com/feed/").strip()
    storage_dir = str(req.get("storage_dir") or "").strip()
    session_id = str(req.get("session_id") or "primary").strip() or "primary"
    stealth = bool(req.get("stealth", True))
    headless = bool(req.get("headless", True))
    timeout_ms = int(req.get("timeout_ms", 60000))

    session_token = str(req.get("session_token") or "").strip()
    jsessionid = str(req.get("jsessionid") or "").strip().strip('"')

    username = str(req.get("username") or "").strip()
    password = str(req.get("password") or "").strip()

    request_method = str(req.get("method") or "GET").strip().upper()
    request_path = str(req.get("path") or VOYAGER_HEALTH_PATH).strip()
    request_body = req.get("body")

    if storage_dir:
        Path(storage_dir).mkdir(parents=True, exist_ok=True)

    pre_warnings: list[str] = []
    if stealth:
        try:
            import playwright_stealth  # noqa: F401
        except Exception:
            stealth = False
            pre_warnings.append("playwright-stealth not installed; proceeding with stealth disabled")

    cfg = BrowserConfig(
        headless=headless,
        browser_name="chromium",
        stealth=stealth,
        storage_dir=storage_dir,
        block_resources=[],
        timezone_id="America/New_York",
        locale="en-US",
    )

    mgr = BrowserManager(cfg)
    if action == "recover" and storage_dir:
        storage_state_path = Path(storage_dir) / f"{session_id}.json"
        try:
            if storage_state_path.exists():
                storage_state_path.unlink()
                pre_warnings.append("cleared persisted browser session state before recovery")
        except Exception as storage_err:
            pre_warnings.append(_sanitize_error_message(f"storage state cleanup warning: {storage_err}"))

    try:
        with mgr.new_context(session_id=session_id) as context:
            warnings: list[str] = list(pre_warnings)
            page = context.new_page()

            if action == "recover":
                try:
                    context.clear_cookies()
                except Exception as clear_err:
                    _append_warning(warnings, f"cookie clear warning: {clear_err}")

                login_url = "https://www.linkedin.com/login"
                login_nav_warning = ""
                state = "logged_out"
                try:
                    page.goto(login_url, wait_until="domcontentloaded", timeout=timeout_ms)
                except Exception as login_nav_err:
                    login_nav_warning = str(login_nav_err)
                    _append_warning(warnings, f"login navigation warning: {login_nav_warning}")

                if not login_nav_warning:
                    if mode == "interactive":
                        # In interactive mode, avoid auto-submit to reduce automation fingerprints.
                        if username:
                            try:
                                if page.locator("#username").count() > 0:
                                    page.locator("#username").fill(username, timeout=5000)
                            except Exception:
                                warnings.append("username field not found")
                        if password:
                            try:
                                if page.locator("#password").count() > 0:
                                    page.locator("#password").fill(password, timeout=5000)
                            except Exception:
                                warnings.append("password field not found")
                        warnings.append("interactive recovery: complete login/checkpoint in browser window")
                        state = _wait_for_post_login_state(page, timeout_ms)
                    else:
                        # Silent mode attempts best-effort credentialed login.
                        if username:
                            try:
                                page.locator("#username").fill(username, timeout=5000)
                            except Exception:
                                warnings.append("username field not found")
                        if password:
                            try:
                                page.locator("#password").fill(password, timeout=5000)
                            except Exception:
                                warnings.append("password field not found")
                        if username and password:
                            try:
                                page.locator("button[type='submit']").first.click(timeout=5000)
                                page.wait_for_timeout(1800)
                            except Exception:
                                warnings.append("login submit button not found")
                        page.wait_for_timeout(1200)
                        state = _classify_state(page, bool(_extract_cookie_value(context, "li_at")))
                else:
                    state = _classify_exception_state(Exception(login_nav_warning))
                li_at = _extract_cookie_value(context, "li_at")
                jsid = _extract_cookie_value(context, "JSESSIONID").strip('"')
            else:
                _seed_missing_cookies(context, session_token, jsessionid)

                initial_nav_warning = ""
                try:
                    page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)
                    page.wait_for_timeout(1200)
                except Exception as nav_err:
                    initial_nav_warning = str(nav_err)

                li_at = _extract_cookie_value(context, "li_at")
                jsid = _extract_cookie_value(context, "JSESSIONID").strip('"')

                if initial_nav_warning:
                    state = _classify_exception_state(Exception(initial_nav_warning))
                else:
                    state = _classify_state(page, bool(li_at))

                if initial_nav_warning:
                    _append_warning(warnings, f"initial navigation warning: {initial_nav_warning}")

            probe = None
            if action in ("health", "recover", "voyager_request"):
                probe_path = request_path if action == "voyager_request" else VOYAGER_HEALTH_PATH
                probe_method = request_method if action == "voyager_request" else "GET"
                probe_body = request_body if action == "voyager_request" else None

                probe = _voyager_probe(
                    context,
                    method=probe_method,
                    path=probe_path,
                    body=probe_body,
                    jsessionid=jsid,
                    timeout_ms=timeout_ms,
                )
                probe_state = _classify_voyager_state(probe)
                if probe_state != "unknown":
                    state = probe_state

                if probe.get("error"):
                    _append_warning(warnings, f"voyager probe error: {probe['error']}")
                if probe.get("status") and int(probe.get("status") or 0) >= 300:
                    _append_warning(
                        warnings,
                        f"voyager probe non-success status: {probe.get('status')} ({_truncate_text(str(probe.get('url') or ''), 120)})",
                    )

            payload = {
                "ok": True,
                "state": state,
                "final_url": page.url,
                "has_li_at": bool(li_at),
                "has_jsessionid": bool(jsid),
                "li_at": li_at,
                "jsessionid": jsid,
                "warnings": warnings,
            }

            if probe is not None:
                payload["http_status"] = int(probe.get("status") or 0)
                payload["response_url"] = str(probe.get("url") or "")
                payload["response_headers"] = probe.get("headers") or {}

                response_text = str(probe.get("text") or "")
                response_json = _parse_json_maybe(response_text)
                if response_json is not None:
                    payload["response_json"] = response_json
                elif response_text:
                    payload["response_text"] = response_text

            sys.stdout.write(json.dumps(payload) + "\n")
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
