#!/usr/bin/env bash
# Slice 0 one-command run: port-forward FlexInfer proxy -> run pipeline -> open review UI.
# Requires: on-LAN, TAVILY_API_KEY in env. SMTP_USER/SMTP_PASS in env to enable Send.
set -euo pipefail
cd "$(dirname "$0")"

# Load pilot-local secrets (gitignored). Put the fresh Tavily key in ./.env (see .env.example).
if [ -f ./.env ]; then
  set -a; . ./.env; set +a
  echo "[run] loaded ./.env"
fi

export KUBECONFIG="${KUBECONFIG:-$HOME/workspace/platform/gitops/.kube/k3s.yaml}"
export LLM_BASE_URL="${LLM_BASE_URL:-http://127.0.0.1:8088/v1}"
export LLM_MODEL="${LLM_MODEL:-gemma4-26b-a4b-gptq}"
: "${TAVILY_API_KEY:?set TAVILY_API_KEY}"

echo "[run] port-forwarding flexinfer-proxy -> 127.0.0.1:8088"
kubectl port-forward -n flexinfer-system svc/flexinfer-proxy 8088:80 >/tmp/gtm-pf.log 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
sleep 4

echo "[run] warming model (cold-start may take ~1-2 min)..."
curl -sS -m 180 http://127.0.0.1:8088/v1/chat/completions \
  -H 'Content-Type: application/json' -H 'Authorization: Bearer none' \
  -d "{\"model\":\"$LLM_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"ok\"}],\"max_tokens\":1}" >/dev/null || true

echo "[run] pipeline (source -> verify email -> score -> draft -> stage)"
python3 pipeline.py "$@"

echo "[run] starting review UI (Ctrl-C to stop). Port-forward stays up while this runs."
python3 server.py
