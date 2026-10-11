# Build context: exact Git archive of the manifest's Hono revision.
# Preparation uses network in disposable Docker; tests run separately offline.
FROM node:20-bookworm-slim@sha256:2cf067cfed83d5ea958367df9f966191a942351a2df77d6f0193e162b5febfc0
RUN apt-get update && apt-get install -y --no-install-recommends python3 git time ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/hono
COPY package.json yarn.lock ./
RUN yarn install --frozen-lockfile --ignore-scripts --non-interactive
# esbuild installation script only, inside the image; never on the host.
RUN node node_modules/esbuild/install.js
ENV PATH=/opt/hono/node_modules/.bin:$PATH
