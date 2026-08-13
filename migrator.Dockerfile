# Container image for weka-csi-migrator.
#
# The CLI talks to Kubernetes only, so the runtime needs nothing beyond CA certificates and
# the static binary. Running from scratch keeps the attack surface of a tool that handles
# cluster credentials as small as possible.

FROM golang:1.25-alpine AS builder
ARG TARGETARCH
ARG TARGETOS
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/weka-csi-migrator ./cmd/weka-csi-migrator
COPY pkg/migrator ./pkg/migrator
COPY pkg/volumeid ./pkg/volumeid

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/weka-csi-migrator ./cmd/weka-csi-migrator

FROM alpine:3.21 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/weka-csi-migrator /weka-csi-migrator

# Run unprivileged: the tool only ever needs a kubeconfig and an archive path.
USER 65534:65534

ENTRYPOINT ["/weka-csi-migrator"]
