#!/usr/bin/env bash

env GOOS=linux GOARCH=amd64 go build -o .tmp-bin/plugin_linux ../../cmd/wekafsplugin/*.go
docker-compose build
docker-compose up --exit-code-from=sanity --always-recreate