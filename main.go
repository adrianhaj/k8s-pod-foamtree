// Command k8sfoams serves a read-only dashboard of where a cluster's
// requested CPU and memory go.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree/internal/kube"
	"github.com/adrianhaj/k8s-pod-foamtree/web"
)

type options struct {
	host                 string
	port                 int
	inCluster            bool
	allowUnauthenticated bool
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("k8sfoams", flag.ContinueOnError)
	fs.StringVar(&o.host, "host", "127.0.0.1", "address to listen on")
	fs.IntVar(&o.port, "port", 8080, "port to listen on")
	fs.BoolVar(&o.inCluster, "in-cluster", false, "use the pod's service account instead of kubeconfig")
	fs.BoolVar(&o.allowUnauthenticated, "allow-unauthenticated", false, "serve without auth on a non-loopback address")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if !isLoopback(o.host) && !o.allowUnauthenticated {
		return o, fmt.Errorf("refusing to serve cluster data without auth on %s: use --allow-unauthenticated", o.host)
	}
	return o, nil
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func run(ctx context.Context, o options) error {
	srv := &http.Server{
		Addr:              net.JoinHostPort(o.host, strconv.Itoa(o.port)),
		Handler:           newHandler(kube.NewSource(o.inCluster), web.Static),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("k8sfoams listening", "url", "http://"+srv.Addr, "inCluster", o.inCluster)
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
