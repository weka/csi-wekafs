# Copyright 2019 The Kubernetes Authors.
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


.PHONY: build-% build container-% container push-% push clean test

# This is the default. It can be overridden in the main Makefile after
# including build.make.
REGISTRY_NAME=quay.io/weka.io
HELM_REPO_URL=https://weka.github.io/csi-wekafs/

# Revision that gets built into each binary via the main.version
# string. Uses the `git describe` output based on the most recent
# version tag with a short revision suffix or, if nothing has been
# tagged yet, just the revision.
#
# Beware that tags may also be missing in shallow clones as done by
# some CI systems (like TravisCI, which pulls only 50 commits).

IMAGE_UNIQUE_TAG=$(shell uuid -v 4 | cut -d- -f1)
# freeze revision from git tag so even if
LATEST_TAG::=$(shell git describe --tags --abbrev=0)
AFTER_LATEST::=$(shell git rev-list $(LATEST_TAG)..HEAD | wc -l)

ifeq "$(AFTER_LATEST)" "0"
  AFTER_LATEST:=
else
  AFTER_LATEST:=-$(AFTER_LATEST)
endif
DIRTY::=$(shell git diff --quiet || echo '-dirty')
$(info $$AFTER_LATEST is [${AFTER_LATEST}])

REV::=$(LATEST_TAG)$(AFTER_LATEST)$(DIRTY)
$(eval VERSION := $$$(REV))
$(eval HELM_CHART_VERSION := $$$(LATEST_TAG))
$(info $$VERSION is [${VERSION}])
$(info $$REV is [${REV}])
# Images are named after the command contained in them.
IMAGE_NAME=$(REGISTRY_NAME)/csi-wekafs


ifdef V
# Adding "-alsologtostderr" assumes that all test binaries contain glog. This is not guaranteed.
TESTARGS = -v -args -alsologtostderr -v 5
else
TESTARGS =
endif

# This builds each command (= the sub-directories of ./cmd) for the target platform(s)
# defined by BUILD_PLATFORMS.
$(CMDS:%=build-%): build-%: check-go-version-go
	mkdir -p bin
	echo '$(BUILD_PLATFORMS)' | tr ';' '\n' | while read -r os arch suffix; do \
		if ! (set -x; CGO_ENABLED=0 GOOS="linux" GOARCH="amd64" go build $(GOFLAGS_VENDOR) -a -ldflags '-X main.version=$(REV) -extldflags "-static"' -o "./bin/$*$$suffix" ./cmd/$*); then \
			echo "Building $* for GOOS=$$os GOARCH=$$arch failed, see error(s) above."; \
			exit 1; \
		fi; \
	done

$(CMDS:%=container-%): container-%: build-%
	docker build -t $(IMAGE_NAME):$(REV) -f $(shell if [ -e ./cmd/$*/Dockerfile ]; then echo ./cmd/$*/Dockerfile; else echo Dockerfile; fi) --label revision=$(REV) .
	sed -i ./deploy/kubernetes-latest/wekafs/csi-wekafs-plugin.yaml -e 's|quay.io/weka.io/csi-wekafs:.*|quay.io/weka.io/csi-wekafs:$(REV)|g'


$(CMDS:%=push-%): push-%: container-%
	set -ex; \
	push_image () { \
		docker push $(IMAGE_NAME):$(REV); \
	}; \
	echo "Pushing under tag $(REV)"; \
	push_image

$(CMDS:%=helm-%): helm-%:
	set -ex; \
	sed -i ./deploy/helm/csi-wekafsplugin/Chart.yaml -e 's|^version: .*|version: "$(VERSION)"|1' -e 's|^appVersion: .*|appVersion: "$(VERSION)"|1' ;\
	sed -i ./deploy/helm/csi-wekafsplugin/Chart.yaml -e 's|\(https://github.com/weka/csi-wekafs/tree/\).*\(/deploy/helm/csi-wekafsplugin\)|\1$(REV)\2|1' ;\
	sed -i ./deploy/helm/csi-wekafsplugin/values.yaml -e 's|\(\&csiDriverVersion \).*|\1 "$(VERSION)"|1' ;\
	helm package deploy/helm/csi-wekafsplugin ;\
	TEMP_DIR=`mktemp -d` ;\
	git clone git@github.com:weka/csi-wekafs.git -q -b gh-pages $$TEMP_DIR ;\
    touch $$TEMP_DIR/index.yaml ;\
	mv csi-wekafsplugin-$(VERSION).tgz $$TEMP_DIR; \
	cur_dir=`pwd` ;\
	cd $$TEMP_DIR ;\
	helm repo index $$TEMP_DIR --url "$(HELM_REPO_URL)" ;\
	git add . ;\
	git commit -m "Added version $(VERSION)" ;\
	git push
	cd $$cur_dir ;\
	rm -rf $$TEMP_DIR; \
	echo "New Helm Chart version pushed successfully to repository, index updated"


build: $(CMDS:%=build-%)
container: $(CMDS:%=container-%)
push: $(CMDS:%=push-%)
helm: $(CMDS:%=helm-%)

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

# This test only runs when dep >= 0.5 is installed, which is the case for the CI setup.
# When using 'go mod', we allow the test to be skipped in the Prow CI under some special
# circumstances, because it depends on accessing all remote repos and thus
# running it all the time would defeat the purpose of vendoring:
# - not handling a PR or
# - the fabricated merge commit leaves go.mod, go.sum and vendor dir unchanged
# - release-tools also didn't change (changing rules or Go version might lead to
#   a different result and thus must be tested)
# - import statements not changed (because if they change, go.mod might have to be updated)
#
# "git diff" is intelligent enough to annotate changes inside the "import" block in
# the start of the diff hunk:
#
# diff --git a/rpc/common.go b/rpc/common.go
# index bb4a5c4..5fa4271 100644
# --- a/rpc/common.go
# +++ b/rpc/common.go
# @@ -21,7 +21,6 @@ import (
#         "fmt"
#         "time"
#
# -       "google.golang.org/grpc"
#         "google.golang.org/grpc/codes"
#         "google.golang.org/grpc/status"
#
# We rely on that to find such changes.
#
# Vendoring is optional when using go.mod.

.PHONY: test-vendor
test: test-vendor
test-vendor:
	@ echo; echo "### $@:"
	@ ./release-tools/verify-vendor.sh

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
