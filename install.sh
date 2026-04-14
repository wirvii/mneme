#!/bin/sh
# install.sh — Install mneme from GitHub Releases.
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/wirvii/mneme/main/install.sh | sh
#
# Environment variables:
#   VERSION     Install a specific version (without "v" prefix). Default: latest.
#   INSTALL_DIR Override the installation directory. Default: /usr/local/bin or
#               ~/.local/bin when /usr/local/bin is not writable.
#
# Requirements: curl, sha256sum (Linux) or shasum (macOS), tar.

set -eu

REPO="wirvii/mneme"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"
GITHUB_BASE="https://github.com/${REPO}/releases/download"

# --- cleanup trap -----------------------------------------------------------

TMPDIR_CREATED=""
cleanup() {
    if [ -n "${TMPDIR_CREATED}" ] && [ -d "${TMPDIR_CREATED}" ]; then
        rm -rf "${TMPDIR_CREATED}"
    fi
}
trap cleanup EXIT INT TERM

# --- detect OS and arch -----------------------------------------------------

detect_os() {
    _raw=$(uname -s)
    case "${_raw}" in
        Linux)  echo "linux" ;;
        Darwin) echo "darwin" ;;
        *)
            echo "error: unsupported OS: ${_raw}" >&2
            exit 1
            ;;
    esac
}

detect_arch() {
    _raw=$(uname -m)
    case "${_raw}" in
        x86_64)         echo "amd64" ;;
        arm64|aarch64)  echo "arm64" ;;
        *)
            echo "error: unsupported architecture: ${_raw}" >&2
            exit 1
            ;;
    esac
}

OS=$(detect_os)
ARCH=$(detect_arch)

# --- resolve version --------------------------------------------------------

if [ -z "${VERSION:-}" ]; then
    printf "Fetching latest release...\n"
    _response=$(curl -sfL "${GITHUB_API}" 2>/dev/null) || {
        echo "error: failed to fetch release info from GitHub API." >&2
        echo "       Check your internet connection and try again." >&2
        exit 1
    }
    # Parse tag_name without jq. The GitHub API response is stable JSON.
    VERSION=$(printf '%s' "${_response}" \
        | grep -o '"tag_name": *"[^"]*"' \
        | cut -d'"' -f4 \
        | sed 's/^v//')
    if [ -z "${VERSION}" ]; then
        echo "error: could not parse version from GitHub API response." >&2
        exit 1
    fi
fi

printf "Installing mneme v%s (%s/%s)...\n" "${VERSION}" "${OS}" "${ARCH}"

# --- build URLs -------------------------------------------------------------

ARCHIVE="mneme-${VERSION}-${OS}-${ARCH}.tar.gz"
TAG="v${VERSION}"
ARCHIVE_URL="${GITHUB_BASE}/${TAG}/${ARCHIVE}"
CHECKSUM_URL="${ARCHIVE_URL}.sha256"

# --- download ---------------------------------------------------------------

TMPDIR_CREATED=$(mktemp -d)
ARCHIVE_PATH="${TMPDIR_CREATED}/${ARCHIVE}"
CHECKSUM_PATH="${ARCHIVE_PATH}.sha256"

printf "  Downloading %s...\n" "${ARCHIVE}"
curl -fSL -o "${ARCHIVE_PATH}" "${ARCHIVE_URL}" || {
    echo "error: download failed. URL: ${ARCHIVE_URL}" >&2
    exit 1
}

printf "  Downloading checksum...\n"
curl -fSL -o "${CHECKSUM_PATH}" "${CHECKSUM_URL}" || {
    echo "error: checksum download failed. URL: ${CHECKSUM_URL}" >&2
    exit 1
}

# --- verify checksum --------------------------------------------------------

printf "  Verifying checksum...\n"
# sha256sum on Linux, shasum on macOS.
# We change into the tmp dir so sha256sum can find the file by basename.
if command -v sha256sum >/dev/null 2>&1; then
    # sha256sum expects "<hash>  <filename>" — the file we downloaded has the
    # archive filename from the release pipeline, not the local path, so we
    # rewrite the checksum file to use the basename.
    _hash=$(awk '{print $1}' "${CHECKSUM_PATH}")
    printf '%s  %s\n' "${_hash}" "${ARCHIVE}" > "${TMPDIR_CREATED}/verify.sha256"
    (cd "${TMPDIR_CREATED}" && sha256sum -c "verify.sha256" >/dev/null) || {
        echo "error: SHA256 checksum verification failed." >&2
        exit 1
    }
elif command -v shasum >/dev/null 2>&1; then
    _hash=$(awk '{print $1}' "${CHECKSUM_PATH}")
    printf '%s  %s\n' "${_hash}" "${ARCHIVE}" > "${TMPDIR_CREATED}/verify.sha256"
    (cd "${TMPDIR_CREATED}" && shasum -a 256 -c "verify.sha256" >/dev/null) || {
        echo "error: SHA256 checksum verification failed." >&2
        exit 1
    }
else
    echo "warning: neither sha256sum nor shasum found — skipping checksum verification." >&2
fi

# --- extract ----------------------------------------------------------------

tar xzf "${ARCHIVE_PATH}" -C "${TMPDIR_CREATED}" mneme

# --- determine install directory --------------------------------------------

if [ -n "${INSTALL_DIR:-}" ]; then
    DEST_DIR="${INSTALL_DIR}"
elif [ -w "/usr/local/bin" ]; then
    DEST_DIR="/usr/local/bin"
else
    DEST_DIR="${HOME}/.local/bin"
    mkdir -p "${DEST_DIR}"
fi

# --- install ----------------------------------------------------------------

BINARY_DEST="${DEST_DIR}/mneme"
mv "${TMPDIR_CREATED}/mneme" "${BINARY_DEST}"
chmod +x "${BINARY_DEST}"

# --- PATH warning -----------------------------------------------------------

# Check if DEST_DIR is on PATH (only relevant for ~/.local/bin fallback).
_on_path=0
IFS=:
for _dir in ${PATH}; do
    if [ "${_dir}" = "${DEST_DIR}" ]; then
        _on_path=1
        break
    fi
done
IFS=" "

if [ "${_on_path}" -eq 0 ] && [ "${DEST_DIR}" != "/usr/local/bin" ]; then
    printf "\nWarning: %s is not in your PATH.\n" "${DEST_DIR}"
    printf "Add this to your shell profile:\n"
    printf '  export PATH="%s:$PATH"\n' "${DEST_DIR}"
fi

# --- done -------------------------------------------------------------------

printf "\nmneme v%s installed to %s\n" "${VERSION}" "${BINARY_DEST}"
printf "Run 'mneme install claude-code' to configure.\n"
