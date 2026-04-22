#!/usr/bin/env bash
set -euo pipefail

DEFAULT_VERSION="v1.15.1"
DEFAULT_BIN_DIR="/usr/local/lib/navaris/firecracker/bin"

version="${FIRECRACKER_VERSION:-$DEFAULT_VERSION}"
bin_dir="${NAVARIS_FIRECRACKER_BIN_DIR:-$DEFAULT_BIN_DIR}"
link_dir="${NAVARIS_FIRECRACKER_LINK_DIR:-}"
arch_override="${NAVARIS_FIRECRACKER_ARCH:-}"
force=0

usage() {
    cat <<'EOF'
Install pinned Firecracker runtime binaries from upstream GitHub releases.

Usage:
  scripts/install-firecracker-runtime.sh [options]

Options:
  --version VERSION   Firecracker release tag to install (default: v1.15.1)
  --bin-dir DIR       Install directory for firecracker and jailer
                      (default: /usr/local/lib/navaris/firecracker/bin)
  --link-dir DIR      Optional directory to receive firecracker/jailer symlinks
  --arch ARCH         Target arch: amd64 or arm64 (default: detect host arch)
  --force             Overwrite existing installed binaries
  -h, --help          Show this help text
EOF
}

need_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "missing required command: $1" >&2
        exit 1
    fi
}

download() {
    local url="$1"
    local out="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 --retry-delay 1 "$url" -o "$out"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO "$out" "$url"
        return
    fi

    echo "missing required downloader: curl or wget" >&2
    exit 1
}

detect_arch() {
    local raw="${arch_override:-$(uname -m)}"
    case "$raw" in
        amd64|x86_64)
            printf 'x86_64\n'
            ;;
        arm64|aarch64)
            printf 'aarch64\n'
            ;;
        *)
            echo "unsupported architecture: $raw (expected amd64 or arm64)" >&2
            exit 1
            ;;
    esac
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            version="$2"
            shift 2
            ;;
        --bin-dir)
            bin_dir="$2"
            shift 2
            ;;
        --link-dir)
            link_dir="$2"
            shift 2
            ;;
        --arch)
            arch_override="$2"
            shift 2
            ;;
        --force)
            force=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

need_cmd tar
need_cmd sha256sum
need_cmd install
need_cmd mktemp

fc_arch="$(detect_arch)"
asset="firecracker-${version}-${fc_arch}.tgz"
asset_sha="${asset}.sha256.txt"
release_dir="release-${version}-${fc_arch}"
base_url="https://github.com/firecracker-microvm/firecracker/releases/download/${version}"

tmpdir="$(mktemp -d)"
cleanup() {
    rm -rf "$tmpdir"
}
trap cleanup EXIT

echo "Downloading ${asset}..."
download "${base_url}/${asset}" "${tmpdir}/${asset}"
download "${base_url}/${asset_sha}" "${tmpdir}/${asset_sha}"

echo "Verifying ${asset}..."
(
    cd "$tmpdir"
    sha256sum -c "$asset_sha"
)

echo "Extracting ${asset}..."
tar -xzf "${tmpdir}/${asset}" -C "$tmpdir"

fc_src="${tmpdir}/${release_dir}/firecracker-${version}-${fc_arch}"
jailer_src="${tmpdir}/${release_dir}/jailer-${version}-${fc_arch}"

if [ ! -f "$fc_src" ] || [ ! -f "$jailer_src" ]; then
    echo "release archive layout did not match expected Firecracker asset structure" >&2
    exit 1
fi

if [ "$force" -ne 1 ]; then
    if [ -e "${bin_dir}/firecracker" ] || [ -e "${bin_dir}/jailer" ]; then
        echo "destination already contains firecracker or jailer: ${bin_dir} (use --force to overwrite)" >&2
        exit 1
    fi
fi

echo "Installing to ${bin_dir}..."
install -d "$bin_dir"
install -m 0755 "$fc_src" "${bin_dir}/firecracker"
install -m 0755 "$jailer_src" "${bin_dir}/jailer"

if [ -n "$link_dir" ]; then
    echo "Linking into ${link_dir}..."
    install -d "$link_dir"
    ln -sfn "${bin_dir}/firecracker" "${link_dir}/firecracker"
    ln -sfn "${bin_dir}/jailer" "${link_dir}/jailer"
fi

cat <<EOF
Installed Firecracker runtime:
  version:    ${version}
  arch:       ${fc_arch}
  bin dir:    ${bin_dir}
  firecracker ${bin_dir}/firecracker
  jailer      ${bin_dir}/jailer
EOF
