// Package localfile is a pki.Root backed by PEM files on disk. It is for
// development and CI only; production uses the Vault (or a cloud KMS) Root.
package localfile

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"
)

// Root is a file-backed trust anchor.
type Root struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// Generate creates a fresh self-signed root valid for 10 years.
func Generate(commonName string) (*Root, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Root{cert: cert, key: key}, nil
}

// Load reads a root from PEM files. The key file must not be readable by
// group or others.
func Load(certPath, keyPath string) (*Root, error) {
	fi, err := os.Stat(keyPath)
	if err != nil {
		return nil, err
	}
	// Group-read is allowed: Kubernetes mounts Secret volumes root:<fsGroup>, so
	// a non-root pod can only read its key through its group. Group-write and
	// any access by others are not.
	if fi.Mode().Perm()&0o027 != 0 {
		return nil, fmt.Errorf("localfile: key %s has mode %v; must not be group-writable or accessible to others", keyPath, fi.Mode().Perm())
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, errors.New("localfile: cert or key is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("localfile: certificate does not match private key")
	}
	return &Root{cert: cert, key: key}, nil
}

// Save writes the root to PEM files, the key with mode 0600.
func (r *Root) Save(certPath, keyPath string) error {
	kb, err := x509.MarshalECPrivateKey(r.key)
	if err != nil {
		return err
	}
	// Certificate first, so a key file on disk always has its cert beside it.
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.cert.Raw}), 0o644); err != nil {
		return err
	}
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// The mode argument above only applies on creation; enforce it on reuse.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Certificate returns the root certificate.
func (r *Root) Certificate() *x509.Certificate { return r.cert }

// Public implements crypto.Signer.
func (r *Root) Public() crypto.PublicKey { return r.key.Public() }

// Sign implements crypto.Signer.
func (r *Root) Sign(rnd io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	return r.key.Sign(rnd, digest, opts)
}
