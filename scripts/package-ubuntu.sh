#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${root_dir}/dist"
output_file="${output_dir}/code-context-amd64"

# Keep dist deterministic for the Go service. CodeGraph's separately versioned
# engine can be bundled when its verified release binary is supplied.
rm -rf "${output_dir}"
mkdir -p "${output_dir}"
embedded_config="$(base64 < "${root_dir}/config.yaml" | tr -d '\n')"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.embeddedConfig=${embedded_config}" \
  -o "${output_file}" \
  "${root_dir}/cmd/code-context"

printf 'Created %s\n' "${output_file}"

if [[ -n "${CODEGRAPH_ENGINE_BIN:-}" ]]; then
  : "${CODEGRAPH_ENGINE_SHA256:?set the official release SHA-256 for CODEGRAPH_ENGINE_BIN}"
  if command -v sha256sum >/dev/null 2>&1; then
    actual_sha="$(sha256sum "${CODEGRAPH_ENGINE_BIN}" | cut -d ' ' -f 1)"
  else
    actual_sha="$(shasum -a 256 "${CODEGRAPH_ENGINE_BIN}" | cut -d ' ' -f 1)"
  fi
  if [[ "${actual_sha}" != "${CODEGRAPH_ENGINE_SHA256}" ]]; then
    printf 'CodeGraph engine checksum mismatch\n' >&2
    exit 1
  fi
  cp "${CODEGRAPH_ENGINE_BIN}" "${output_dir}/codegraph-server"
  chmod 755 "${output_dir}/codegraph-server"
  cat > "${output_dir}/codegraph-mcp" <<'WRAPPER'
#!/usr/bin/env bash
set -euo pipefail
exec "$(dirname "$0")/codegraph-server" --mcp "$@"
WRAPPER
  chmod 755 "${output_dir}/codegraph-mcp"
  printf 'Bundled verified CodeGraph engine in %s\n' "${output_dir}"
fi
