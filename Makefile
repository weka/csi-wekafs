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

.PHONY: build-% build clean build-debug deploy-debug

# understand what is the version tag
VERSION ?= $(shell cat charts/csi-wekafsplugin/Chart.yaml | grep appVersion | awk '{print $$2}' | tr -d '"')
TIMESTAMP := $(shell date +%Y%m%d-%H%M%S)
DEBUG_VERSION ?= $(VERSION)-debug-$(TIMESTAMP)
DOCKER_IMAGE_NAME?=csi-wekafs
DEBUG_IMAGE_NAME?=csi-wekafs-debug
QUAY_REGISTRY?=quay.io/weka.io
DEBUG_IMAGE?=$(QUAY_REGISTRY)/$(DEBUG_IMAGE_NAME):$(DEBUG_VERSION)

$(CMDS:%=build-%): build-%:
	docker buildx build --platform linux/amd64 --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE_NAME):$(VERSION) -f Dockerfile --label revision=$(VERSION) .

build: $(CMDS:%=build-%)

push: build
	docker push $(DOCKER_IMAGE_NAME):$(VERSION)

clean:
	-rm -rf bin

# Build debug binaries locally (fast on native arch)
.PHONY: build-debug-binaries
build-debug-binaries:
	@echo "🔨 Building debug binaries locally for linux/amd64..."
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-gcflags="all=-N -l" \
		-ldflags "-X main.version=$(DEBUG_VERSION)" \
		-o ./bin/wekafsplugin-debug \
		./cmd/wekafsplugin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-gcflags="all=-N -l" \
		-ldflags "-X main.version=$(DEBUG_VERSION)" \
		-o ./bin/metricsserver-debug \
		./cmd/metricsserver
	@echo "✅ Debug binaries built in ./bin/"

# Build debug image with Delve and debug symbols
.PHONY: build-debug
build-debug: build-debug-binaries
	@echo "🔨 Building DEBUG Docker image..."
	docker buildx build --platform linux/amd64 \
		--build-arg VERSION=$(DEBUG_VERSION) \
		-t $(DEBUG_IMAGE_NAME):$(DEBUG_VERSION) \
		-t $(DEBUG_IMAGE) \
		-f debug.Dockerfile \
		--label revision=$(DEBUG_VERSION) \
		--label debug=true \
		--load .
	@echo "✅ Debug image built: $(DEBUG_IMAGE)"

