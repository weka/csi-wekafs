FROM golang:1.16.5-alpine3.14 as go-builder
ARG VERSION
# https://stackoverflow.com/questions/36279253/go-compiled-binary-wont-run-in-an-alpine-docker-container-on-ubuntu-host
RUN apk add --no-cache libc6-compat
COPY go.mod /src/go.mod
COPY go.sum /src/go.sum
WORKDIR /src
RUN echo Building binaries version $VERSION
RUN echo Downloading required Go modules
RUN go mod download
ADD . /src
RUN echo Executing tests
RUN files=$(find . -name '*.go' | grep -v './vendor'); \
    if [ $(gofmt -d $files | wc -l) -ne 0 ]; then echo "formatting errors:"; gofmt -d $files; false; fi
RUN go vet /src/*/*.go
RUN go test /src/*/*.go
RUN echo Building package
RUN CGO_ENABLED=0 GOOS="linux" GOARCH="amd64" go build -a -ldflags '-X main.version='$VERSION' -extldflags "-static"' -o "/bin/wekafsplugin" /src/cmd/*

FROM alpine:3.14
LABEL maintainers="Weka"
LABEL description="Weka CSI Driver"
# Add util-linux to get a new version of losetup.
RUN apk add util-linux
COPY --from=go-builder /bin/wekafsplugin /wekafsplugin
ARG binary=/bin/wekafsplugin
ENTRYPOINT ["/wekafsplugin"]
