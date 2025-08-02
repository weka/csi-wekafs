FROM golang:1.24-alpine as builder
RUN go install -v github.com/kubernetes-csi/csi-test/v5/cmd/csi-sanity@v5.3.1

FROM alpine
COPY --from=builder /go/bin/csi-sanity /usr/local/bin/csi-sanity
ADD tests/csi-sanity/wekafs-dirv1.yaml /test/wekafs-dirv1.yaml
ADD tests/csi-sanity/wekafs-fs.yaml /test/wekafs-fs.yaml
ADD tests/csi-sanity/wekafs-snapvol.yaml /test/wekafs-snapvol.yaml
ADD tests/csi-sanity/wekafs-api-secret.yaml /test/wekafs-api-secret.yaml
ADD tests/csi-sanity/run_sanity.sh /usr/bin/run_sanity
ADD --chmod=755 ./build/linux-amd64/wekafsplugin /usr/local/bin/wekafsplugin
