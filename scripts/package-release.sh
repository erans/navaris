#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

arch=""
version=""
output_dir="dist"
skip_ui_build=0
formats="tar.gz"

usage() {
    cat <<'EOF'
Build a Navaris release tarball for a single Linux architecture.

Usage:
  scripts/package-release.sh --version VERSION --arch ARCH [options]

Options:
  --version VERSION   Release version or tag, for example v0.1.0
  --arch ARCH         Target arch: amd64 or arm64
  --output-dir DIR    Directory to write the archive into (default: dist)
  --formats LIST      Comma-separated formats: tar.gz,deb (default: tar.gz)
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

has_format() {
    local wanted="$1"
    local fmt
    OLD_IFS="$IFS"
    IFS=','
    for fmt in $formats; do
        if [ "$fmt" = "$wanted" ]; then
            IFS="$OLD_IFS"
            return 0
        fi
    done
    IFS="$OLD_IFS"
    return 1
}

debian_version() {
    local raw="$1"
    raw="${raw#v}"
    if printf '%s' "$raw" | grep -q -- '-'; then
        local base suffix
        base="${raw%%-*}"
        suffix="${raw#*-}"
        suffix="${suffix//-/.}"
        printf '%s~%s\n' "$base" "$suffix"
        return
    fi
    printf '%s\n' "$raw"
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
        --formats)
            formats="$2"
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
need_cmd grep

if has_format deb; then
    need_cmd nfpm
fi

if [ "$skip_ui_build" -ne 1 ]; then
    need_cmd npm
    need_cmd node
    need_node_major 20
    (
        cd "${repo_root}/web"
        npm ci
        npm run build
    )

    rm -rf "${repo_root}/internal/webui/dist"
    mkdir -p "${repo_root}/internal/webui/dist"
    cp -a "${repo_root}/web/dist/." "${repo_root}/internal/webui/dist/"
    touch "${repo_root}/internal/webui/dist/.gitkeep"
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

(
    cd "$repo_root"
    build_go "${bin_dir}/navarisd" -tags withui,firecracker,incus ./cmd/navarisd
    build_go "${bin_dir}/navaris" ./cmd/navaris
    build_go "${bin_dir}/navaris-agent" ./cmd/navaris-agent
    build_go "${bin_dir}/navaris-mcp" -ldflags "-X main.version=${version}" ./cmd/navaris-mcp
)

install -m 0644 "${repo_root}/README.md" "${repo_root}/LICENSE" "${stage_dir}/"
install -m 0644 "${repo_root}/docs/native-install.md" "${stage_dir}/docs/native-install.md"
install -m 0644 "${repo_root}/packaging/systemd/navarisd.service" "${stage_dir}/packaging/systemd/navarisd.service"
install -m 0644 "${repo_root}/packaging/systemd/navarisd.env.example" "${stage_dir}/packaging/systemd/navarisd.env.example"
install -m 0755 "${repo_root}/packaging/systemd/navarisd-launch.sh" "${stage_dir}/packaging/systemd/navarisd-launch.sh"
install -m 0755 "${repo_root}/scripts/install-firecracker-runtime.sh" "${stage_dir}/scripts/install-firecracker-runtime.sh"
install -m 0755 "${repo_root}/scripts/install-native.sh" "${stage_dir}/scripts/install-native.sh"

mkdir -p "$output_dir"
archive_path="${output_dir}/${release_root}.tar.gz"

if has_format tar.gz; then
    tar -C "$tmpdir" -czf "$archive_path" "$release_root"
    echo "$archive_path"
fi

if has_format deb; then
    deb_version="$(debian_version "$version")"
    deb_arch="$arch"
    deb_target="${output_dir}/navaris_${deb_version}_${deb_arch}.deb"
    nfpm_config="${tmpdir}/nfpm.yaml"

    cat > "$nfpm_config" <<EOF
name: navaris
arch: ${deb_arch}
platform: linux
version: ${deb_version}
section: admin
priority: optional
maintainer: Eran Sandler <eran@sandler.co.il>
description: |
  Navaris is a sandbox control plane for managing isolated execution
  environments across multiple backends.
homepage: https://github.com/erans/navaris
license: Apache-2.0
depends:
  - systemd
contents:
  - src: ${bin_dir}/navarisd
    dst: /usr/bin/navarisd
  - src: ${bin_dir}/navaris
    dst: /usr/bin/navaris
  - src: ${bin_dir}/navaris-mcp
    dst: /usr/bin/navaris-mcp
  - src: ${bin_dir}/navaris-agent
    dst: /usr/bin/navaris-agent
  - src: ${repo_root}/packaging/systemd/navarisd.service
    dst: /lib/systemd/system/navarisd.service
  - src: ${repo_root}/packaging/systemd/navarisd-launch.sh
    dst: /usr/lib/navaris/navarisd-launch.sh
  - src: ${repo_root}/scripts/install-firecracker-runtime.sh
    dst: /usr/lib/navaris/scripts/install-firecracker-runtime.sh
  - src: ${repo_root}/packaging/deb/navarisd.env
    dst: /etc/navaris/navarisd.env
    type: config|noreplace
  - src: ${repo_root}/docs/native-install.md
    dst: /usr/share/doc/navaris/native-install.md
  - src: ${repo_root}/LICENSE
    dst: /usr/share/doc/navaris/LICENSE
  - dst: /var/lib/navaris
    type: dir
  - dst: /var/lib/navaris/firecracker
    type: dir
  - dst: /var/lib/navaris/firecracker/images
    type: dir
  - dst: /var/lib/navaris/firecracker/snapshots
    type: dir
  - dst: /var/lib/navaris/firecracker/vm
    type: dir
scripts:
  postinstall: ${repo_root}/packaging/deb/postinstall.sh
  postremove: ${repo_root}/packaging/deb/postremove.sh
EOF

    nfpm package --packager deb --config "$nfpm_config" --target "$deb_target"
    echo "$deb_target"
fi
