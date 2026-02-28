# Pre-built Node.js 20 base image for devbox sandboxes.
# Built by scripts/build-base-images.sh and pushed to Harbor.
FROM node:20-alpine

RUN apk add --no-cache git make bash curl

# Enable corepack for pnpm/yarn support
RUN corepack enable && corepack prepare pnpm@latest --activate

WORKDIR /workspace
CMD ["sleep", "infinity"]
