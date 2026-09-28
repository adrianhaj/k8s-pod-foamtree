package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
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
	// Plain "attachment": a filename here would override the UI's download name.
	app.HandleFunc("GET /report.json", func(w http.ResponseWriter, r *http.Request) {
		if nodes, pods, ok := snapshot(w, r, src); ok {
			w.Header().Set("Content-Disposition", "attachment")
			writeJSON(w, foam.Report(nodes, pods))
		}
	})
	app.HandleFunc("GET /report.csv", func(w http.ResponseWriter, r *http.Request) {
		if nodes, pods, ok := snapshot(w, r, src); ok {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment")
			writeCSV(w, foam.Report(nodes, pods))
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

// snapshot writes the error response itself when it returns ok=false.
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

func writeCSV(w http.ResponseWriter, rows []foam.ReportRow) {
	opt := func(v *int64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatInt(*v, 10)
	}
	cw := csv.NewWriter(w)
	cw.Write([]string{"node", "zone", "pool", "instance_type", "node_cpu_m", "node_memory_bytes", "node_warnings",
		"namespace", "pod", "qos", "cpu_request_m", "cpu_limit_m", "memory_request_bytes", "memory_limit_bytes", "findings"})
	for _, r := range rows {
		cw.Write([]string{r.Node, r.Zone, r.Pool, r.InstanceType, strconv.FormatInt(r.NodeCPU, 10),
			strconv.FormatInt(r.NodeMemoryBytes, 10), strings.Join(r.NodeWarnings, " "),
			r.Namespace, r.Pod, r.QOS, strconv.FormatInt(r.CPU, 10), opt(r.CPULimit),
			strconv.FormatInt(r.MemoryBytes, 10), opt(r.MemoryLimitBytes), strings.Join(r.Findings, " ")})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slog.Warn("write response", "err", err)
	}
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
