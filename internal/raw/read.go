package raw

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// PartPaths lists the published parts of a partition directory in write order.
// Temporary files are skipped by construction: they do not carry the part
// suffix.
func PartPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("raw: list partition %s: %w", dir, err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, partPrefix) || !strings.HasSuffix(name, partSuffix) {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

// ReadMatches reads rows back from part files. It exists for the tests that
// prove the archive is readable and for operators debugging a partition; the
// aggregation reads the archive through DuckDB.
func ReadMatches(paths ...string) ([]MatchRow, error) {
	var rows []MatchRow
	for _, path := range paths {
		part, err := parquet.ReadFile[MatchRow](path)
		if err != nil {
			return nil, fmt.Errorf("raw: read %s: %w", path, err)
		}
		rows = append(rows, part...)
	}
	return rows, nil
}

// ReadLeaguePages reads back LEAGUE-V4 pages.
func ReadLeaguePages(paths ...string) ([]LeagueRow, error) {
	var rows []LeagueRow
	for _, path := range paths {
		part, err := parquet.ReadFile[LeagueRow](path)
		if err != nil {
			return nil, fmt.Errorf("raw: read %s: %w", path, err)
		}
		rows = append(rows, part...)
	}
	return rows, nil
}

// ReadStatic reads back Data Dragon documents.
func ReadStatic(paths ...string) ([]StaticRow, error) {
	var rows []StaticRow
	for _, path := range paths {
		part, err := parquet.ReadFile[StaticRow](path)
		if err != nil {
			return nil, fmt.Errorf("raw: read %s: %w", path, err)
		}
		rows = append(rows, part...)
	}
	return rows, nil
}

// ReadMatchPartition reads every published part of one partition directory.
func ReadMatchPartition(dir string) ([]MatchRow, error) {
	paths, err := PartPaths(dir)
	if err != nil {
		return nil, err
	}
	return ReadMatches(paths...)
}
