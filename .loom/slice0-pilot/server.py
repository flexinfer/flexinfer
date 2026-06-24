#!/usr/bin/env python3
"""
Slice 0 review + send web app (the human gate) -- v2 (SQLite backend via store.py).

Serves staged leads as editable cards (recipient, subject, body) plus the research
signals (person, what they sell, trigger, funding). You review/edit and click Send.
Send goes out via SMTP ONLY on click (per-draft). Nothing auto-sends.

Run:  python3 server.py    -> http://127.0.0.1:8765
SMTP env (needed only to SEND; review works without it):
  SMTP_HOST (smtp.gmail.com) SMTP_PORT (587) SMTP_USER SMTP_PASS  FROM_EMAIL FROM_NAME
  Gmail app password: https://myaccount.google.com/apppasswords (needs 2FA).
"""
import json
import os
import smtplib
import ssl
from email.message import EmailMessage
from email.utils import formataddr
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import store

HOST, PORT = "127.0.0.1", int(os.environ.get("REVIEW_PORT", 8765))

PAGE = """<!doctype html><html><head><meta charset=utf-8>
<title>GTM Slice 0 - Review & Send</title><style>
body{font:14px -apple-system,sans-serif;max-width:880px;margin:24px auto;color:#1a1a1a;background:#fafafa}
h1{font-size:18px} .meta{color:#666;font-size:12px;margin-bottom:16px}
.card{background:#fff;border:1px solid #ddd;border-radius:10px;padding:16px;margin:14px 0;box-shadow:0 1px 2px rgba(0,0,0,.04)}
.card.sent{opacity:.5} .card.skipped{opacity:.4}
.row{display:flex;gap:10px;align-items:center;margin:6px 0;flex-wrap:wrap}
.badge{background:#eef;border-radius:6px;padding:2px 8px;font-size:12px;color:#335}
.badge.q-personal{background:#e3f6e8;color:#1a7f37}.badge.q-founder{background:#e3f6e8;color:#1a7f37}.badge.q-role{background:#fdf3e0;color:#9a6700}.badge.q-generic{background:#fde8e8;color:#a33}.badge.q-guessed{background:#fde8e8;color:#a33}
.warn{color:#a33;font-size:12px;background:#fde8e8;border-radius:6px;padding:6px 8px;margin:4px 0}
.sig{color:#555;font-size:12px;background:#f5f5f7;border-radius:6px;padding:6px 8px;margin:4px 0}
label{font-size:11px;color:#888;display:block;margin-top:8px}
input,textarea{width:100%;box-sizing:border-box;padding:8px;border:1px solid #ccc;border-radius:6px;font:13px monospace}
textarea{min-height:140px;resize:vertical}
button{padding:8px 16px;border:0;border-radius:6px;cursor:pointer;font-weight:600}
.send{background:#1a7f37;color:#fff} .skip{background:#eee;color:#444}
.st{font-size:12px;margin-left:8px} .ok{color:#1a7f37} .err{color:#c00}
a{color:#36c;font-size:12px}
</style></head><body>
<h1>GTM Slice 0 - Review &amp; Send</h1>
<div class=meta id=meta>loading...</div>
<div id=list></div>
<script>
async function load(){
  const r=await fetch('/api/queue'); const j=await r.json();
  document.getElementById('meta').textContent=
    `${j.total} total | ${j.staged} to review | ${j.sent} sent | ${j.skipped} skipped. SMTP: ${j.smtp}`;
  const L=document.getElementById('list'); L.innerHTML='';
  for(const d of j.leads) L.appendChild(card(d));
}
function card(d){
  const s=d.signals||{};
  const c=document.createElement('div'); c.className='card '+(d.status||'');
  c.innerHTML=`<div class=row><b>${esc(d.company)}</b>
    <span class=badge>score ${d.score}</span>
    <span class="badge q-${esc(d.email_quality||'')}">${esc(d.email_quality||'')}</span>
    ${d.person_name?`<span class=badge>${esc(d.person_name)} · ${esc(d.person_title||'')}</span>`:''}
    <a href="${esc(d.source_url||'#')}" target=_blank>source</a></div>
    <div class=sig><b>sells:</b> ${esc(s.what_they_sell||'?')} &nbsp;|&nbsp; <b>stage:</b> ${esc(s.stage||'?')} ${esc(s.funding||'')} &nbsp;|&nbsp; <b>size:</b> ${esc(s.headcount_est||'?')}</div>
    <div class=sig><b>trigger:</b> ${esc(s.trigger_signal||'?')}</div>
    ${s.email_guess?`<div class=warn><b>⚠ guessed email:</b> ${esc(s.email_guess)}${s.email_alternates?' · try: '+esc(s.email_alternates):''}</div>`:''}
    <div class=sig style="color:#777"><b>fit:</b> ${esc(d.rationale||'')}</div>
    <label>To</label><input id="to_${d.id}" value="${esc(d.email||'')}">
    <label>Subject</label><input id="su_${d.id}" value="${esc(d.subject||'')}">
    <label>Body</label><textarea id="bo_${d.id}">${esc(d.body||'')}</textarea>
    <div class=row style="margin-top:10px">
      <button class=send onclick="send(${d.id})" ${d.status!=='staged'?'disabled':''}>Send</button>
      <button class=skip onclick="skip(${d.id})" ${d.status!=='staged'?'disabled':''}>Skip</button>
      <span class=st id="st_${d.id}">${d.status!=='staged'?d.status:''}</span>
    </div>`;
  return c;
}
function esc(s){return (s||'').toString().replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
async function send(id){
  const st=document.getElementById('st_'+id); st.textContent='sending...'; st.className='st';
  const r=await fetch('/api/send',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({id, to:v('to_'+id), subject:v('su_'+id), body:v('bo_'+id)})});
  const j=await r.json();
  if(j.ok){st.textContent='sent ✓';st.className='st ok';setTimeout(load,600);}
  else{st.textContent='ERROR: '+j.error;st.className='st err';}
}
async function skip(id){
  await fetch('/api/skip',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id})});
  load();
}
function v(i){return document.getElementById(i).value;}
load();
</script></body></html>"""


