#!/usr/bin/env bash

set -e

if [[ $1 != "--keep" ]]; then
    TO_STOP=--exit-code-from=sanity
fi

env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags '-X main.version=dev' -o .tmp-bin/plugin_linux ../../cmd/wekafsplugin/*.go
docker-compose build
docker-compose up ${TO_STOP} --always-recreate