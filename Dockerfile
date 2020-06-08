FROM alpine
LABEL maintainers="Weka.IO"
LABEL description="Weka Matrix CSI Driver"
ARG binary=./bin/wekafsplugin

# Add util-linux to get a new version of losetup.
RUN apk add util-linux
COPY ${binary} /wekafsplugin
ENTRYPOINT ["/wekafsplugin"]
