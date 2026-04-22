#!/usr/bin/env bash
set -euo pipefail

prefix="/usr/local"
systemd_dir="/etc/systemd/system"
config_dir="/etc/navaris"
state_dir="/var/lib/navaris"
install_systemd=1
reload_systemd=1
enable_service=0
start_service=0

usage() {
    cat <<'EOF'
Install a Navaris release tarball onto a Linux host.

Run this script from the extracted release root that contains:
  - bin/
  - packaging/systemd/
  - scripts/

Usage:
  scripts/install-native.sh [options]

Options:
  --prefix DIR          Install prefix for binaries and support files
                        (default: /usr/local)
  --systemd-dir DIR     Directory for navarisd.service
                        (default: /etc/systemd/system)
  --config-dir DIR      Directory for navarisd.env
                        (default: /etc/navaris)
  --state-dir DIR       State root for database and Firecracker assets
                        (default: /var/lib/navaris)
  --skip-systemd        Do not install or reload the systemd unit
  --no-reload           Do not run systemctl daemon-reload
  --enable              Enable navarisd.service after install
  --start               Start navarisd.service after install
  -h, --help            Show this help text
EOF
}

need_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "missing required command: $1" >&2
        exit 1
    fi
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix)
            prefix="$2"
            shift 2
            ;;
        --systemd-dir)
            systemd_dir="$2"
            shift 2
            ;;
        --config-dir)
            config_dir="$2"
            shift 2
            ;;
        --state-dir)
            state_dir="$2"
            shift 2
            ;;
        --skip-systemd)
            install_systemd=0
            shift
            ;;
        --no-reload)
            reload_systemd=0
            shift
            ;;
        --enable)
            enable_service=1
            shift
            ;;
        --start)
            start_service=1
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

if [ ! -d bin ] || [ ! -d packaging/systemd ] || [ ! -d scripts ]; then
    echo "run this script from the extracted Navaris release root" >&2
    exit 1
fi

need_cmd install
need_cmd mkdir
need_cmd cp

bin_install_dir="${prefix}/bin"
libexec_dir="${prefix}/lib/navaris"

echo "Installing binaries into ${bin_install_dir}..."
install -d "$bin_install_dir"
install -m 0755 bin/navarisd "${bin_install_dir}/navarisd"
install -m 0755 bin/navaris "${bin_install_dir}/navaris"
install -m 0755 bin/navaris-mcp "${bin_install_dir}/navaris-mcp"
install -m 0755 bin/navaris-agent "${bin_install_dir}/navaris-agent"

echo "Installing support files into ${libexec_dir}..."
install -d "${libexec_dir}/scripts" "${libexec_dir}/firecracker/bin"
install -m 0755 packaging/systemd/navarisd-launch.sh "${libexec_dir}/navarisd-launch.sh"
install -m 0755 scripts/install-firecracker-runtime.sh "${libexec_dir}/scripts/install-firecracker-runtime.sh"

echo "Creating config and state directories..."
install -d "$config_dir" "$state_dir"
install -d "${state_dir}/firecracker/images" "${state_dir}/firecracker/snapshots" "${state_dir}/firecracker/vm"

if [ -f "${config_dir}/navarisd.env" ]; then
    echo "Leaving existing config in place: ${config_dir}/navarisd.env"
else
    install -m 0644 packaging/systemd/navarisd.env.example "${config_dir}/navarisd.env"
    echo "Installed default config: ${config_dir}/navarisd.env"
fi

if [ "$install_systemd" -eq 1 ]; then
    install -d "$systemd_dir"
    install -m 0644 packaging/systemd/navarisd.service "${systemd_dir}/navarisd.service"

    if [ "$reload_systemd" -eq 1 ] && command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload
    fi

    if [ "$enable_service" -eq 1 ]; then
        if ! command -v systemctl >/dev/null 2>&1; then
            echo "--enable was requested but systemctl is not available" >&2
            exit 1
        fi
        systemctl enable navarisd.service
    fi

    if [ "$start_service" -eq 1 ]; then
        if ! command -v systemctl >/dev/null 2>&1; then
            echo "--start was requested but systemctl is not available" >&2
            exit 1
        fi
        systemctl start navarisd.service
    fi
fi

service_status="skipped"
if [ "$install_systemd" -eq 1 ]; then
    service_status="${systemd_dir}/navarisd.service"
fi

cat <<EOF
Installed Navaris:
  prefix:      ${prefix}
  config:      ${config_dir}/navarisd.env
  state:       ${state_dir}
  service:     ${service_status}

Next steps:
  1. Edit ${config_dir}/navarisd.env for Incus and optional Firecracker settings.
  2. If using Firecracker, run:
     ${libexec_dir}/scripts/install-firecracker-runtime.sh --link-dir ${prefix}/bin
  3. If systemd was installed:
     systemctl enable --now navarisd
EOF
