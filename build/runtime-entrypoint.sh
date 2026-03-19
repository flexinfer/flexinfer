#!/bin/sh
# Runtime entrypoint: source profile env vars, then exec the requested command.
# The profile env file is baked into the image by build/build-runtime.sh.
set -e

if [ -f /etc/flexinfer/runtime.env ]; then
    set -a
    . /etc/flexinfer/runtime.env
    set +a
fi

case "$1" in
    flexinfer-runtime)
        shift
        exec flexinfer-runtime --gpu-vendor "${GPU_VENDOR}" --gpu-arch "${GPU_ARCH}" "$@"
        ;;
    *)
        exec "$@"
        ;;
esac
