#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_BASE="${TMPDIR:-/tmp}"
TMP="$(mktemp -d "${TMP_BASE%/}/herdr-last-install-test.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux) OS="linux" ;;
  *) echo "unsupported test OS" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "unsupported test architecture" >&2; exit 1 ;;
esac
ASSET="herdr-last-${OS}-${ARCH}"

fail() {
  echo "install test failed: $*" >&2
  exit 1
}

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

new_checkout() {
  local name="$1"
  local checkout="${TMP}/${name}"
  mkdir -p "${checkout}/scripts"
  cp "${ROOT}/scripts/install.sh" "${checkout}/scripts/install.sh"
  cp "${ROOT}/herdr-plugin.toml" "${checkout}/herdr-plugin.toml"
  printf '%s\n' "${checkout}"
}

assert_preserved() {
  local expected="$1"
  local actual="$2"
  cmp "${expected}" "${actual}" || fail "existing binary contents changed"
  [[ -x "${actual}" ]] || fail "existing binary lost executable mode"
}

MOCK_BIN="${TMP}/mock-bin"
mkdir -p "${MOCK_BIN}"
cat > "${MOCK_BIN}/curl" <<'MOCK_CURL'
#!/bin/sh
if [ "${MOCK_CURL_MODE:-copy}" = "fail" ]; then
  exit 22
fi
url=""
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    http://*|https://*) url="$1" ;;
    --output) shift; output="$1" ;;
  esac
  shift
done
printf '%s\n' "$url" >> "$MOCK_URL_LOG"
case "$url" in
  */SHA256SUMS) cp "$MOCK_SUMS" "$output" ;;
  *) cp "$MOCK_ASSET" "$output" ;;
esac
MOCK_CURL
chmod +x "${MOCK_BIN}/curl"

cat > "${MOCK_BIN}/go" <<'MOCK_GO'
#!/bin/sh
case "${MOCK_GO_MODE:-fail}" in
  success) version="0.1.0" ;;
  mismatch) version="9.9.9" ;;
  *) exit 1 ;;
esac
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output="$1"
  fi
  shift
done
[ -n "$output" ] || exit 2
cat > "$output" <<BINARY
#!/bin/sh
[ "\${1:-}" = "version" ] && printf '%s\\n' 'herdr-last v${version}'
BINARY
chmod +x "$output"
MOCK_GO
chmod +x "${MOCK_BIN}/go"

FIXTURE="${TMP}/fixture-binary"
cat > "${FIXTURE}" <<'FIXTURE_BINARY'
#!/bin/sh
[ "${1:-}" = "version" ] && printf '%s\n' 'herdr-last v0.1.0'
FIXTURE_BINARY
chmod 0644 "${FIXTURE}"
MISMATCH_FIXTURE="${TMP}/mismatch-binary"
cat > "${MISMATCH_FIXTURE}" <<'MISMATCH_BINARY'
#!/bin/sh
[ "${1:-}" = "version" ] && printf '%s\n' 'herdr-last v9.9.9'
MISMATCH_BINARY
chmod 0644 "${MISMATCH_FIXTURE}"
VALID_SUMS="${TMP}/valid-sums"
printf '%s  %s\n' "$(checksum "${FIXTURE}")" "${ASSET}" > "${VALID_SUMS}"
MISMATCH_SUMS="${TMP}/mismatch-sums"
printf '%s  %s\n' "$(checksum "${MISMATCH_FIXTURE}")" "${ASSET}" > "${MISMATCH_SUMS}"
INVALID_SUMS="${TMP}/invalid-sums"
printf '%064d  %s\n' 0 "${ASSET}" > "${INVALID_SUMS}"
SYSTEM_PATH="${MOCK_BIN}:/usr/bin:/bin:/usr/sbin:/sbin"
BASE_URL="https://example.invalid/lmilojevicc/herdr-last/releases/download"

ATOMIC_MV_BIN="${TMP}/atomic-mv-bin"
mkdir -p "${ATOMIC_MV_BIN}"
cat > "${ATOMIC_MV_BIN}/mv" <<'MOCK_MV'
#!/bin/sh
if [ "$#" -eq 2 ] && [ "$2" = "${MOCK_MV_EXPECT_TARGET:-}" ]; then
  [ -e "$2" ] || {
    echo "atomic replacement check failed: destination disappeared before mv" >&2
    exit 97
  }
  printf '%s\n' "$2" >> "$MOCK_MV_LOG"
  if [ "${MOCK_MV_MODE:-pass}" = "fail" ]; then
    exit 98
  fi
fi
exec "$MOCK_REAL_MV" "$@"
MOCK_MV
chmod +x "${ATOMIC_MV_BIN}/mv"
REAL_MV="$(command -v mv)"

valid_checkout="$(new_checkout valid)"
valid_log="${TMP}/valid-urls"
env PATH="${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=copy MOCK_URL_LOG="${valid_log}" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" \
  MOCK_GO_MODE=fail \
  bash "${valid_checkout}/scripts/install.sh"
