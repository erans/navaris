# Supply-chain: Go image pinned to 1.26.5-bookworm (matches go.mod).
# To enforce digest pin (recommended), resolve via:
#   docker buildx imagetools inspect golang:1.26.5-bookworm --format '{{json .Manifest}}' | jq -r .digest
# Then append @sha256:<digest> to each FROM golang line (e.g. ...-bookworm@sha256:...).
# Placeholder digest (replace before enforcing):
ARG GOLANG_IMAGE_DIGEST=sha256:6c5605ab3a9a9fb3c4eafe5b3d63cdbf3881caf113262b67862547b54a9db599
# Supply-chain: Firecracker tgz SHA256 — resolve via:
#   curl -fsSL https://github.com/firecracker-microvm/firecracker/releases/download/v1.15.0/sha256sums.txt | grep firecracker
ARG FC_SHA256=REPLACE_WITH_REAL_SHA256_FOR_firecracker-v1.15.0.tgz
# Supply-chain: Zabbly key fingerprint — verify via:
#   curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --show-keys
ARG ZABBLY_KEY_FINGERPRINT=4EFC590696CB15B87C73A3AD82CC8797C838DCFD
# ---- Stage 0: Alias for pre-built Firecracker artifacts ----
ARG FC_IMAGE=navarisd-firecracker
FROM ${FC_IMAGE} AS fc-artifacts

# ---- Stage 0.5: Build Web UI ----
FROM node:20-bookworm-slim AS webui-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 1: Build Go binaries ----
FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Bring in the built SPA so go:embed can find it.
COPY --from=webui-build /src/web/dist/ internal/webui/dist/

RUN CGO_ENABLED=0 go build -tags withui,firecracker,incus -o /navarisd ./cmd/navarisd
RUN CGO_ENABLED=0 go build -o /navaris ./cmd/navaris

# ---- Stage 2: Runtime ----
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH="/opt/incus/bin:${PATH}"
ENV LD_LIBRARY_PATH="/opt/incus/lib"

# Install Incus from Zabbly PPA.
# Supply-chain: verify Zabbly key fingerprint before use.
# Resolve fingerprint via: curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --show-keys
# Then set --build-arg ZABBLY_KEY_FINGERPRINT=<fingerprint> to enforce.
ARG ZABBLY_KEY_FINGERPRINT
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gpg && \
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /etc/apt/keyrings/zabbly.gpg && \
    if [ "$ZABBLY_KEY_FINGERPRINT" != "REPLACE_WITH_REAL_FINGERPRINT" ] && [ -n "$ZABBLY_KEY_FINGERPRINT" ]; then \
        echo "Verifying Zabbly key fingerprint..."; \
        gpg --show-keys /etc/apt/keyrings/zabbly.gpg | grep -q "$ZABBLY_KEY_FINGERPRINT" || (echo "ERROR: Zabbly key fingerprint mismatch (expected $ZABBLY_KEY_FINGERPRINT)" >&2; exit 1); \
    else \
        echo "WARNING: ZABBLY_KEY_FINGERPRINT not set — skipping fingerprint verification (set --build-arg ZABBLY_KEY_FINGERPRINT=... to enforce)" >&2; \
        gpg --show-keys /etc/apt/keyrings/zabbly.gpg || true; \
    fi && \
    echo "deb [signed-by=/etc/apt/keyrings/zabbly.gpg] https://pkgs.zabbly.com/incus/stable $(. /etc/os-release && echo ${VERSION_CODENAME}) main" \
        > /etc/apt/sources.list.d/zabbly-incus.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends incus=1:7.3-* && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Firecracker runtime dependencies.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        iproute2 iptables e2fsprogs procps wget && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Download Firecracker and jailer binaries.
ARG FC_VERSION=v1.15.0
ARG FC_SHA256
RUN ARCH=$(uname -m) && \
    wget -q "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz" \
        -O /tmp/fc.tgz && \
    if [ "$FC_SHA256" != "REPLACE_WITH_REAL_SHA256_FOR_firecracker-v1.15.0.tgz" ] && [ -n "$FC_SHA256" ]; then \
        echo "${FC_SHA256}  /tmp/fc.tgz" | sha256sum -c -; \
    else \
        echo "WARNING: FC_SHA256 not set — skipping Firecracker SHA verification (set --build-arg FC_SHA256=... to enforce)"; \
    fi && \
    tar -xzf /tmp/fc.tgz -C /tmp && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH} /usr/local/bin/firecracker && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/jailer-${FC_VERSION}-${ARCH} /usr/local/bin/jailer && \
    chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer && \
    rm -rf /tmp/fc.tgz /tmp/release-*

# Copy kernel and rootfs images from pre-built Firecracker image.
RUN mkdir -p /opt/firecracker/images
COPY --from=fc-artifacts /opt/firecracker/vmlinux /opt/firecracker/vmlinux
COPY --from=fc-artifacts /opt/firecracker/images/ /opt/firecracker/images/

# Copy Go binaries.
COPY --from=build /navarisd /usr/local/bin/navarisd
COPY --from=build /navaris /usr/local/bin/navaris

# Copy entrypoint.
COPY scripts/allinone-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
