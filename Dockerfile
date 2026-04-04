# ---- Stage 1: Build Go binaries ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags firecracker,incus -o /navarisd ./cmd/navarisd
RUN CGO_ENABLED=0 go build -o /navaris ./cmd/navaris

# ---- Stage 2: Runtime ----
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH="/opt/incus/bin:${PATH}"
ENV LD_LIBRARY_PATH="/opt/incus/lib"

# Install Incus from Zabbly PPA.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gpg && \
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /etc/apt/keyrings/zabbly.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/zabbly.gpg] https://pkgs.zabbly.com/incus/stable $(. /etc/os-release && echo ${VERSION_CODENAME}) main" \
        > /etc/apt/sources.list.d/zabbly-incus.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends incus && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Firecracker runtime dependencies.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        iproute2 iptables e2fsprogs procps wget && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Download Firecracker and jailer binaries.
ARG FC_VERSION=v1.15.0
RUN ARCH=$(uname -m) && \
    wget -q "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz" \
        -O /tmp/fc.tgz && \
    tar -xzf /tmp/fc.tgz -C /tmp && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH} /usr/local/bin/firecracker && \
    mv /tmp/release-${FC_VERSION}-${ARCH}/jailer-${FC_VERSION}-${ARCH} /usr/local/bin/jailer && \
    chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer && \
    rm -rf /tmp/fc.tgz /tmp/release-*

# Copy kernel and rootfs images from pre-built Firecracker image.
ARG FC_IMAGE=navarisd-firecracker
RUN mkdir -p /opt/firecracker/images
COPY --from=${FC_IMAGE} /opt/firecracker/vmlinux /opt/firecracker/vmlinux
COPY --from=${FC_IMAGE} /opt/firecracker/images/ /opt/firecracker/images/

# Copy Go binaries.
COPY --from=build /navarisd /usr/local/bin/navarisd
COPY --from=build /navaris /usr/local/bin/navaris

# Copy entrypoint.
COPY scripts/allinone-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
