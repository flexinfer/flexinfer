# Pre-built Python 3.13 base image for devbox sandboxes.
# Built by scripts/build-base-images.sh and pushed to Harbor.
FROM python:3.13-slim-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    git make curl build-essential ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Common Python tools
RUN pip install --no-cache-dir uv poetry black pytest

WORKDIR /workspace
CMD ["sleep", "infinity"]
