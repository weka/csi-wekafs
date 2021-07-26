FROM alpine
LABEL maintainers="Weka"
LABEL description="Weka CSI Driver"
ARG binary=./bin/wekafsplugin

# Add util-linux to get a new version of losetup.
RUN apk add util-linux
COPY ${binary} /wekafsplugin
ENTRYPOINT ["/wekafsplugin"]
