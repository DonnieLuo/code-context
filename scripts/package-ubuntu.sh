#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${root_dir}/dist"
output_file="${output_dir}/code-context-amd64"

# Keep dist deterministic: the Ubuntu package consists of exactly one binary.
rm -rf "${output_dir}"
mkdir -p "${output_dir}"
embedded_config="$(base64 < "${root_dir}/config.yaml" | tr -d '\n')"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.embeddedConfig=${embedded_config}" \
  -o "${output_file}" \
  "${root_dir}/cmd/code-context"

printf 'Created %s\n' "${output_file}"
