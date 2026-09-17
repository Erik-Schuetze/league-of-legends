package aggregate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// JSON in and out.
//
// Byte stability matters here beyond tidiness: the demo determinism test
// compares two runs byte for byte, a reviewer reads the diff between two
// builds of the same archive, and a repeated build of an unchanged archive
// should publish an identical artifact so that a no-op run does not push
// anything. Every document therefore goes through one encoder with a fixed
// indent, HTML escaping off (champion slugs contain neither, but a stray
// apostrophe must not become an entity) and a trailing newline.

// readJSONArray decodes a JSON array file written by DuckDB.
func readJSONArray[T any](path string) ([]T, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is inside the build scratch directory.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("read %s: file is empty", filepath.Base(path))
	}
	var out []T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return out, nil
}

// readJSONDoc decodes a single JSON document written by this package.
//
// The raw read error is returned unwrapped and unlabelled so that callers can
// still test it with errors.Is(err, fs.ErrNotExist): "the manifest does not
// exist yet" and "the manifest is corrupt" need different handling.
func readJSONDoc[T any](path string) (T, error) {
	var out T
	raw, err := os.ReadFile(path) //nolint:gosec // path is inside the aggregate tree or build scratch directory.
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return out, nil
}

// marshalDoc encodes a document the one way this project encodes documents.
func marshalDoc(doc any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("marshal document: %w", err)
	}
	return buf.Bytes(), nil
}

// writeDoc writes a document under root, creating parent directories.
func writeDoc(root, relPath string, doc any) error {
	buf, err := marshalDoc(doc)
	if err != nil {
		return err
	}
	target := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(target), publishedDirPerm); err != nil { //nolint:gosec // G301: read by the site-build job as uid 1000 on an NFS volume where fsGroup is not honoured; see perms.go.
		return fmt.Errorf("create directory for %s: %w", relPath, err)
	}
	if err := os.WriteFile(target, buf, publishedFilePerm); err != nil { //nolint:gosec // G306: read by the site-build job as uid 1000 on an NFS volume where fsGroup is not honoured; see perms.go.
		return fmt.Errorf("write %s: %w", relPath, err)
	}
	return nil
}
