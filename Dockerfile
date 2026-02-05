ARG UBI_HASH=9.7-1770267347
FROM golang:1.24-alpine AS go-builder
# https://stackoverflow.com/questions/36279253/go-compiled-binary-wont-run-in-an-alpine-docker-container-on-ubuntu-host
RUN apk add --no-cache libc6-compat gcc musl-dev
COPY go.mod /src/go.mod
COPY go.sum /src/go.sum
WORKDIR /src
ARG VERSION
RUN echo Building binaries version $VERSION
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
RUN go mod download

ARG TARGETARCH
ARG TARGETOS
RUN echo Building package
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -ldflags "-X main.version=$VERSION -extldflags '-static'" -o "/bin" /src/cmd/*

FROM registry.access.redhat.com/ubi9-minimal:${UBI_HASH} AS ubibuilder
RUN microdnf install -y util-linux libselinux-utils pciutils binutils jq procps less container-selinux
RUN microdnf clean all && rm -rf /var/cache/dnf

FROM ubibuilder
LABEL maintainers="WekaIO, LTD"
LABEL description="Weka CSI Driver"

RUN mkdir -p /licenses
COPY LICENSE /licenses
LABEL maintainer="csi@weka.io"
LABEL name="WEKA CSI Plugin"
LABEL vendor="weka.io"
LABEL summary="This image is used by WEKA CSI Plugin and incorporates both Controller and Node modules"
LABEL description="Container Storage Interface (CSI) plugin for WEKA - the data platform for AI"
LABEL url="https://www.weka.io"
ARG LOCAR_VERSION=0.4.3
RUN curl -L -o /locar https://github.com/weka/locar/releases/download/$LOCAR_VERSION/locar-$LOCAR_VERSION-$TARGETOS-$TARGETARCH
RUN chmod +x /locar
COPY --from=go-builder --chmod=755 /bin/wekafsplugin /wekafsplugin
COPY --from=go-builder --chmod=755 /bin/metricsserver /metricsserver
ARG binary=/bin/wekafsplugin
EXPOSE 2049 111/tcp 111/udp
ENTRYPOINT ["/wekafsplugin"]
