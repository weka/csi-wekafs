FROM golang
#ENV test=test
RUN GO_ENABLED=0 GOOS=linux GOARCH=amd64 go get -u -t github.com/kubernetes-csi/csi-test/cmd/csi-sanity || true
ADD wekafs-dirv1.yaml /test/wekafs-dirv1.yaml
ADD wekafs-fs.yaml /test/wekafs-fs.yaml
ADD wekafs-snapvol.yaml /test/wekafs-snapvol.yaml
ADD wekafs-api-secret.yaml /test/wekafs-api-secret.yaml
ADD _docker_run_sanity.sh /usr/bin/run_sanity
ADD .tmp-bin/plugin_linux /usr/local/bin/wekafsplugin
