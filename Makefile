ARGS ?=

# Load order matters: each file publishes its API on window.* for the next.
JSX := $(addprefix web/src/,nodestatus.jsx podaudit.jsx query.jsx workload.jsx tweaks-panel.jsx treemap.jsx cube3d.jsx app.jsx)
APP_JS := web/static/app.js

.PHONY: web build run test lint clean

web: $(APP_JS)

# Each file becomes its own IIFE: the sources redeclare top-level consts
# (e.g. `const { workloadKey }`), which in-browser Babel hid by compiling to var.
$(APP_JS): $(JSX)
	go tool esbuild $(JSX) --format=iife --minify --target=es2020 --log-level=warning --outdir=build/js
	cat $(patsubst web/src/%.jsx,build/js/%.js,$(JSX)) > $@

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/k8sfoams .

run: web
	go run . $(ARGS)

test: web
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

clean:
	rm -rf bin build $(APP_JS)
