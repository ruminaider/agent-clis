package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureLayout_CreatesDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	l, err := EnsureLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{l.AuditDir(), l.BlobsDir(), l.LocksDir()} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if !fi.IsDir() {
			t.Fatalf("%s is not a dir", d)
		}
	}
}

func TestEnsureLayout_Idempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	if _, err := EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureLayout(dir); err != nil {
		t.Fatalf("second call: %v", err)
	}
}
