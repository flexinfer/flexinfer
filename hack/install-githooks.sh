#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

git config core.hooksPath .githooks
echo "Installed git hooks to use .githooks (core.hooksPath=.githooks)"
echo "Hooks:"
ls -1 .githooks

