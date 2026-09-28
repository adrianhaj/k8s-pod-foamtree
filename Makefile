IMAGE     ?= ghcr.io/adrianhaj/k8sfoams
TAG       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PLATFORMS ?= linux/amd64,linux/arm64
ARGS      ?=
# kind keeps its credentials here, never in ~/.kube/config.
KIND_KUBECONFIG ?= $(CURDIR)/kind.kubeconfig
KIND_KUBECTL    := kubectl --kubeconfig $(KIND_KUBECONFIG) --context kind-k8sfoams

# Load order matters: each file publishes its API on window.* for the next.
JSX := $(addprefix web/src/,nodestatus.jsx podaudit.jsx qos.jsx query.jsx workload.jsx topology.jsx tweaks-panel.jsx treemap.jsx scene3d.jsx app.jsx)
APP_JS := web/static/app.js
THREE_JS := web/static/three.js

.PHONY: web build run test lint image image-push kind-up kind-down kind-load deploy-dev port-forward clean

web: $(APP_JS) $(THREE_JS)

# Each file becomes its own IIFE: the sources redeclare top-level consts
# (e.g. `const { workloadKey }`), which in-browser Babel hid by compiling to var.
$(APP_JS): $(JSX)
	go tool esbuild $(JSX) --format=iife --minify --target=es2020 --log-level=warning --outdir=build/js
	cat $(patsubst web/src/%.jsx,build/js/%.js,$(JSX)) > $@

# three.js ships only ES modules: bundle the parts we import into one IIFE
# that sets window.THREE, like the React UMD builds.
$(THREE_JS): web/src/three-entry.js $(wildcard web/vendor/three/*.js)
	go tool esbuild web/src/three-entry.js --bundle --format=iife --minify --target=es2020 \
		--alias:three=./web/vendor/three/three.module.js --log-level=warning --outfile=$@

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/k8sfoams .

run: web
	go run . $(ARGS)

test: web
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

image:
	docker build -t $(IMAGE):$(TAG) .

image-push:
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE):$(TAG) --push .

kind-up:
	kind get clusters | grep -qx k8sfoams || kind create cluster --name k8sfoams --kubeconfig $(KIND_KUBECONFIG)
	kind export kubeconfig --name k8sfoams --kubeconfig $(KIND_KUBECONFIG)

kind-down:
	kind delete cluster --name k8sfoams --kubeconfig $(KIND_KUBECONFIG)

kind-load: image
	docker tag $(IMAGE):$(TAG) $(IMAGE):dev
	kind load docker-image $(IMAGE):dev --name k8sfoams

deploy-dev: kind-load
	$(KIND_KUBECTL) apply -k deploy/overlays/dev
	$(KIND_KUBECTL) -n k8sfoams rollout restart deploy/k8sfoams

port-forward:
	$(KIND_KUBECTL) -n k8sfoams port-forward svc/k8sfoams 8080:80

clean:
	rm -rf bin build $(APP_JS) $(THREE_JS)
