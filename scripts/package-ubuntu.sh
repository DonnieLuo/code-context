#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${root_dir}/dist"
package_name="code-context-linux-amd64"
stage_root="$(mktemp -d "${TMPDIR:-/tmp}/code-context-package.XXXXXX")"
stage_dir="${stage_root}/${package_name}"
trap 'rm -rf "${stage_root}"' EXIT

mkdir -p "${stage_dir}"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "${stage_dir}/code-context" "${root_dir}/cmd/code-context"
cp "${root_dir}/config.yaml" "${root_dir}/README.md" "${stage_dir}/"
mkdir -p "${output_dir}"
tar -C "${stage_root}" -czf "${output_dir}/${package_name}.tar.gz" "${package_name}"

printf 'Created %s\n' "${output_dir}/${package_name}.tar.gz"
