#!/bin/bash
# faster-whisper large-v3 int8 CPU reference, same image/env as gitops k3s/ai/transcription (cpu limit 8)
set -u
cd ~/build/ktasr-2026-10-30
docker rm -f ktasr-fw >/dev/null 2>&1
docker run -d --name ktasr-fw --cpus 8 -p 127.0.0.1:18000:8000 \
  -e WHISPER__MODEL=Systran/faster-whisper-large-v3 -e WHISPER__INFERENCE_DEVICE=cpu -e WHISPER__COMPUTE_TYPE=int8 \
  -v ktasr-hf:/root/.cache/huggingface \
  docker.io/fedirz/faster-whisper-server@sha256:760e5e43d427dc6cfbbc4731934b908b7de9c7e6d5309c6a1f0c8c923a5b6030
for i in $(seq 1 120); do curl -sf http://127.0.0.1:18000/health >/dev/null && break; sleep 5; done
echo "health ok after ~$((i*5))s"
for f in twospk twospk meeting10 meeting60; do
  t=$(curl -s -o ref/$f.fw.json -w "%{http_code} %{time_total}" -F file=@fixtures/$f.wav -F model=Systran/faster-whisper-large-v3 \
      -F response_format=verbose_json -F "timestamp_granularities[]=word" http://127.0.0.1:18000/v1/audio/transcriptions)
  echo "$f $t"
done
docker logs ktasr-fw 2>&1 | tail -5
docker rm -f ktasr-fw >/dev/null