cmp "${FIXTURE}" "${valid_checkout}/bin/herdr-last"
[[ -x "${valid_checkout}/bin/herdr-last" ]] || fail "installed release is not executable"
grep -Fx "${BASE_URL}/v0.1.0/${ASSET}" "${valid_log}" >/dev/null
grep -Fx "${BASE_URL}/v0.1.0/SHA256SUMS" "${valid_log}" >/dev/null
if grep -F '/latest/' "${valid_log}" >/dev/null; then
  fail "installer requested an unpinned release"
fi

old_binary="${TMP}/old-binary"
printf '#!/bin/sh\necho old working binary\n' > "${old_binary}"
chmod 0755 "${old_binary}"

release_replace_checkout="$(new_checkout release-replacement)"
mkdir -p "${release_replace_checkout}/bin"
cp -p "${old_binary}" "${release_replace_checkout}/bin/herdr-last"
release_replace_log="${TMP}/release-replacement-mv"
env PATH="${ATOMIC_MV_BIN}:${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=copy MOCK_URL_LOG="${TMP}/release-replacement-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" MOCK_GO_MODE=fail \
  MOCK_MV_EXPECT_TARGET="${release_replace_checkout}/bin/herdr-last" \
  MOCK_MV_LOG="${release_replace_log}" MOCK_REAL_MV="${REAL_MV}" \
  bash "${release_replace_checkout}/scripts/install.sh"
cmp "${FIXTURE}" "${release_replace_checkout}/bin/herdr-last"
[[ -x "${release_replace_checkout}/bin/herdr-last" ]] || fail "release replacement is not executable"
grep -Fx "${release_replace_checkout}/bin/herdr-last" "${release_replace_log}" >/dev/null || fail "release replacement did not exercise atomic mv assertion"

release_mv_failure_checkout="$(new_checkout release-mv-failure)"
mkdir -p "${release_mv_failure_checkout}/bin"
cp -p "${old_binary}" "${release_mv_failure_checkout}/bin/herdr-last"
release_mv_failure_expected="${TMP}/release-mv-failure-expected"
cp -p "${release_mv_failure_checkout}/bin/herdr-last" "${release_mv_failure_expected}"
release_mv_failure_output="${TMP}/release-mv-failure-output"
if env PATH="${ATOMIC_MV_BIN}:${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=copy MOCK_URL_LOG="${TMP}/release-mv-failure-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" MOCK_GO_MODE=fail \
  MOCK_MV_EXPECT_TARGET="${release_mv_failure_checkout}/bin/herdr-last" \
  MOCK_MV_LOG="${TMP}/release-mv-failure-log" MOCK_MV_MODE=fail MOCK_REAL_MV="${REAL_MV}" \
  bash "${release_mv_failure_checkout}/scripts/install.sh" >"${release_mv_failure_output}" 2>&1; then
  fail "release replacement mv failure unexpectedly succeeded"
fi
assert_preserved "${release_mv_failure_expected}" "${release_mv_failure_checkout}/bin/herdr-last"
if grep -F "Installed herdr-last" "${release_mv_failure_output}" >/dev/null; then
  fail "release replacement mv failure printed success"
fi

invalid_checkout="$(new_checkout invalid)"
if env PATH="${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=copy MOCK_URL_LOG="${TMP}/invalid-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${INVALID_SUMS}" \
  MOCK_GO_MODE=fail \
  bash "${invalid_checkout}/scripts/install.sh"; then
  fail "invalid checksum unexpectedly succeeded"
fi
[[ ! -e "${invalid_checkout}/bin/herdr-last" ]] || fail "invalid checksum installed a binary"

failed_checkout="$(new_checkout failed-reinstall)"
mkdir -p "${failed_checkout}/bin"
cp -p "${old_binary}" "${failed_checkout}/bin/herdr-last"
if env PATH="${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=fail MOCK_URL_LOG="${TMP}/failed-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" \
  MOCK_GO_MODE=fail \
  bash "${failed_checkout}/scripts/install.sh"; then
  fail "total installation failure unexpectedly succeeded"
fi
assert_preserved "${old_binary}" "${failed_checkout}/bin/herdr-last"

mismatch_checkout="$(new_checkout version-mismatch)"
mkdir -p "${mismatch_checkout}/bin"
cp -p "${old_binary}" "${mismatch_checkout}/bin/herdr-last"
if env PATH="${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=copy MOCK_URL_LOG="${TMP}/mismatch-urls" \
  MOCK_ASSET="${MISMATCH_FIXTURE}" MOCK_SUMS="${MISMATCH_SUMS}" \
  MOCK_GO_MODE=fail \
  bash "${mismatch_checkout}/scripts/install.sh"; then
  fail "version-mismatched release unexpectedly succeeded"
fi
assert_preserved "${old_binary}" "${mismatch_checkout}/bin/herdr-last"