# Complete debug deployment: build, push to Quay, and update deployment
.PHONY: deploy-debug
deploy-debug: build-debug
	@echo "📤 Pushing debug image to Quay.io..."
	docker push $(DEBUG_IMAGE)
	@echo "✅ Debug image pushed: $(DEBUG_IMAGE)"
	@echo ""
	@echo "🚀 Deploying debug image to cluster..."
	@# Get the release name and namespace from helm
	@RELEASE_NAME=$$(helm list --all-namespaces -o json | jq -r '.[] | select(.chart | startswith("csi-wekafsplugin")) | .name' | head -n1); \
	NAMESPACE=$$(helm list --all-namespaces -o json | jq -r '.[] | select(.chart | startswith("csi-wekafsplugin")) | .namespace' | head -n1); \
	if [ -z "$$RELEASE_NAME" ] || [ -z "$$NAMESPACE" ]; then \
		echo "❌ Could not find csi-wekafsplugin helm release. Please specify manually:"; \
		echo "  RELEASE_NAME=<name> NAMESPACE=<ns> make deploy-debug"; \
		exit 1; \
	fi; \
	echo "Found release: $$RELEASE_NAME in namespace: $$NAMESPACE"; \
	echo "Setting imagePullPolicy to Always to force pull..."; \
	kubectl patch deployment $$RELEASE_NAME-controller -n $$NAMESPACE --type='json' \
		-p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Always"}]' 2>/dev/null || echo "⚠️  Failed to patch controller imagePullPolicy"; \
	kubectl patch daemonset $$RELEASE_NAME-node -n $$NAMESPACE --type='json' \
		-p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Always"}]' 2>/dev/null || echo "⚠️  Failed to patch node imagePullPolicy"; \
	echo "Updating controller deployment..."; \
	kubectl set image deployment/$$RELEASE_NAME-controller -n $$NAMESPACE wekafs=$(DEBUG_IMAGE) || echo "⚠️  Controller deployment not found or failed to update"; \
	echo "Updating node daemonset..."; \
	kubectl set image daemonset/$$RELEASE_NAME-node -n $$NAMESPACE wekafs=$(DEBUG_IMAGE) || echo "⚠️  Node daemonset not found or failed to update"; \
	echo "Forcing pod restart to pull new image..."; \
	kubectl rollout restart deployment/$$RELEASE_NAME-controller -n $$NAMESPACE 2>/dev/null || echo "⚠️  Failed to restart controller"; \
	kubectl rollout restart daemonset/$$RELEASE_NAME-node -n $$NAMESPACE 2>/dev/null || echo "⚠️  Failed to restart node"; \
	echo ""; \
	echo "⏰ Adjusting timeouts and enabling Delve for debugging..."; \
	kubectl patch deployment $$RELEASE_NAME-controller -n $$NAMESPACE --type='json' \
		-p='[{"op": "remove", "path": "/spec/template/spec/containers/0/livenessProbe"}]' 2>/dev/null || echo "⚠️  No liveness probe to remove from controller"; \
	kubectl patch daemonset $$RELEASE_NAME-node -n $$NAMESPACE --type='json' \
		-p='[{"op": "remove", "path": "/spec/template/spec/containers/0/livenessProbe"}]' 2>/dev/null || echo "⚠️  No liveness probe to remove from node"; \
	echo "Setting up Delve entrypoint for controller..."; \
	kubectl patch deployment $$RELEASE_NAME-controller -n $$NAMESPACE --type='json' \
		-p='[{"op":"add","path":"/spec/template/spec/containers/0/command","value":["dlv","exec","--api-version=2","--headless","--listen=0.0.0.0:2345","--accept-multiclient","/wekafsplugin","--"]}]' 2>/dev/null || echo "⚠️  Failed to patch controller command"; \
	echo "Setting up Delve entrypoint for node..."; \
	kubectl patch daemonset $$RELEASE_NAME-node -n $$NAMESPACE --type='json' \
		-p='[{"op":"add","path":"/spec/template/spec/containers/0/command","value":["dlv","exec","--api-version=2","--headless","--listen=0.0.0.0:2345","--accept-multiclient","/wekafsplugin","--"]}]' 2>/dev/null || echo "⚠️  Failed to patch node command"; \
	echo "Exposing Delve port 2345 in controller..."; \
	kubectl patch deployment $$RELEASE_NAME-controller -n $$NAMESPACE --type='json' \
		-p='[{"op":"add","path":"/spec/template/spec/containers/0/ports/-","value":{"containerPort":2345,"name":"delve","protocol":"TCP"}}]' 2>/dev/null || echo "⚠️  Port may already exist or failed to add"; \
	echo "Exposing Delve port 2345 in node..."; \
	kubectl patch daemonset $$RELEASE_NAME-node -n $$NAMESPACE --type='json' \
		-p='[{"op":"add","path":"/spec/template/spec/containers/0/ports/-","value":{"containerPort":2345,"name":"delve","protocol":"TCP"}}]' 2>/dev/null || echo "⚠️  Port may already exist or failed to add"; \
	echo ""; \
	echo "⏳ Waiting for pods to restart..."; \
	sleep 5; \
	kubectl wait --for=condition=ready pod -l app=$$RELEASE_NAME-controller -n $$NAMESPACE --timeout=120s 2>/dev/null || echo "⚠️  Controller pod not ready yet"; \
	kubectl wait --for=condition=ready pod -l app=$$RELEASE_NAME-node -n $$NAMESPACE --timeout=120s 2>/dev/null || echo "⚠️  Node pods not ready yet"; \
	echo ""; \
	echo "✅ Debug deployment complete!"; \
	echo ""; \
	echo "📎 To start debugging:"; \
	echo "  1. Find a pod:"; \
	echo "     Controller: kubectl get pods -n $$NAMESPACE -l app=$$RELEASE_NAME-controller"; \
	echo "     Node:       kubectl get pods -n $$NAMESPACE -l app=$$RELEASE_NAME-node"; \
	echo ""; \
	echo "  2. Port forward to the pod:"; \
	echo "     kubectl port-forward <pod-name> 2345:2345 -n $$NAMESPACE"; \
	echo ""; \
	echo "  3. Connect your debugger to localhost:2345"; \
	echo "     - GoLand/IntelliJ: Run -> Edit Configurations -> Go Remote"; \
	echo "     - VSCode: Use 'Connect to server' launch configuration with port 2345"; \
	echo ""; \
	echo "⚠️  Note: Liveness probes removed and Delve enabled. Restore normal operation with helm upgrade when done."; \
	echo ""; \
	echo "💡 Tip: Use '--accept-multiclient' flag allows multiple debugger connections"
