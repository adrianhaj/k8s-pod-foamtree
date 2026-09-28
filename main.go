// Command k8sfoams serves a read-only dashboard of where a cluster's
// requested CPU and memory go.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree/internal/auth"
	"github.com/adrianhaj/k8s-pod-foamtree/internal/kube"
	"github.com/adrianhaj/k8s-pod-foamtree/web"
)

type options struct {
	host                 string
	port                 int
	inCluster            bool
	authMode             string
	allowUnauthenticated bool
	oidc                 auth.Config
	emails, groups       string
	scopes, sessionKey   string
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("k8sfoams", flag.ContinueOnError)
	fs.StringVar(&o.host, "host", "127.0.0.1", "address to listen on")
	fs.IntVar(&o.port, "port", 8080, "port to listen on")
	fs.BoolVar(&o.inCluster, "in-cluster", false, "use the pod's service account instead of kubeconfig")
	fs.StringVar(&o.authMode, "auth", "none", "none or oidc")
	fs.BoolVar(&o.allowUnauthenticated, "allow-unauthenticated", false, "allow --auth=none on a non-loopback address")
	fs.StringVar(&o.oidc.Issuer, "oidc-issuer", "", "OIDC issuer URL")
	fs.StringVar(&o.oidc.ClientID, "oidc-client-id", "", "OIDC client id")
	fs.StringVar(&o.oidc.RedirectURL, "oidc-redirect-url", "", "public URL of /auth/callback")
	fs.StringVar(&o.oidc.GroupsClaim, "oidc-groups-claim", "groups", "ID token claim holding group names")
	fs.StringVar(&o.scopes, "oidc-scopes", "openid,email,profile", "comma-separated scopes")
	fs.StringVar(&o.emails, "oidc-allowed-emails", "", "comma-separated emails or globs like *@example.com")
	fs.StringVar(&o.groups, "oidc-allowed-groups", "", "comma-separated groups")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	// Secrets come from the environment only: flags show up in `ps`.
	o.oidc.ClientSecret = os.Getenv("K8SFOAMS_OIDC_CLIENT_SECRET")
	o.sessionKey = os.Getenv("K8SFOAMS_SESSION_KEY")
	o.oidc.Scopes, o.oidc.AllowedEmails, o.oidc.AllowedGroups = list(o.scopes), list(o.emails), list(o.groups)

	switch o.authMode {
	case "none":
		if !isLoopback(o.host) && !o.allowUnauthenticated {
			return o, fmt.Errorf("refusing to serve cluster data without auth on %s: use --auth=oidc or --allow-unauthenticated", o.host)
		}
	case "oidc":
	default:
		return o, fmt.Errorf("--auth must be none or oidc, got %q", o.authMode)
	}
	return o, nil
}

func list(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func sessionKey(encoded string) ([]byte, error) {
	if encoded == "" {
		slog.Warn("K8SFOAMS_SESSION_KEY unset: using a random key, sessions end on restart")
		key := make([]byte, 32)
		rand.Read(key)
		return key, nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("K8SFOAMS_SESSION_KEY must be 32 bytes, base64-encoded (openssl rand -base64 32)")
	}
	return key, nil
}

func run(ctx context.Context, o options) error {
	var a *auth.Auth
	if o.authMode == "oidc" {
		key, err := sessionKey(o.sessionKey)
		if err != nil {
			return err
		}
		o.oidc.SessionKey = key
		if a, err = auth.New(ctx, o.oidc); err != nil {
			return err
		}
	}
	srv := &http.Server{
		Addr:              net.JoinHostPort(o.host, strconv.Itoa(o.port)),
		Handler:           newHandler(kube.NewSource(o.inCluster), web.Static, a),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("k8sfoams listening", "url", "http://"+srv.Addr, "auth", o.authMode, "inCluster", o.inCluster)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	o, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		slog.Error(err.Error())
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, o); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
