// Command apiserver is the Sextant management-plane API server.
//
//	apiserver migrate        apply database migrations (run as the schema owner)
//	apiserver serve          serve the API (and, with TLS + a PKI root, agent enrollment and tunnels)
//	apiserver pki init       create a local-file root CA for dev/CI
//
// Secrets come from the environment, never flags (flags are visible in ps):
//
//	SEXTANT_OWNER_DATABASE_URL  schema-owner DSN, for migrate
//	SEXTANT_DATABASE_URL        sextant_app DSN, for serve (RLS applies to this role)
//	SEXTANT_DEV_TOKEN           dev-only bearer token, see --dev-tenant
package main

import (
	"context"
	stdtls "crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dpuig/sextant/pkg/controllers"
	"github.com/dpuig/sextant/pkg/enroll"
	"github.com/dpuig/sextant/pkg/pki"
	"github.com/dpuig/sextant/pkg/pki/localfile"
	"github.com/dpuig/sextant/pkg/registry"
	"github.com/dpuig/sextant/pkg/server"
	"github.com/dpuig/sextant/pkg/storage"
	"github.com/dpuig/sextant/pkg/tenancy"
	"github.com/dpuig/sextant/pkg/tunnel"
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
		return errors.New("usage: apiserver migrate|serve|pki|version")
	}
	switch args[0] {
	case "version":
		fmt.Println(version.Version)
		return nil
	case "migrate":
		return migrate(ctx)
	case "serve":
		return serve(ctx, args[1:])
	case "pki":
		return pkiCmd(args[1:])
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
	rootCert := fs.String("pki-root-cert", "", "root CA certificate; with --pki-root-key and TLS, enables agent enrollment and tunnels")
	rootKey := fs.String("pki-root-key", "", "root CA private key file (local-file signer; dev/CI)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*rootCert == "") != (*rootKey == "") {
		return errors.New("--pki-root-cert and --pki-root-key must be set together")
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

	var root *localfile.Root
	if *rootCert != "" {
		if !tls {
			return errors.New("agent enrollment and tunnels require --tls-cert/--tls-key")
		}
		if root, err = localfile.Load(*rootCert, *rootKey); err != nil {
			return fmt.Errorf("load PKI root: %w", err)
		}
	}
	// A nil *localfile.Root in a pki.Root interface would be non-nil: pass the
	// interface only when a root was actually configured.
	var pkiRoot pki.Root
	if root != nil {
		pkiRoot = root
	}
	handler, stop := buildHandler(ctx, pool, authn, authz, pkiRoot, slog.Default())
	defer stop()

	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // the cluster proxy route lifts this per request
		IdleTimeout:       120 * time.Second,
	}
	if tls {
		cert, err := stdtls.LoadX509KeyPair(*certFile, *keyFile)
		if err != nil {
			return fmt.Errorf("load TLS keypair: %w", err)
		}
		if root != nil {
			srv.TLSConfig = tunnel.ServerTLSConfig(cert, root.Certificate())
		} else {
			srv.TLSConfig = &stdtls.Config{MinVersion: stdtls.VersionTLS13, Certificates: []stdtls.Certificate{cert}}
		}
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("serving", "addr", *listen, "tls", tls, "version", version.Version)
		if tls {
			errc <- srv.ListenAndServeTLS("", "") // certificates are in srv.TLSConfig
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

// buildHandler assembles the management plane's HTTP surface. With a PKI root
// it also serves agent enrollment (/v1/) and the tunnel (/connect) and keeps
// Cluster.status in step with agent connectivity; without one it is the API
// alone. The returned func stops background work.
func buildHandler(ctx context.Context, pool *pgxpool.Pool, authn server.Authenticator, authz server.Authorizer, root pki.Root, log *slog.Logger) (http.Handler, func()) {
	reg := registry.New(storage.New(pool))
	tokens := storage.NewTokens(pool)
	opts := []server.Option{server.WithTokens(tokens)}
	mux := http.NewServeMux()
	stop := func() {}

	if root != nil {
		tun := tunnel.NewServer(nil, log)
		tracker := controllers.NewConnectionTracker(reg, log)
		tun.OnChange(tracker.OnChange)
		tctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { tracker.Run(tctx); close(done) }()
		stop = func() { cancel(); <-done }

		cas := pki.NewTenantCAs(pki.NewAuthority(root), 7*24*time.Hour)
		mux.Handle("/connect", tun)
		mux.Handle("/v1/", enroll.New(tokens, cas, nil, log))
		opts = append(opts, server.WithAgents(tun))
	}
	mux.Handle("/", server.New(reg, authn, authz, log, opts...))
	return mux, stop
}

// pkiCmd implements `apiserver pki init --dir DIR`.
func pkiCmd(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: apiserver pki init --dir DIR")
	}
	fs := flag.NewFlagSet("pki init", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to write root.crt and root.key")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	certPath, keyPath := filepath.Join(*dir, "root.crt"), filepath.Join(*dir, "root.key")
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite a root key", p)
		}
	}
	r, err := localfile.Generate("sextant root CA")
	if err != nil {
		return err
	}
	if err := r.Save(certPath, keyPath); err != nil {
		return err
	}
	fmt.Println("wrote", certPath, "and", keyPath)
	return nil
}
