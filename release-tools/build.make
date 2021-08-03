# Copyright 2021 Weka.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


.PHONY: build-% build clean test

# understand what is the version tag
VERSION=$(shell cat deploy/helm/csi-wekafsplugin/Chart.yaml | grep appVersion | awk '{print $$2}' | tr -d '"')
DOCKER_IMAGE_NAME=csi-wekafs
$(info Docker image name: $(DOCKER_IMAGE_NAME))

# This builds each command (= the sub-directories of ./cmd) for the target platform(s)
# defined by BUILD_PLATFORMS.
$(CMDS:%=build-%): build-%: check-go-version-go
	mkdir -p bin
	echo '$(BUILD_PLATFORMS)' | tr ';' '\n' | while read -r os arch suffix; do \
		if ! (set -x; CGO_ENABLED=0 GOOS="linux" GOARCH="amd64" go build $(GOFLAGS_VENDOR) -a -ldflags '-X main.version=v$(VERSION) -extldflags "-static"' -o "./bin/$*$$suffix" ./cmd/$*); then \
			echo "Building $* for GOOS=$$os GOARCH=$$arch failed, see error(s) above."; \
			exit 1; \
		fi; \
	done
	docker build -t $(DOCKER_IMAGE_NAME):$(VERSION) -f $(shell if [ -e ./cmd/$*/Dockerfile ]; then echo ./cmd/$*/Dockerfile; else echo Dockerfile; fi) --label revision=$(REV) .


build: $(CMDS:%=build-%)

clean:
	-rm -rf bin

test: check-go-version-go

.PHONY: test-go
test: test-go
test-go:
	@ echo; echo "### $@:"
	go test $(GOFLAGS_VENDOR) `go list $(GOFLAGS_VENDOR) ./... | grep -v -e 'vendor' -e '/test/e2e$$' $(TEST_GO_FILTER_CMD)` $(TESTARGS)

.PHONY: test-vet
test: test-vet
test-vet:
	@ echo; echo "### $@:"
	go vet $(GOFLAGS_VENDOR) `go list $(GOFLAGS_VENDOR) ./... | grep -v vendor $(TEST_VET_FILTER_CMD)`

.PHONY: test-fmt
test: test-fmt
test-fmt:
	@ echo; echo "### $@:"
	files=$$(find . -name '*.go' | grep -v './vendor' $(TEST_FMT_FILTER_CMD)); \
	if [ $$(gofmt -d $$files | wc -l) -ne 0 ]; then \
		echo "formatting errors:"; \
		gofmt -d $$files; \
		false; \
	fi

.PHONY: test-subtree
test: test-subtree
test-subtree:
	@ echo; echo "### $@:"
	./release-tools/verify-subtree.sh release-tools

# Components can extend the set of directories which must pass shellcheck.
# The default is to check only the release-tools directory itself.
TEST_SHELLCHECK_DIRS=release-tools
.PHONY: test-shellcheck
test: test-shellcheck
test-shellcheck:
	@ echo; echo "### $@:"
	@ ret=0; \
	if ! command -v docker; then \
		echo "skipped, no Docker"; \
		exit 0; \
        fi; \
	for dir in $(abspath $(TEST_SHELLCHECK_DIRS)); do \
		echo; \
		echo "$$dir:"; \
		./release-tools/verify-shellcheck.sh "$$dir" || ret=1; \
	done; \
	exit $$ret

# Targets in the makefile can depend on check-go-version-<path to go binary>
# to trigger a warning if the x.y version of that binary does not match
# what the project uses. Make ensures that this is only checked once per
# invocation.
.PHONY: check-go-version-%
check-go-version-%:
	./release-tools/verify-go-version.sh "$*"
