// Command agent is the in-cluster Sextant agent. It enrolls with a one-time
// registration token, keeps its certificate fresh, and holds one outbound
// mTLS tunnel to the management plane through which that plane (and only that
// plane) can reach this cluster's kube-apiserver. It opens no listening ports.
package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dpuig/sextant/pkg/agent"
	"github.com/dpuig/sextant/pkg/tunnel"
	"github.com/dpuig/sextant/pkg/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "version" {
		fmt.Println(version.Version)
		return nil
	}
	cfg, err := parseConfig(args, os.Getenv, os.ReadFile)
	if err != nil {
		return err
	}
	log := slog.Default()

	var roots *x509.CertPool // nil: system roots
	if cfg.serverCAFile != "" {
		pem, err := os.ReadFile(cfg.serverCAFile)
		if err != nil {
			return fmt.Errorf("read server CA: %w", err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("server CA file has no certificates")
		}
	} else if roots, err = x509.SystemCertPool(); err != nil {
		return fmt.Errorf("system cert pool: %w", err)
	}

	var store agent.Store = agent.FileStore{Dir: cfg.stateDir}
	if cfg.credentialsSecret != "" {
		store, err = agent.NewSecretStore(cfg.kubeAPI, cfg.kubeCAFile, cfg.kubeTokenFile, cfg.namespace, cfg.credentialsSecret)
		if err != nil {
			return err
		}
	}
	mgr := agent.NewManager(&agent.Client{BaseURL: cfg.managementURL.String(), Roots: roots}, store, cfg.token, log)
	if err := mgr.Init(ctx); err != nil {
		return err
	}
	go mgr.Run(ctx)

	kube, err := agent.NewKubeProxy(cfg.kubeAPI, cfg.kubeCAFile, cfg.kubeTokenFile, log)
	if err != nil {
		return err
	}
	t, err := tunnel.NewAgent(tunnel.AgentConfig{
		URL:         cfg.tunnelURL(),
		Roots:       roots,
		Certificate: mgr.Certificate,
		AllowedAddr: tunnel.KubeAPIAddr,
		Handler:     kube,
		Log:         log,
	})
	if err != nil {
		return err
	}
	log.Info("agent starting", "version", version.Version, "management", cfg.managementURL.Host)
	return t.Run(ctx)
}
