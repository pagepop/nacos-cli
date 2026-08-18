#!/usr/bin/env bash

set -Eeuo pipefail

readonly RELEASE_REPOSITORY="pagepop/nacos-cli"
readonly BINARY_NAME="nacos-cli"

work_dir=""
staged_binary=""

log_info() {
  printf '[INFO] %s\n' "$*" >&2
}

fail() {
  printf '[ERROR] %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Install a PagePop nacos-cli release.

Usage:
  install.sh [--version VERSION] [--install-dir ABSOLUTE_PATH]

Options:
  --version VERSION           Install an exact release, for example 1.1.4-pagepop.1.
                              Defaults to the most recently published release.
  --install-dir ABSOLUTE_PATH Install into this directory.
                              Defaults to $HOME/.nacos/bin.
  -h, --help                  Show this help.

Environment:
  NACOS_CLI_VERSION           Same as --version; the flag takes precedence.
  NACOS_CLI_INSTALL_DIR       Same as --install-dir; the flag takes precedence.
EOF
}

cleanup() {
  if [[ -n "$work_dir" ]]; then
    case "$work_dir" in
      /tmp/nacos-cli-install.*)
        rm -rf -- "$work_dir"
        ;;
      *)
        printf '[ERROR] Refusing to clean unexpected work directory: %s\n' "$work_dir" >&2
        ;;
    esac
  fi

  if [[ -n "$staged_binary" && -f "$staged_binary" ]]; then
    rm -f -- "$staged_binary"
  fi
}

require_command() {
  local command_name="$1"
  command -v "$command_name" >/dev/null 2>&1 || fail "Required command is unavailable: $command_name"
}

resolve_latest_version() {
  local latest_release_url="https://github.com/${RELEASE_REPOSITORY}/releases/latest"
  local resolved_release_url
  local expected_release_prefix="https://github.com/${RELEASE_REPOSITORY}/releases/tag/"
  local release_tag

  log_info "Resolving the latest published ${RELEASE_REPOSITORY} release"
  if ! resolved_release_url="$(
    curl --fail --silent --show-error --location --head \
      --output /dev/null \
      --write-out '%{url_effective}' \
      "$latest_release_url"
  )"; then
    fail "Unable to resolve the latest PagePop release"
  fi

  case "$resolved_release_url" in
    "${expected_release_prefix}"*)
      release_tag="${resolved_release_url#"${expected_release_prefix}"}"
      ;;
    *)
      fail "Unexpected latest-release URL: $resolved_release_url"
      ;;
  esac

  if [[ ! "$release_tag" =~ ^v[0-9A-Za-z][0-9A-Za-z._-]*$ ]]; then
    fail "Latest PagePop release does not contain a valid tag"
  fi

  printf '%s\n' "${release_tag#v}"
}

detect_platform() {
  local kernel_name
  local machine_name

  kernel_name="$(uname -s)"
  machine_name="$(uname -m)"

  case "$kernel_name" in
    Darwin)
      release_os="darwin"
      ;;
    Linux)
      release_os="linux"
      ;;
    *)
      fail "Unsupported operating system: $kernel_name"
      ;;
  esac

  case "$machine_name" in
    x86_64 | amd64)
      release_arch="amd64"
      ;;
    arm64 | aarch64)
      release_arch="arm64"
      ;;
    *)
      fail "Unsupported architecture: $machine_name"
      ;;
  esac

  log_info "Detected platform ${release_os}/${release_arch}"
}

verify_checksum() {
  local checksum_file="$1"
  local archive_file="$2"
  local archive_name="$3"
  local expected_checksum
  local actual_checksum

  expected_checksum="$(awk -v archive="$archive_name" '$2 == archive { print $1; exit }' "$checksum_file")"
  if [[ ! "$expected_checksum" =~ ^[[:xdigit:]]{64}$ ]]; then
    fail "checksums.txt does not contain a valid SHA-256 entry for $archive_name"
  fi

  case "$release_os" in
    darwin)
      actual_checksum="$(shasum -a 256 "$archive_file" | awk '{ print $1 }')"
      ;;
    linux)
      actual_checksum="$(sha256sum "$archive_file" | awk '{ print $1 }')"
      ;;
    *)
      fail "Checksum verification is not implemented for $release_os"
      ;;
  esac

  if [[ "$actual_checksum" != "$expected_checksum" ]]; then
    fail "SHA-256 checksum mismatch for $archive_name"
  fi

  log_info "Verified SHA-256 checksum for $archive_name"
}

