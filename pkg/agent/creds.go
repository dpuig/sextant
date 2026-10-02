// Package agent is the in-cluster agent runtime: it obtains and renews its
// client certificate, and fronts the local kube-apiserver for the tunnel.
package agent

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials are the agent's client key and certificate chain (leaf first).
type Credentials struct {
	Key   *ecdsa.PrivateKey
	Chain [][]byte // DER, leaf first, then the tenant intermediate
}

// Leaf parses the leaf certificate.
func (c *Credentials) Leaf() (*x509.Certificate, error) {
	if len(c.Chain) == 0 {
		return nil, errors.New("agent: empty certificate chain")
	}
	return x509.ParseCertificate(c.Chain[0])
}

// TLS returns the credentials in crypto/tls form.
func (c *Credentials) TLS() *tls.Certificate {
	return &tls.Certificate{Certificate: c.Chain, PrivateKey: c.Key}
}

// Store persists credentials across restarts. Load returns (nil, nil) when
// nothing is stored yet.
type Store interface {
	Load() (*Credentials, error)
	Save(*Credentials) error
}

// FileStore keeps credentials in one PEM file (the key, then the chain) with
// mode 0600. A single file means a single atomic rename: there is no instant
// at which a crash can leave a new key next to an old chain.
type FileStore struct{ Dir string }

func (s FileStore) path() string { return filepath.Join(s.Dir, "agent-credentials.pem") }

func (s FileStore) Load() (*Credentials, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeCredentials(data)
}

// Save writes the credentials atomically (temp file + rename).
func (s FileStore) Save(c *Credentials) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	out, err := encodeCredentials(c)
	if err != nil {
		return err
	}
	return writeAtomic(s.path(), out, 0o600)
}

// encodeCredentials renders the key then the chain as PEM.
func encodeCredentials(c *Credentials) ([]byte, error) {
	kb, err := x509.MarshalECPrivateKey(c.Key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	for _, der := range c.Chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out, nil
}

// decodeCredentials parses encodeCredentials output and verifies that the
// certificate belongs to the key.
func decodeCredentials(data []byte) (*Credentials, error) {
	var (
		key   *ecdsa.PrivateKey
		chain [][]byte
		err   error
	)
	for rest := data; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		switch b.Type {
		case "EC PRIVATE KEY":
			if key, err = x509.ParseECPrivateKey(b.Bytes); err != nil {
				return nil, fmt.Errorf("agent: parse key: %w", err)
			}
		case "CERTIFICATE":
			chain = append(chain, b.Bytes)
		}
	}
	if key == nil || len(chain) == 0 {
		return nil, errors.New("agent: credentials are missing the key or certificate")
	}
	c := &Credentials{Key: key, Chain: chain}
	leaf, err := c.Leaf()
	if err != nil {
		return nil, err
	}
	if pub, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("agent: stored certificate does not match stored key")
	}
	return c, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
