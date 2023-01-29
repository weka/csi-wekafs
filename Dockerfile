FROM golang:1.19-alpine3.17 as go-builder
# https://stackoverflow.com/questions/36279253/go-compiled-binary-wont-run-in-an-alpine-docker-container-on-ubuntu-host
RUN apk add --no-cache libc6-compat gcc
RUN apk add musl-dev
COPY go.mod /src/go.mod
COPY go.sum /src/go.sum
WORKDIR /src
RUN go mod download
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

RUN echo Building package
RUN CGO_ENABLED=0 GOOS="linux" GOARCH="amd64" go build -a -ldflags '-X main.version='$VERSION' -extldflags "-static"' -o "/bin/wekafsplugin" /src/cmd/*

FROM alpine:3.17
LABEL maintainers="WekaIO, LTD"
LABEL description="Weka CSI Driver"
# Add util-linux to get a new version of losetup.
RUN apk add util-linux libselinux libselinux-utils util-linux pciutils usbutils coreutils binutils findutils grep bash
COPY --from=go-builder /bin/wekafsplugin /wekafsplugin
ARG binary=/bin/wekafsplugin
ENTRYPOINT ["/wekafsplugin"]