requested_version="${NACOS_CLI_VERSION:-}"
requested_install_dir="${NACOS_CLI_INSTALL_DIR:-}"

while (( $# > 0 )); do
  case "$1" in
    --version)
      (( $# >= 2 )) || fail "--version requires a value"
      requested_version="$2"
      shift 2
      ;;
    --install-dir)
      (( $# >= 2 )) || fail "--install-dir requires a value"
      requested_install_dir="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      fail "Unknown argument: $1"
      ;;
  esac
done

require_command awk
require_command curl
require_command install
require_command mktemp
require_command mv
require_command tar
require_command uname

if [[ -z "$requested_install_dir" ]]; then
  [[ -n "${HOME:-}" ]] || fail "HOME is unset; pass --install-dir explicitly"
  requested_install_dir="${HOME}/.nacos/bin"
fi

if [[ "$requested_install_dir" != /* ]]; then
  fail "Installation directory must be an absolute path: $requested_install_dir"
fi

if [[ -z "$requested_version" ]]; then
  requested_version="$(resolve_latest_version)"
fi

release_version="${requested_version#v}"
if [[ ! "$release_version" =~ ^[0-9A-Za-z][0-9A-Za-z._-]*$ ]]; then
  fail "Invalid release version: $requested_version"
fi
release_tag="v${release_version}"

detect_platform
case "$release_os" in
  darwin)
    require_command shasum
    ;;
  linux)
    require_command sha256sum
    ;;
esac

archive_name="${BINARY_NAME}-${release_version}-${release_os}-${release_arch}.tar.gz"
release_base_url="https://github.com/${RELEASE_REPOSITORY}/releases/download/${release_tag}"

work_dir="$(mktemp -d /tmp/nacos-cli-install.XXXXXX)"
archive_path="${work_dir}/${archive_name}"
checksum_path="${work_dir}/checksums.txt"
extract_dir="${work_dir}/extract"

trap cleanup EXIT

log_info "Downloading ${RELEASE_REPOSITORY} ${release_tag}"
curl --fail --silent --show-error --location \
  --output "$archive_path" \
  "${release_base_url}/${archive_name}"
curl --fail --silent --show-error --location \
  --output "$checksum_path" \
  "${release_base_url}/checksums.txt"

verify_checksum "$checksum_path" "$archive_path" "$archive_name"

mkdir -p "$extract_dir"
# Extract only the expected executable instead of trusting unrelated archive entries.
tar -xzf "$archive_path" -C "$extract_dir" "$BINARY_NAME"
downloaded_binary="${extract_dir}/${BINARY_NAME}"
if [[ -L "$downloaded_binary" || ! -f "$downloaded_binary" ]]; then
  fail "Release archive does not contain a regular ${BINARY_NAME} executable"
fi

chmod 0755 "$downloaded_binary"
if ! downloaded_version="$("$downloaded_binary" --version 2>&1)"; then
  fail "Downloaded binary failed its version check: $downloaded_version"
fi
case "$downloaded_version" in
  "${BINARY_NAME} version ${release_version}"*)
    ;;
  *)
    fail "Downloaded binary version does not match ${release_tag}: $downloaded_version"
    ;;
esac
log_info "Verified executable: $downloaded_version"

mkdir -p "$requested_install_dir"
target_binary="${requested_install_dir}/${BINARY_NAME}"
if [[ -L "$target_binary" ]]; then
  fail "Refusing to replace symbolic link: $target_binary"
fi
if [[ -e "$target_binary" && ! -f "$target_binary" ]]; then
  fail "Installation target is not a regular file: $target_binary"
fi

staged_binary="${requested_install_dir}/.${BINARY_NAME}.install.$$"
install -m 0755 "$downloaded_binary" "$staged_binary"

if [[ -f "$target_binary" ]]; then
  backup_dir="$(mktemp -d /tmp/nacos-cli-backup.XXXXXX)"
  backup_binary="${backup_dir}/${BINARY_NAME}"
  mv -- "$target_binary" "$backup_binary"
  log_info "Moved the previous binary to $backup_binary"
fi

if ! mv -- "$staged_binary" "$target_binary"; then
  fail "Unable to install $target_binary"
fi
staged_binary=""

log_info "Installed ${BINARY_NAME} ${release_tag} at $target_binary"
case ":${PATH:-}:" in
  *":${requested_install_dir}:"*)
    ;;
  *)
    log_info "Add ${requested_install_dir} to PATH before invoking ${BINARY_NAME} by name"
    ;;
esac
