package sops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmoove/tplr/internal/aws"
)

// withDecrypt replaces the package decryptFile hook for the duration of a test
// and creates a placeholder file so the os.Stat existence check passes.
func withDecrypt(t *testing.T, clear []byte, err error) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	if writeErr := os.WriteFile(path, []byte("placeholder"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	orig := decryptFile
	decryptFile = func(string) ([]byte, error) { return clear, err }
	t.Cleanup(func() { decryptFile = orig })
	return path
}

func TestDecryptWholeFile(t *testing.T) {
	path := withDecrypt(t, []byte("db:\n  password: pw\n"), nil)
	got, err := DecryptFile(path, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if want := "db:\n  password: pw"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestDecryptNestedKey(t *testing.T) {
	path := withDecrypt(t, []byte("db:\n  password: pw\n  port: 5432\n"), nil)
	for _, tc := range []struct{ key, want string }{
		{"db.password", "pw"},
		{"db.port", "5432"},
	} {
		got, err := DecryptFile(path, tc.key)
		if err != nil {
			t.Fatalf("key %s: %v", tc.key, err)
		}
		if got != tc.want {
			t.Fatalf("key %s: want %q got %q", tc.key, tc.want, got)
		}
	}
}

func TestDecryptJSONKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := decryptFile
	decryptFile = func(string) ([]byte, error) { return []byte(`{"api":{"token":"abc"}}`), nil }
	t.Cleanup(func() { decryptFile = orig })

	got, err := DecryptFile(path, "api.token")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if want := "abc"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestDecryptDotenvKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := decryptFile
	decryptFile = func(string) ([]byte, error) {
		return []byte("# comment\nDB_PASSWORD=\"pw\"\nAPI_TOKEN=abc\n"), nil
	}
	t.Cleanup(func() { decryptFile = orig })

	got, err := DecryptFile(path, "DB_PASSWORD")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if want := "pw"; got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestDecryptMissingKeyIsNotFound(t *testing.T) {
	path := withDecrypt(t, []byte("db:\n  password: pw\n"), nil)
	_, err := DecryptFile(path, "db.absent")
	if !aws.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestDecryptMissingFileIsNotFound(t *testing.T) {
	_, err := DecryptFile(filepath.Join(t.TempDir(), "nope.yaml"), "")
	if !aws.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestDecryptFailurePropagates(t *testing.T) {
	path := withDecrypt(t, nil, errors.New("no matching keys found"))
	_, err := DecryptFile(path, "")
	if err == nil || aws.IsNotFound(err) {
		t.Fatalf("expected real decryption error, got %v", err)
	}
}