def smtp_ready():
    return bool(os.environ.get("SMTP_USER") and os.environ.get("SMTP_PASS"))


def send_email(to, subject, body):
    user = os.environ["SMTP_USER"]
    msg = EmailMessage()
    msg["From"] = formataddr(
        (os.environ.get("FROM_NAME", ""), os.environ.get("FROM_EMAIL", user))
    )
    msg["To"] = to
    msg["Subject"] = subject
    msg.set_content(body)
    ctx = ssl.create_default_context()
    with smtplib.SMTP(
        os.environ.get("SMTP_HOST", "smtp.gmail.com"),
        int(os.environ.get("SMTP_PORT", 587)),
        timeout=30,
    ) as s:
        s.starttls(context=ctx)
        s.login(user, os.environ["SMTP_PASS"])
        s.send_message(msg)


def lead_view(r):
    d = dict(r)
    try:
        d["signals"] = json.loads(d.get("signals_json") or "{}")
    except Exception:
        d["signals"] = {}
    d.pop("signals_json", None)
    return d


class H(BaseHTTPRequestHandler):
    def _send(self, code, body, ctype="application/json"):
        b = body if isinstance(body, bytes) else body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.path == "/" or self.path.startswith("/index"):
            return self._send(200, PAGE, "text/html; charset=utf-8")
        if self.path == "/api/queue":
            c = store.connect()
            cnt = store.counts(c)
            payload = {
                "leads": [lead_view(r) for r in store.listall(c)],
                "total": sum(cnt.values()),
                "staged": cnt.get("staged", 0),
                "sent": cnt.get("sent", 0),
                "skipped": cnt.get("skipped", 0),
                "smtp": (
                    "configured"
                    if smtp_ready()
                    else "NOT configured (set SMTP_USER/SMTP_PASS)"
                ),
            }
            return self._send(200, json.dumps(payload))
        return self._send(404, json.dumps({"error": "not found"}))

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        data = json.loads(self.rfile.read(n) or b"{}")
        c = store.connect()
        rec = store.get(c, data.get("id"))
        if not rec:
            return self._send(404, json.dumps({"ok": False, "error": "id not found"}))
        if self.path == "/api/skip":
            store.update(c, rec["id"], status="skipped")
            return self._send(200, json.dumps({"ok": True}))
        if self.path == "/api/send":
            if not smtp_ready():
                return self._send(
                    200, json.dumps({"ok": False, "error": "SMTP not configured"})
                )
            to = data.get("to", rec["email"])
            subject = data.get("subject", rec["subject"])
            body = data.get("body", rec["body"])
            try:
                send_email(to, subject, body)
            except Exception as e:
                return self._send(200, json.dumps({"ok": False, "error": str(e)}))
            store.update(
                c,
                rec["id"],
                status="sent",
                email=to,
                subject=subject,
                body=body,
                sent_at=store.now(),
            )
            return self._send(200, json.dumps({"ok": True}))
        return self._send(404, json.dumps({"ok": False, "error": "not found"}))


if __name__ == "__main__":
    print(
        f"Review UI: http://{HOST}:{PORT}  (SMTP {'ready' if smtp_ready() else 'NOT configured - review only'})"
    )
    ThreadingHTTPServer((HOST, PORT), H).serve_forever()
