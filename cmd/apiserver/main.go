// Command apiserver is the Sextant management-plane API server.
//
//	apiserver migrate   apply database migrations (run as the schema owner)
//	apiserver serve     serve the API
//
// Secrets come from the environment, never flags (flags are visible in ps):
//
//	SEXTANT_OWNER_DATABASE_URL  schema-owner DSN, for migrate
//	SEXTANT_DATABASE_URL        sextant_app DSN, for serve (RLS applies to this role)
//	SEXTANT_DEV_TOKEN           dev-only bearer token, see --dev-tenant
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
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/server"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("apiserver", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: apiserver migrate|serve|version")
	}
	switch args[0] {
	case "version":
		fmt.Println(version.Version)
		return nil
	case "migrate":
		return migrate(ctx)
	case "serve":
		return serve(ctx, args[1:])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func migrate(ctx context.Context) error {
	dsn := os.Getenv("SEXTANT_OWNER_DATABASE_URL")
	if dsn == "" {
		return errors.New("SEXTANT_OWNER_DATABASE_URL is required")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	return storage.Migrate(ctx, conn)
}

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8443", "address to listen on")
	certFile := fs.String("tls-cert", "", "TLS certificate file")
	keyFile := fs.String("tls-key", "", "TLS key file")
	devTenant := fs.String("dev-tenant", "", "DEV ONLY: tenant that SEXTANT_DEV_TOKEN grants full access to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tls := *certFile != "" || *keyFile != ""
	if tls && (*certFile == "" || *keyFile == "") {
		return errors.New("--tls-cert and --tls-key must be set together")
	}
	if err := checkListen(*listen, tls, *devTenant != ""); err != nil {
		return err
	}

	dsn := os.Getenv("SEXTANT_DATABASE_URL")
	if dsn == "" {
		return errors.New("SEXTANT_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database: %w", err)
	}

	var (
		authn server.Authenticator = denyAuthn{}
		authz server.Authorizer    = server.DenyAll{}
	)
	if *devTenant != "" {
		tid, err := tenancy.ParseID(*devTenant)
		if err != nil {
			return err
		}
		sa, err := server.NewStaticAuth(os.Getenv("SEXTANT_DEV_TOKEN"), tid)
		if err != nil {
			return fmt.Errorf("SEXTANT_DEV_TOKEN: %w", err)
		}
		authn, authz = sa, sa
		slog.Warn("DEV AUTH ENABLED: a static token grants full access to one tenant", "tenant", tid)
	} else {
		slog.Warn("no authentication configured: every request will be denied")
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.New(registry.New(storage.New(pool)), authn, authz, slog.Default()),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("serving", "addr", *listen, "tls", tls, "version", version.Version)
		if tls {
			errc <- srv.ListenAndServeTLS(*certFile, *keyFile)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// checkListen refuses configurations that would expose plaintext HTTP or the
// dev token beyond the local machine.
func checkListen(addr string, tls, dev bool) error {
	if tls && !dev {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--listen: %w", err)
	}
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	if dev {
		return errors.New("dev auth is only allowed on a loopback --listen address")
	}
	return errors.New("plain HTTP is only allowed on a loopback --listen address; set --tls-cert and --tls-key")
}

type denyAuthn struct{}

func (denyAuthn) Authenticate(*http.Request) (server.Principal, error) {
	return server.Principal{}, errors.New("no authenticator configured")
}
