ARG UBI_HASH=9.7-1773204619
FROM golang:1.24-alpine AS go-builder
ARG TARGETARCH
ARG TARGETOS
# https://stackoverflow.com/questions/36279253/go-compiled-binary-wont-run-in-an-alpine-docker-container-on-ubuntu-host
RUN apk add --no-cache libc6-compat gcc musl-dev
COPY go.mod /src/go.mod
COPY go.sum /src/go.sum
WORKDIR /src
ARG LOCAR_VERSION=0.4.3
ADD --chmod=655 https://github.com/weka/locar/releases/download/$LOCAR_VERSION/locar-$LOCAR_VERSION-$TARGETOS-$TARGETARCH locar
RUN go mod download
ARG VERSION
RUN echo Building binaries version $VERSION for architecture $TARGETARCH
RUN echo Downloading required Go modules
ADD go.mod /src/go.mod
# Need to add true in between to avoid "failed to get layer"
# https://stackoverflow.com/questions/51115856/docker-failed-to-export-image-failed-to-create-image-failed-to-get-layer
RUN true
ADD go.sum /src/go.sum
RUN true
ADD pkg /src/pkg
RUN true
ADD cmd /src/cmd
RUN true


RUN echo Building package
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -ldflags "-X main.version=$VERSION -extldflags '-static'" -o "/bin/wekafsplugin" /src/cmd/wekafsplugin

RUN echo Building wait-for-leader utility
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -ldflags "-extldflags '-static'" -o "/bin/wait-for-leader" /src/cmd/wait-for-leader

FROM registry.access.redhat.com/ubi9-minimal:${UBI_HASH}
LABEL maintainers="WekaIO, LTD"
LABEL description="Weka CSI Driver"

# NOTE: usbutils, nfs-utils, rpcbind removed — unavailable in UBI9-minimal repos.
# nfs-utils/rpcbind were not used at runtime (app uses kernel NFS client via k8s mount-utils).
# If USB device discovery is needed, install usbutils from EPEL.
RUN microdnf install -y util-linux libselinux-utils pciutils \
    procps less container-selinux && \
    microdnf clean all && rm -rf /var/cache/dnf
RUN mkdir -p /licenses
COPY LICENSE /licenses
LABEL maintainer="csi@weka.io"
LABEL name="WEKA CSI Plugin"
LABEL vendor="weka.io"
LABEL summary="This image is used by WEKA CSI Plugin and incorporates both Controller and Node modules"
LABEL description="Container Storage Interface (CSI) plugin for WEKA - the data platform for AI"
LABEL url="https://www.weka.io"
COPY --from=go-builder /bin/wekafsplugin /wekafsplugin
COPY --from=go-builder /bin/wait-for-leader /wait-for-leader
COPY --from=go-builder /src/locar /locar
ARG binary=/bin/wekafsplugin
EXPOSE 2049 111/tcp 111/udp
ENTRYPOINT ["/wekafsplugin"]
