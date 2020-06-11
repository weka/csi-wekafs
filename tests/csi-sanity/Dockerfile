FROM golang
#ENV test=test
RUN go get github.com/kubernetes-csi/csi-test/cmd/csi-sanity
ADD wekafs-vol.yaml /test/wekafs-vol.yaml
COPY .tmp-bin/plugin_linux /usr/local/bin/wekafsplugin
