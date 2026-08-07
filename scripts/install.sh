#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_DIR="$(CDPATH= cd -- "${SCRIPT_DIR}/.." && pwd)"
MANIFEST="${PLUGIN_DIR}/herdr-plugin.toml"
BIN_DIR="${PLUGIN_DIR}/bin"
TARGET="${BIN_DIR}/herdr-last"
REPO="lmilojevicc/herdr-last"
RELEASE_BASE_URL="${HERDR_LAST_RELEASE_BASE_URL:-https://github.com/${REPO}/releases/download}"

VERSION="$(awk -F ' *= *' '$1 == "version" { gsub(/"/, "", $2); print $2; exit }' "${MANIFEST}")"
if [[ -z "${VERSION}" ]]; then
  echo "Error: Could not read the plugin version from ${MANIFEST}." >&2
  exit 1
fi
TAG="v${VERSION}"

case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux) OS="linux" ;;
  *) echo "Error: Unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Error: Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

ASSET="herdr-last-${OS}-${ARCH}"
RELEASE_URL="${RELEASE_BASE_URL}/${TAG}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/herdr-last.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

mkdir -p "${BIN_DIR}"
rm -f "${TARGET}.tmp"

download_file() {
  local url="$1"
  local output="$2"
  if command -v curl >/dev/null 2>&1; then
    curl --fail --silent --show-error --location "${url}" --output "${output}"
  elif command -v wget >/dev/null 2>&1; then
    wget --quiet --output-document="${output}" "${url}"
  else
    echo "Neither curl nor wget is available." >&2
    return 1
  fi
}

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "Neither sha256sum nor shasum is available." >&2
    return 1
  fi
}

install_release() {
  local asset_path="${TMP_DIR}/${ASSET}"
  local sums_path="${TMP_DIR}/SHA256SUMS"
  local expected actual

  download_file "${RELEASE_URL}/${ASSET}" "${asset_path}" || return 1
  download_file "${RELEASE_URL}/SHA256SUMS" "${sums_path}" || return 1

  expected="$(awk -v asset="${ASSET}" '$2 == asset { print $1; exit }' "${sums_path}")"
  if [[ -z "${expected}" ]]; then
    echo "Checksum for ${ASSET} is missing from SHA256SUMS." >&2
    return 1
  fi
  actual="$(file_sha256 "${asset_path}")" || return 1
  if [[ "${actual}" != "${expected}" ]]; then
    echo "Checksum verification failed for ${ASSET}." >&2
    return 1
  fi

  cp "${asset_path}" "${TARGET}.tmp" || return 1
  chmod 0755 "${TARGET}.tmp" || return 1
  if [[ "$("${TARGET}.tmp" version 2>/dev/null)" != "herdr-last v${VERSION}" ]]; then
    echo "Version verification failed for ${ASSET}." >&2
    rm -f "${TARGET}.tmp"
    return 1
  fi
  mv "${TARGET}.tmp" "${TARGET}" || return 1
  echo "Installed herdr-last ${VERSION} for ${OS}/${ARCH}."
}

if install_release; then
  exit 0
fi

echo "Prebuilt release unavailable or invalid; building herdr-last from source." >&2
if ! command -v go >/dev/null 2>&1; then
  echo "Error: Could not install a verified release and Go is not installed." >&2
  exit 1
fi

(
  cd "${PLUGIN_DIR}"
  go build -trimpath -ldflags="-s -w" -o "${TARGET}.tmp" .
)
chmod 0755 "${TARGET}.tmp"
if [[ "$("${TARGET}.tmp" version 2>/dev/null)" != "herdr-last v${VERSION}" ]]; then
  echo "Error: Source build version does not match herdr-last v${VERSION}." >&2
  rm -f "${TARGET}.tmp"
  exit 1
fi
if ! mv "${TARGET}.tmp" "${TARGET}"; then
  echo "Error: Could not replace ${TARGET} with the source build." >&2
  exit 1
fi
echo "Built herdr-last ${VERSION} from source."
