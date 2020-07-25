FROM golang
#ENV test=test
RUN go get github.com/kubernetes-csi/csi-test/cmd/csi-sanity
ADD wekafs-dirv1.yaml /test/wekafs-dirv1.yaml
ADD wekafs-existingPathv1.yaml /test/wekafs-existingPathv1.yaml
ADD _docker_run_sanity.sh /usr/bin/run_sanity
ADD .tmp-bin/plugin_linux /usr/local/bin/wekafsplugin
