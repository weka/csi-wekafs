ARG UBI_HASH=9.6-1754584681

# Build Delve using Debian-based golang (compatible with UBI/glibc)
FROM golang:1.25.0 AS delve-builder
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go install -ldflags "-s -w -extldflags '-static'" github.com/go-delve/delve/cmd/dlv@latest

FROM registry.access.redhat.com/ubi9-minimal:${UBI_HASH}
LABEL maintainers="WekaIO, LTD"
LABEL description="Weka CSI Driver (Debug Build)"

# Install required packages
RUN microdnf install -y util-linux libselinux-utils pciutils binutils jq procps less container-selinux && \
    microdnf clean all && \
    rm -rf /var/cache/dnf

RUN mkdir -p /licenses
COPY LICENSE /licenses

LABEL maintainer="csi@weka.io"
LABEL name="WEKA CSI Plugin (Debug)"
LABEL vendor="weka.io"
LABEL summary="This image is used by WEKA CSI Plugin and incorporates both Controller and Node modules with debug support"
LABEL description="Container Storage Interface (CSI) plugin for WEKA - the data platform for AI (Debug Build with Delve)"
LABEL url="https://www.weka.io"

# Copy pre-built debug binaries (built on host)
COPY --chmod=755 ./bin/wekafsplugin-debug /wekafsplugin
COPY --chmod=755 ./bin/metricsserver-debug /metricsserver
COPY --from=delve-builder --chmod=755 /go/bin/dlv /usr/local/bin/dlv

# Verify dlv is present and executable
RUN /usr/local/bin/dlv version

# Expose NFS ports and Delve debug port
EXPOSE 2049 111/tcp 111/udp 2345

ENTRYPOINT ["/wekafsplugin"]
