// Package sops decrypts SOPS-encrypted files for use as a tplr secret source.
//
// SOPS (https://github.com/getsops/sops) stores secrets encrypted at rest with
// a data key wrapped by a KMS/age/PGP/... key. Decryption happens locally using
// whatever key material the caller can access (AWS KMS via the default credential
// chain, the SOPS_AGE_KEY environment, a GnuPG keyring, ...); tplr does not need
// any extra configuration beyond what the sops CLI itself would use.
package sops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getsops/sops/v3/decrypt"
	"gopkg.in/yaml.v3"

	"github.com/dmoove/tplr/internal/aws"
)

// decryptFile is the decryption entry point, overridable in tests so they do not
// need real key material. It returns the cleartext document in its original
// format (yaml stays yaml, json stays json, dotenv stays dotenv).
var decryptFile = func(path string) ([]byte, error) {
	// An empty format lets sops infer the format from the file extension, the
	// same logic the sops CLI uses.
	return decrypt.File(path, "")
}

// DecryptFile decrypts the SOPS-encrypted file at path. When key is empty the
// whole decrypted document is returned (trailing newline trimmed, like the file:
// source). When key is set it is treated as a dotted path into the decrypted
// document (e.g. "db.password") and only that value is returned.
//
// A missing file or a key that does not exist in the document is reported as an
// aws.NotFoundError so the default/required modifiers and --ignore-missing treat
// it as absent. A decryption failure (wrong/unavailable keys, tampered file) is
// returned verbatim so it always fails the render.
func DecryptFile(path, key string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", aws.NotFound("sops file %s not found", path)
		}
		return "", fmt.Errorf("stat sops file %s: %w", path, err)
	}

	clear, err := decryptFile(path)
	if err != nil {
		return "", fmt.Errorf("decrypt sops file %s: %w", path, err)
	}

	if key == "" {
		return strings.TrimRight(string(clear), "\n"), nil
	}
	return extract(path, clear, key)
}

// extract pulls a dotted key path out of a decrypted document. The document is
// parsed according to the file extension; yaml and json share the YAML parser
// (JSON is a subset of YAML), dotenv is parsed as KEY=VALUE lines.
func extract(path string, clear []byte, key string) (string, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".env", ".dotenv":
		return extractDotenv(path, clear, key)
	case ".yaml", ".yml", ".json", "":
		return extractTree(path, clear, key)
	default:
		return "", fmt.Errorf("sops file %s: key extraction is not supported for %s files", path, ext)
	}
}

// extractTree navigates a dotted key path through a YAML/JSON document.
func extractTree(path string, clear []byte, key string) (string, error) {
	var doc any
	if err := yaml.Unmarshal(clear, &doc); err != nil {
		return "", fmt.Errorf("sops file %s is not valid YAML/JSON: %w", path, err)
	}
	node := doc
	for _, part := range strings.Split(key, ".") {
		m, ok := node.(map[string]any)
		if !ok {
			return "", aws.NotFound("key %s not found in sops file %s", key, path)
		}
		node, ok = m[part]
		if !ok {
			return "", aws.NotFound("key %s not found in sops file %s", key, path)
		}
	}
	return scalar(node)
}

// scalar renders a leaf value as a string. Nested structures (maps/slices) are
// re-emitted as YAML so a subtree can still be pulled out as a block.
func scalar(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		out, err := yaml.Marshal(v)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(out), "\n"), nil
	}
}

// extractDotenv returns the value of key from a decrypted dotenv document.
func extractDotenv(path string, clear []byte, key string) (string, error) {
	for _, line := range strings.Split(string(clear), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) == key {
			return strings.Trim(strings.TrimSpace(val), `"'`), nil
		}
	}
	return "", aws.NotFound("key %s not found in sops file %s", key, path)
}
