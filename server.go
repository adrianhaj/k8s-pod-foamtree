package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree/internal/auth"
	"github.com/adrianhaj/k8s-pod-foamtree/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree/internal/kube"
)

type source interface {
	Contexts() ([]kube.Context, error)
	Snapshot(ctx context.Context, name string) ([]foam.Node, []foam.Pod, error)
}

// First load of a big cluster can take a while; later requests hit the cache.
const snapshotTimeout = 20 * time.Second

// A generous cap on the free-text /api/fit fields, well above any real
// selector or toleration list, so a client can't force a huge parse.
const maxFitFieldBytes = 4096

// a is nil when auth is off.
func newHandler(src source, static fs.FS, a *auth.Auth) http.Handler {
	app := http.NewServeMux()
	app.HandleFunc("GET /contexts", func(w http.ResponseWriter, r *http.Request) {
		cs, err := src.Contexts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, cs)
	})
	app.HandleFunc("GET /resources/{kind}", func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		axis, ok := map[string]foam.Axis{"cpu": foam.CPU, "memory": foam.Memory}[strings.ToLower(kind)]
		if !ok {
			http.Error(w, "Resource type: "+kind+" is not supported. Supported types are: [cpu, memory]", http.StatusBadRequest)
			return
		}
		if nodes, pods, ok := snapshot(w, r, src); ok {
			writeJSON(w, foam.Treemap(nodes, pods, axis))
		}
	})
	// Read-only dry run, so a GET: nothing to forge, and the link can be shared.
	app.HandleFunc("GET /api/fit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q.Get("nodeSelector")) > maxFitFieldBytes || len(q.Get("tolerations")) > maxFitFieldBytes {
			http.Error(w, "nodeSelector and tolerations must be at most 4096 bytes", http.StatusBadRequest)
			return
		}
		p, err := foam.Hypothetical(q.Get("cpu"), q.Get("memory"), q.Get("nodeSelector"), q.Get("tolerations"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if nodes, pods, ok := snapshot(w, r, src); ok {
			writeJSON(w, foam.Fit(nodes, pods, p))
		}
	})
	app.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := auth.UserFrom(r.Context()); ok {
			writeJSON(w, map[string]string{"auth": "oidc", "email": u.Email, "name": u.Name})
			return
		}
		writeJSON(w, map[string]string{"auth": "none"})
	})
	app.Handle("GET /", http.FileServerFS(static))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthcheck", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	if a == nil {
		root.Handle("/", app)
	} else {
		a.Register(root)
		root.Handle("/", a.Require(app))
	}
	return secure(root)
}

// snapshot reads the ?context= cluster, writing the error response itself
// when that fails.
func snapshot(w http.ResponseWriter, r *http.Request, src source) ([]foam.Node, []foam.Pod, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
	defer cancel()
	nodes, pods, err := src.Snapshot(ctx, r.URL.Query().Get("context"))
	switch {
	case errors.Is(err, kube.ErrUnknownContext):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		slog.Warn("snapshot", "err", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		return nodes, pods, true
	}
	return nil, nil, false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}

// Fonts are the only third-party load; everything else is embedded.
const csp = "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; img-src 'self' data:; frame-ancestors 'none'"

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
