// Command gen-types derives the JSON Schema and the TypeScript declaration for
// the agg/v1 artifacts from the Go structs in internal/aggmodel. The structs are
// the single source of truth: anything hand-written on a consumer's side would
// drift the moment a field is added, so the declarations are generated from the
// same types the aggregator marshals. No consumer in this repository reads them
// since the presentation tier was deleted on 2026-09-18
// (docs/decisions/ADR-011-retire-the-web-tier.md); they are published as part of
// the artifact contract.
//
// Usage:
//
//	go run ./cmd/gen-types -out schema
//	go run ./cmd/gen-types -check -out schema
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

const (
	schemaFile = "agg.schema.json"
	typesFile  = "agg.d.ts"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gen-types:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gen-types", flag.ContinueOnError)
	out := fs.String("out", "schema", "directory to write the generated declaration and schema into")
	check := fs.Bool("check", false, "do not write; exit non-zero if the generated files are stale")
	if err := fs.Parse(args); err != nil {
		return err
	}

	schema, err := aggmodel.MarshalSchema()
	if err != nil {
		return err
	}
	outputs := []output{
		{name: schemaFile, data: schema},
		{name: typesFile, data: []byte(aggmodel.TypeScript())},
	}

	stale := make([]string, 0, len(outputs))
	for _, o := range outputs {
		path := filepath.Join(*out, o.name)
		if *check {
			if err := compare(path, o.data); err != nil {
				stale = append(stale, err.Error())
			}
			continue
		}
		// 0o750/0o600 are the linter's defaults for anything written from a
		// variable path. The relaxation to 0o755/0o644 is deliberate: these
		// are generated, committed build artifacts with nothing secret in
		// them, and the Node toolchain has to be able to read them.
		if err := os.MkdirAll(*out, 0o755); err != nil { //nolint:gosec // G301: see above
			return err
		}
		if err := os.WriteFile(path, o.data, 0o644); err != nil { //nolint:gosec // G306: see above
			return err
		}
		fmt.Printf("wrote %s (%d bytes)\n", path, len(o.data))
	}

	if len(stale) > 0 {
		return fmt.Errorf("generated files are stale, run `make types`:\n\t%s", joinLines(stale))
	}
	return nil
}

type output struct {
	name string
	data []byte
}

func compare(path string, want []byte) error {
	// path is built from the -out flag and a constant file name, never from
	// external input; this command is not exposed to anything untrusted.
	got, err := os.ReadFile(path) //nolint:gosec // G304: see above
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s is missing", path)
	case err != nil:
		return err
	case !bytes.Equal(got, want):
		return fmt.Errorf("%s differs from the Go types", path)
	}
	return nil
}

func joinLines(lines []string) string {
	var b bytes.Buffer
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n\t")
		}
		// bytes.Buffer's WriteString error is always nil, so there is nothing
		// to report and nothing to unwrap.
		_, _ = b.WriteString(l)
	}
	return b.String()
}