source_mismatch_checkout="$(new_checkout source-version-mismatch)"
mkdir -p "${source_mismatch_checkout}/bin"
cp -p "${old_binary}" "${source_mismatch_checkout}/bin/herdr-last"
if env PATH="${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=fail MOCK_URL_LOG="${TMP}/source-mismatch-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" \
  MOCK_GO_MODE=mismatch \
  bash "${source_mismatch_checkout}/scripts/install.sh"; then
  fail "version-mismatched source build unexpectedly succeeded"
fi
assert_preserved "${old_binary}" "${source_mismatch_checkout}/bin/herdr-last"

fallback_checkout="$(new_checkout fallback)"
env PATH="${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=fail MOCK_URL_LOG="${TMP}/fallback-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" \
  MOCK_GO_MODE=success \
  bash "${fallback_checkout}/scripts/install.sh"
[[ "$("${fallback_checkout}/bin/herdr-last" version)" = "herdr-last v0.1.0" ]] || fail "fallback version mismatch"

fallback_replace_checkout="$(new_checkout fallback-replacement)"
mkdir -p "${fallback_replace_checkout}/bin"
cp -p "${old_binary}" "${fallback_replace_checkout}/bin/herdr-last"
fallback_replace_log="${TMP}/fallback-replacement-mv"
env PATH="${ATOMIC_MV_BIN}:${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=fail MOCK_URL_LOG="${TMP}/fallback-replacement-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" MOCK_GO_MODE=success \
  MOCK_MV_EXPECT_TARGET="${fallback_replace_checkout}/bin/herdr-last" \
  MOCK_MV_LOG="${fallback_replace_log}" MOCK_REAL_MV="${REAL_MV}" \
  bash "${fallback_replace_checkout}/scripts/install.sh"
[[ "$("${fallback_replace_checkout}/bin/herdr-last" version)" = "herdr-last v0.1.0" ]] || fail "fallback replacement version mismatch"
grep -Fx "${fallback_replace_checkout}/bin/herdr-last" "${fallback_replace_log}" >/dev/null || fail "fallback replacement did not exercise atomic mv assertion"

fallback_mv_failure_checkout="$(new_checkout fallback-mv-failure)"
mkdir -p "${fallback_mv_failure_checkout}/bin"
cp -p "${old_binary}" "${fallback_mv_failure_checkout}/bin/herdr-last"
fallback_mv_failure_output="${TMP}/fallback-mv-failure-output"
if env PATH="${ATOMIC_MV_BIN}:${SYSTEM_PATH}" \
  HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
  MOCK_CURL_MODE=fail MOCK_URL_LOG="${TMP}/fallback-mv-failure-urls" \
  MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" MOCK_GO_MODE=success \
  MOCK_MV_EXPECT_TARGET="${fallback_mv_failure_checkout}/bin/herdr-last" \
  MOCK_MV_LOG="${TMP}/fallback-mv-failure-log" MOCK_MV_MODE=fail MOCK_REAL_MV="${REAL_MV}" \
  bash "${fallback_mv_failure_checkout}/scripts/install.sh" >"${fallback_mv_failure_output}" 2>&1; then
  fail "source replacement mv failure unexpectedly succeeded"
fi
assert_preserved "${old_binary}" "${fallback_mv_failure_checkout}/bin/herdr-last"
if grep -F "Built herdr-last" "${fallback_mv_failure_output}" >/dev/null; then
  fail "source replacement mv failure printed success"
fi

if command -v shasum >/dev/null 2>&1; then
  WGET_BIN="${TMP}/wget-bin"
  mkdir -p "${WGET_BIN}"
  for command_name in awk chmod cp dirname mkdir mktemp mv rm uname; do
    ln -s "$(command -v "${command_name}")" "${WGET_BIN}/${command_name}"
  done
  ln -s "$(command -v shasum)" "${WGET_BIN}/shasum"
  cat > "${WGET_BIN}/wget" <<'MOCK_WGET'
#!/bin/sh
url=""
output=""
for argument in "$@"; do
  case "$argument" in
    --output-document=*) output="${argument#*=}" ;;
    http://*|https://*) url="$argument" ;;
  esac
done
printf '%s\n' "$url" >> "$MOCK_URL_LOG"
case "$url" in
  */SHA256SUMS) cp "$MOCK_SUMS" "$output" ;;
  *) cp "$MOCK_ASSET" "$output" ;;
esac
MOCK_WGET
  chmod +x "${WGET_BIN}/wget"

  wget_checkout="$(new_checkout wget-shasum)"
  env PATH="${WGET_BIN}" \
    HERDR_LAST_RELEASE_BASE_URL="${BASE_URL}" \
    MOCK_URL_LOG="${TMP}/wget-urls" MOCK_ASSET="${FIXTURE}" MOCK_SUMS="${VALID_SUMS}" \
    /bin/bash "${wget_checkout}/scripts/install.sh"
  cmp "${FIXTURE}" "${wget_checkout}/bin/herdr-last"
  [[ -x "${wget_checkout}/bin/herdr-last" ]] || fail "wget-installed release is not executable"
else
  echo "shasum unavailable; wget + shasum branch not exercised" >&2
fi

echo "install tests passed"
