package localfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	r, err := Generate("test-root")
	if err != nil {
		t.Fatal(err)
	}
	cert, key := filepath.Join(dir, "root.crt"), filepath.Join(dir, "root.key")
	if err := r.Save(cert, key); err != nil {
		t.Fatal(err)
	}
	got, err := Load(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Certificate().Equal(r.Certificate()) {
		t.Fatal("loaded cert differs")
	}
}

func TestSave_KeyFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	r, _ := Generate("test-root")
	key := filepath.Join(dir, "root.key")
	if err := r.Save(filepath.Join(dir, "root.crt"), key); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(key)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestLoad_RejectsLooseKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	r, _ := Generate("test-root")
	cert, key := filepath.Join(dir, "root.crt"), filepath.Join(dir, "root.key")
	_ = r.Save(cert, key)
	_ = os.Chmod(key, 0o644)
	if _, err := Load(cert, key); err == nil {
		t.Fatal("expected group/world-readable key to be rejected")
	}
}

// Kubernetes mounts Secret volumes root:<fsGroup> with the requested mode, so a
// non-root pod can only read its key if group-read is allowed.
func TestLoad_ModePolicy(t *testing.T) {
	for mode, wantOK := range map[os.FileMode]bool{
		0o600: true, 0o400: true, 0o440: true, // owner-only, or group-read (Secret volumes)
		0o640: true,
		0o660: false, 0o620: false, // group-write
		0o644: false, 0o604: false, 0o601: false, 0o444: false, // any access by others
	} {
		dir := t.TempDir()
		r, _ := Generate("test-root")
		cert, key := filepath.Join(dir, "root.crt"), filepath.Join(dir, "root.key")
		_ = r.Save(cert, key)
		_ = os.Chmod(key, mode)
		_, err := Load(cert, key)
		if (err == nil) != wantOK {
			t.Errorf("mode %v: err = %v, want ok=%v", mode, err, wantOK)
		}
	}
}

func TestLoad_RejectsMismatchedKey(t *testing.T) {
	dir := t.TempDir()
	a, _ := Generate("a")
	b, _ := Generate("b")
	_ = a.Save(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	_ = b.Save(filepath.Join(dir, "b.crt"), filepath.Join(dir, "b.key"))
	if _, err := Load(filepath.Join(dir, "a.crt"), filepath.Join(dir, "b.key")); err == nil {
		t.Fatal("expected cert/key mismatch to be rejected")
	}
}

func TestSave_TightensExistingKeyFileMode(t *testing.T) {
	dir := t.TempDir()
	r, _ := Generate("test-root")
	key := filepath.Join(dir, "root.key")
	if err := os.WriteFile(key, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(filepath.Join(dir, "root.crt"), key); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(key)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, want 0600", fi.Mode().Perm())
	}
}
