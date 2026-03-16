# Troubleshooting

## "No module named browser_kit" / "No module named playwright"

Install Python deps:

```bash
pip install flexinfer-browser-kit playwright
```

## "Executable doesn't exist" / Chromium won't launch

Install Playwright browsers:

```bash
python3 -m playwright install chromium
```

## Screenshots Are Blank / Missing Data

- Try `wait_until: "networkidle"` and a small `wait_ms` (e.g. 200-500ms).
- If a CSS selector is present, make sure it's visible in the current viewport, or omit `selector`.

## Corporate DNS / SSL / Proxies

BrowserKit runs locally; use host network configuration. For internal services, prefer `http://localhost:...` or your VPN DNS name.
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
