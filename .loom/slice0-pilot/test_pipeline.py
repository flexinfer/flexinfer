#!/usr/bin/env python3
"""Regression tests for the Slice 0 pilot. stdlib-only: `python3 test_pipeline.py`.

Covers the domain-handling bug where `str.lstrip("www.")` stripped a CHARACTER SET
(corrupting 'wix.com' -> 'ix.com', 'wework.com' -> 'ework.com') instead of the
'www.' prefix. Domain corruption silently poisons dedup, email-domain matching,
and guessed-email construction (would send to first@ix.com), so it directly
threatens kill-test integrity.
"""
import pipeline as p


def test_strip_www_prefix_only():
    # leading 'www.' prefix is removed
    assert p.strip_www("www.acme.com") == "acme.com"
    assert p.strip_www("WWW.Acme.COM") == "acme.com"
    # domains that merely START with 'w' / '.' must NOT be eroded (the bug)
    assert p.strip_www("wix.com") == "wix.com"
    assert p.strip_www("wework.com") == "wework.com"
    assert p.strip_www("web3startup.io") == "web3startup.io"
    assert p.strip_www("www.web.dev") == "web.dev"
    # subdomain 'www.' only at the front
    assert p.strip_www("app.wwwidget.com") == "app.wwwidget.com"
    assert p.strip_www("") == ""
    assert p.strip_www(None) == ""


def test_host_of_uses_prefix_strip():
    assert p.host_of("https://www.wix.com/pricing") == "wix.com"
    assert p.host_of("http://wework.com") == "wework.com"
    assert p.host_of("https://www.acme.com") == "acme.com"
    assert p.host_of("not a url") == ""


def test_is_agg_domain():
    # directory/aggregator hosts must be rejected as a lead's own domain
    assert p.is_agg_domain("ycombinator.com")
    assert p.is_agg_domain("www.ycombinator.com")
    assert p.is_agg_domain("jobs.builtin.com")  # subdomain
    assert p.is_agg_domain("seedtable.com")
    # real prospect domains must pass through
    assert not p.is_agg_domain("acme.com")
    assert not p.is_agg_domain("crosby.ai")
    assert not p.is_agg_domain("")


def test_placeholder_locals_blocked():
    # doc-example addresses must be in the blocklist so harvest() drops them
    for local in ("jane", "john", "jane.doe", "firstname", "user", "you"):
        assert local in p.PLACEHOLDER_LOCAL, local
    # real-ish names must NOT be blocked
    assert "ryan" not in p.PLACEHOLDER_LOCAL
    assert "founders" not in p.PLACEHOLDER_LOCAL


def test_search_depth_defaults_to_basic():
    # default must be the cheap tier (1 credit), not advanced (2 credits)
    import os

    assert p.SEARCH_DEPTH in ("basic", "advanced")
    if "TAVILY_SEARCH_DEPTH" not in os.environ:
        assert p.SEARCH_DEPTH == "basic"
    assert p.GATHER_QUERIES >= 1 and p.EMAIL_QUERIES >= 1


def test_guess_email_uses_uncorrupted_domain():
    # the consequence the bug would have caused: a guessed address at a mangled domain
    primary, alts = p.guess_email(["jane", "doe"], "wix.com")
    assert primary == "jane@wix.com", primary
    assert "jane.doe@wix.com" in alts


if __name__ == "__main__":
    fns = [v for k, v in sorted(globals().items()) if k.startswith("test_")]
    for fn in fns:
        fn()
        print(f"ok  {fn.__name__}")
    print(f"\n{len(fns)} passed")
