#!/usr/bin/env bash

if [[ $1 != "--keep" ]]; then
    TO_STOP=--exit-code-from=sanity
fi

env GOOS=linux GOARCH=amd64 go build -o .tmp-bin/plugin_linux ../../cmd/wekafsplugin/*.go
docker-compose build
docker-compose up ${TO_STOP} --always-recreate