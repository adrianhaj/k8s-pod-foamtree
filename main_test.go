package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/adrianhaj/k8s-pod-foamtree/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree/internal/kube"
)

type fakeSource struct {
	nodes []foam.Node
	pods  []foam.Pod
	err   error
	asked string
}

func (f *fakeSource) Contexts() ([]kube.Context, error) {
	return []kube.Context{{Context: "kind", Active: true}}, nil
}

func (f *fakeSource) Snapshot(_ context.Context, name string) ([]foam.Node, []foam.Pod, error) {
	f.asked = name
	return f.nodes, f.pods, f.err
}

var static = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>k8sfoams</title>")}}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}

func TestRoutes(t *testing.T) {
	src := &fakeSource{
		nodes: []foam.Node{{Name: "minikube", CPU: 2000, Memory: 1_000_000}},
		pods:  []foam.Pod{{Name: "etcd", NodeName: "minikube", CPU: 150, Containers: []foam.Container{{Name: "etcd", CPU: 150}}}},
	}
	h := newHandler(src, static, nil)
	cases := []struct {
		target string
		code   int
		body   string
	}{
		{"/healthcheck", 200, `{"status":"ok"}`},
		{"/contexts", 200, `[{"context":"kind","active":true}]`},
		{"/resources/cpu", 200, `"label":"minikube","weight":2000`},
		{"/resources/MEMORY", 200, `"label":"minikube","weight":1000000`},
		{"/resources/disk", 400, "Resource type: disk is not supported. Supported types are: [cpu, memory]"},
		{"/api/me", 200, `{"auth":"none"}`},
		{"/", 200, "<title>k8sfoams</title>"},
	}
	for _, tc := range cases {
		w := get(h, tc.target)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
			t.Errorf("%s: %d %q", tc.target, w.Code, w.Body)
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", tc.target)
		}
	}
}

func TestResourcesPassesContextAndMapsErrors(t *testing.T) {
	src := &fakeSource{}
	h := newHandler(src, static, nil)
	if get(h, "/resources/cpu?context=kind-a"); src.asked != "kind-a" {
		t.Fatalf("context not forwarded: %q", src.asked)
	}
	src.err = fmt.Errorf("%w %q", kube.ErrUnknownContext, "prod")
	if w := get(h, "/resources/cpu?context=prod"); w.Code != 400 {
		t.Fatalf("unknown context: %d", w.Code)
	}
	src.err = errors.New(`cluster "kind": Unauthorized`)
	if w := get(h, "/resources/cpu"); w.Code != 503 || !strings.Contains(w.Body.String(), "Unauthorized") {
		t.Fatalf("cluster error: %d %s", w.Code, w.Body)
	}
}

func TestFlags(t *testing.T) {
	cases := []struct {
		args []string
		ok   bool
	}{
		{nil, true},
		{[]string{"--host", "0.0.0.0"}, false},
		{[]string{"--host", "0.0.0.0", "--allow-unauthenticated"}, true},
		{[]string{"--host", "0.0.0.0", "--auth", "oidc"}, true},
		{[]string{"--host", "::1"}, true},
		{[]string{"--auth", "basic"}, false},
	}
	for _, tc := range cases {
		if _, err := parseFlags(tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: err=%v", tc.args, err)
		}
	}
	o, _ := parseFlags([]string{"--auth", "oidc", "--oidc-allowed-emails", "a@x.com,*@y.com"})
	if fmt.Sprint(o.oidc.AllowedEmails, o.oidc.Scopes) != "[a@x.com *@y.com] [openid email profile]" {
		t.Fatalf("lists: %v %v", o.oidc.AllowedEmails, o.oidc.Scopes)
	}
}

func TestSessionKey(t *testing.T) {
	if k, err := sessionKey(""); err != nil || len(k) != 32 {
		t.Fatalf("random key: %d %v", len(k), err)
	}
	if _, err := sessionKey("c2hvcnQ="); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestFitRoute(t *testing.T) {
	src := &fakeSource{nodes: []foam.Node{
		{Name: "big", AllocCPU: 8000, AllocMemory: 32_000_000, AllocPods: 110},
		{Name: "small", AllocCPU: 1000, AllocMemory: 32_000_000, AllocPods: 110},
	}}
	h := newHandler(src, static, nil)
	w := get(h, "/api/fit?context=kind-a&cpu=2&memory=1Gi")
	want := `[{"node":"big","reasons":[]},{"node":"small","reasons":["insufficient cpu: requires 2000m, available 1000m"]}]` + "\n"
	if w.Code != 200 || w.Body.String() != want || src.asked != "kind-a" {
		t.Fatalf("%d %s (context %q)", w.Code, w.Body, src.asked)
	}
	if w := get(h, "/api/fit?cpu=lots"); w.Code != 400 || !strings.Contains(w.Body.String(), `cpu "lots"`) {
		t.Fatalf("bad input: %d %s", w.Code, w.Body)
	}
	src.err = errors.New("Unauthorized")
	if w := get(h, "/api/fit?cpu=1"); w.Code != 503 {
		t.Fatalf("cluster error: %d", w.Code)
	}
}

func TestFitRouteRejectsOversizedFreeText(t *testing.T) {
	h := newHandler(&fakeSource{}, static, nil)
	big := strings.Repeat("a", 4097)
	if w := get(h, "/api/fit?tolerations="+big); w.Code != 400 {
		t.Fatalf("oversized tolerations: %d", w.Code)
	}
	if w := get(h, "/api/fit?nodeSelector="+big); w.Code != 400 {
		t.Fatalf("oversized nodeSelector: %d", w.Code)
	}
}
