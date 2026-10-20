#!/bin/sh
# Compile l'agent de seedbox (cmd/d3x-agent) pour Linux amd64 et arm64 et le
# range, compressé, là où D3X l'embarque. Usage : sh scripts/build-agents.sh [version]
set -e
v="${1:-dev}"
out="internal/remote/agents"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for arch in amd64 arm64; do
  GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -buildid= -X main.version=$v" -o "$tmp/d3x-agent-linux-$arch" ./cmd/d3x-agent
  gzip -9 -n -f "$tmp/d3x-agent-linux-$arch"
done
rm -f "$out"/*.gz
mv "$tmp"/*.gz "$out"/
ls -l "$out"
