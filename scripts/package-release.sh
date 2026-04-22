#!/usr/bin/env bash
set -euo pipefail

arch=""
version=""
output_dir="dist"
skip_ui_build=0

usage() {
    cat <<'EOF'
Build a Navaris release tarball for a single Linux architecture.

Usage:
  scripts/package-release.sh --version VERSION --arch ARCH [options]

Options:
  --version VERSION   Release version or tag, for example v0.1.0
  --arch ARCH         Target arch: amd64 or arm64
  --output-dir DIR    Directory to write the archive into (default: dist)
  --skip-ui-build     Reuse the existing embedded UI build
  -h, --help          Show this help text
EOF
}

need_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "missing required command: $1" >&2
        exit 1
    fi
}

need_node_major() {
    local min_major="$1"
    local actual

    actual="$(node -p 'process.versions.node.split(".")[0]')"
    if [ "$actual" -lt "$min_major" ]; then
        echo "node ${min_major}+ is required to build the embedded web UI (found $(node -v))" >&2
        exit 1
    fi
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            version="$2"
            shift 2
            ;;
        --arch)
            arch="$2"
            shift 2
            ;;
        --output-dir)
            output_dir="$2"
            shift 2
            ;;
        --skip-ui-build)
            skip_ui_build=1
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

if [ -z "$version" ] || [ -z "$arch" ]; then
    usage >&2
    exit 1
fi

case "$arch" in
    amd64|arm64)
        ;;
    *)
        echo "unsupported arch: $arch (expected amd64 or arm64)" >&2
        exit 1
        ;;
esac

need_cmd go
need_cmd tar
need_cmd install
need_cmd mktemp
need_cmd rm
need_cmd cp

if [ "$skip_ui_build" -ne 1 ]; then
    need_cmd npm
    need_cmd node
    need_node_major 20
    (
        cd web
        npm ci
        npm run build
    )

    rm -rf internal/webui/dist
    mkdir -p internal/webui/dist
    cp -a web/dist/. internal/webui/dist/
    touch internal/webui/dist/.gitkeep
fi

tmpdir="$(mktemp -d)"
cleanup() {
    rm -rf "$tmpdir"
}
trap cleanup EXIT

release_root="navaris_${version}_linux_${arch}"
stage_dir="${tmpdir}/${release_root}"
bin_dir="${stage_dir}/bin"

install -d "$bin_dir" "${stage_dir}/docs" "${stage_dir}/packaging/systemd" "${stage_dir}/scripts"

build_go() {
    local out="$1"
    shift
    GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -o "$out" "$@"
}

build_go "${bin_dir}/navarisd" -tags withui,firecracker,incus ./cmd/navarisd
build_go "${bin_dir}/navaris" ./cmd/navaris
build_go "${bin_dir}/navaris-agent" ./cmd/navaris-agent
build_go "${bin_dir}/navaris-mcp" -ldflags "-X main.version=${version}" ./cmd/navaris-mcp

install -m 0644 README.md LICENSE "${stage_dir}/"
install -m 0644 docs/native-install.md "${stage_dir}/docs/native-install.md"
install -m 0644 packaging/systemd/navarisd.service "${stage_dir}/packaging/systemd/navarisd.service"
install -m 0644 packaging/systemd/navarisd.env.example "${stage_dir}/packaging/systemd/navarisd.env.example"
install -m 0755 packaging/systemd/navarisd-launch.sh "${stage_dir}/packaging/systemd/navarisd-launch.sh"
install -m 0755 scripts/install-firecracker-runtime.sh "${stage_dir}/scripts/install-firecracker-runtime.sh"
install -m 0755 scripts/install-native.sh "${stage_dir}/scripts/install-native.sh"

mkdir -p "$output_dir"
archive_path="${output_dir}/${release_root}.tar.gz"

tar -C "$tmpdir" -czf "$archive_path" "$release_root"

echo "$archive_path"
