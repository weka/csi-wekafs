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

CMDS=wekafsplugin
all: build

.PHONY: build-% build clean

# understand what is the version tag
VERSION=$(shell cat charts/csi-wekafsplugin/Chart.yaml | grep appVersion | awk '{print $$2}' | tr -d '"')
DOCKER_IMAGE_NAME=csi-wekafs

$(CMDS:%=build-%): build-%:
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE_NAME):$(VERSION) -f Dockerfile --label revision=$(VERSION) .

build: $(CMDS:%=build-%)

clean:
	-rm -rf bin
