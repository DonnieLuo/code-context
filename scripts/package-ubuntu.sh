#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${root_dir}/dist"
package_dir="${output_dir}"

mkdir -p "${output_dir}"
rm -rf "${package_dir}"
mkdir -p "${package_dir}"
embedded_config="$(base64 < "${root_dir}/config.yaml" | tr -d '\n')"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.embeddedConfig=${embedded_config}" -o "${package_dir}/code-context-amd64" "${root_dir}/cmd/code-context-amd64"

printf 'Created %s\n' "${package_dir}"
