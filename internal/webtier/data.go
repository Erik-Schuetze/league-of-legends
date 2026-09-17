package webtier

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The Data Dragon projections the repository checks in under web/src/data/.
//
// They are read from disk when the repository is present (the local and CI
// layout) and from this embedded copy otherwise, which is what makes the
// deployed binary self-contained: a container has no checkout, and a build row
// whose item names were silently missing would be a worse outcome than a larger
// binary. The copies are byte-identical to web/src/data/*.json and
// TestEmbeddedProjectionsMatchTheRepository fails when they drift.
//
//go:embed data
var checkedInFS embed.FS

const (
	itemsDataIn  = "items.json"
	runesDataIn  = "runes.json"
	spellsDataIn = "spells.json"
)

// regenerateHint is build-lookup.ts's REGENERATE. The projection is generated,
// so the error text names the command that regenerates it.
const regenerateHint = `Run "npm run data:champions" to regenerate it from Data Dragon.`

// checkedInData reads one checked-in projection and reports where it came from,
// so an error can name the file that is wrong rather than a directory.
func (l *Loader) checkedInData(name string) ([]byte, string, error) {
	if dir := l.opts.DataDir; dir != "" {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path) // #nosec G304 -- dir is the operator's data directory, name is a fixed checked-in file name
		if err == nil {
			return raw, path, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, path, artifactFault(path, "cannot be read: %v", err)
		}
	}
	raw, err := fs.ReadFile(checkedInFS, "data/"+name)
	if err != nil {
		return nil, name, artifactFault(name, "is missing: no %s directory holds it and no embedded copy exists: %v",
			EnvDataDir, err)
	}
	return raw, "embedded:" + name, nil
}

// dataFault is artifactFault with the checked-in projections' remedy appended:
// the message has to say both what is wrong and how to put it right.
func dataFault(path string, format string, args ...any) error {
	return artifactFault(path, "%s %s", fmt.Sprintf(format, args...), regenerateHint)
}
