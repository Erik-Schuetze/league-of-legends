package aggregate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Raw archive layout, frozen by docs/contracts.md section 4:
//
//	<raw root>/riot/match-v5/dt=<YYYY-MM-DD>/part-00001.parquet.zst
//	<raw root>/riot/match-v5-timeline/dt=<YYYY-MM-DD>/part-00001.parquet.zst
//
// Two sources live side by side under one root, and which one a walk reads is
// the caller's choice rather than the layout's: the nightly tier-list build
// reads match-v5 alone (a bad timeline extract must not be able to fail it),
// and the feature dataset build reads both. RawArchive.Source is that choice.
// The layout is otherwise identical, so the staging, magic-sniffing and
// partition-pruning code below is shared verbatim.

const (
	// rawSourceDir is the match summary source, and the default a zero Source
	// resolves to.
	rawSourceDir = "match-v5"
	// RawSourceTimeline is the match timeline source, whose rows are the
	// timeline payloads for the same matches. See
	// docs/decisions/ADR-012-ingest-match-timelines.md.
	RawSourceTimeline = "match-v5-timeline"
	rawPartitionPr    = "dt="
)

// ArchivePart is one parquet part inside one date partition of the archive.
type ArchivePart struct {
	// PartitionDate is the dt= value, i.e. the date the crawler fetched the
	// match. It is a fallback timestamp only: see ExtractOptions.
	PartitionDate string
	// Path is the on-disk path as found.
	Path string
}

// RawArchive is a read-only view of one source of the immutable raw archive.
type RawArchive struct {
	// Root is the raw root from configuration, e.g. ./data/raw.
	Root string
	// Before prunes partitions that cannot contribute to the window. When the
	// zero value, nothing is pruned.
	Before string
	// Source names the payload directory to walk. Empty means the match
	// summary source, which is what every caller written before timelines
	// existed means, so the zero value is the old behaviour.
	Source string
}

var (
	// ErrArchiveEmpty reports an archive with no match-v5 parts at all.
	ErrArchiveEmpty = errors.New("raw archive contains no match-v5 parquet parts")
	// ErrNoTimelineArchive reports that the timeline archive holds nothing to
	// build a dataset from. It is separate from ErrArchiveEmpty because the
	// two mean opposite things to an operator: an empty summary archive is a
	// crawler that has not run, and an empty timeline archive is a backfill
	// that has not run - and the dataset build is allowed to fail on the
	// second while the tier list is not.
	ErrNoTimelineArchive = errors.New("raw archive contains no timeline parquet parts")
)

// sourceDir is the payload directory this view walks.
func (a RawArchive) sourceDir() string {
	if a.Source == "" {
		return rawSourceDir
	}
	return a.Source
}

// emptyErr is the sentinel an empty walk of this source reports.
func (a RawArchive) emptyErr() error {
	if a.Source == RawSourceTimeline {
		return ErrNoTimelineArchive
	}
	return ErrArchiveEmpty
}

// Parts walks the archive and returns the parts that may contribute to the
// build window.
//
// Pruning is by partition directory name only and is exact: a dt= partition
// holds matches crawled on that date, and a match is never crawled before it
// was played, so a partition strictly older than the window start cannot hold a
// match inside the window. Partitions *newer* than the window are deliberately
// kept: the crawler may backfill, and the SQL filters on the per-match
// timestamp anyway.
func (a RawArchive) Parts() ([]ArchivePart, error) {
	base := filepath.Join(a.Root, "riot", a.sourceDir())
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("read raw archive %s: %w", base, err)
	}
	var parts []ArchivePart
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), rawPartitionPr) {
			continue
		}
		date := strings.TrimPrefix(entry.Name(), rawPartitionPr)
		if _, err := parseDate(date); err != nil {
			return nil, fmt.Errorf("raw partition %q: %w", entry.Name(), err)
		}
		if a.Before != "" && date < a.Before {
			continue
		}
		dir := filepath.Join(base, entry.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read raw partition %s: %w", dir, err)
		}
		for _, file := range files {
			if file.IsDir() || !isParquetPart(file.Name()) {
				continue
			}
			parts = append(parts, ArchivePart{PartitionDate: date, Path: filepath.Join(dir, file.Name())})
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w under %s", a.emptyErr(), base)
	}
	// Deterministic order: the engine's file list must not depend on readdir.
	sort.Slice(parts, func(i, j int) bool { return parts[i].Path < parts[j].Path })
	return parts, nil
}

func isParquetPart(name string) bool {
	return strings.HasSuffix(name, ".parquet") || strings.HasSuffix(name, ".parquet.zst")
}

// StageParts makes every part readable as plain parquet and returns the paths
// to hand to DuckDB, in the same order.
//
// Two archives are possible in the wild and both are handled:
//
//   - the collector compressed the parquet bytes into a zstd *frame*, in which
//     case the file starts with the zstd magic and must be decompressed first;
//   - the parquet file merely uses the zstd *codec* internally (DuckDB's own
//     default), in which case the file starts with the parquet magic and
//     DuckDB reads it as-is.
//
// The magic bytes are sniffed rather than the file name trusted, so both
// namings and both encodings work. Decompressed parts are written into scratch,
// which the caller owns.
func (a RawArchive) StageParts(ctx context.Context, parts []ArchivePart, scratch string) ([]string, error) {
	// The scratch directory holds decompressed archive bytes that only this
	// process and the engine it spawns read, and the engine is a child of this
	// process, so it needs no group or other access. See perms.go.
	if err := os.MkdirAll(scratch, privateDirPerm); err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	paths := make([]string, 0, len(parts))
	for i, part := range parts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		magic, err := readMagic(part.Path)
		if err != nil {
			return nil, err
		}
		switch {
		case bytes.Equal(magic, zstdMagic):
			target := filepath.Join(scratch, fmt.Sprintf("part-%05d.parquet", i))
			if err := decompressZstdFrame(part.Path, target); err != nil {
				return nil, err
			}
			paths = append(paths, target)
		case bytes.Equal(magic, parquetMagic):
			paths = append(paths, part.Path)
		default:
			return nil, fmt.Errorf("raw part %s: unrecognised file magic %q: expected zstd frame or parquet", part.Path, string(magic))
		}
	}
	return paths, nil
}

var (
	zstdMagic    = []byte{0x28, 0xb5, 0x2f, 0xfd}
	parquetMagic = []byte("PAR1")
)

func readMagic(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // path comes from our own archive walk.
	if err != nil {
		return nil, fmt.Errorf("open raw part %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	buf := make([]byte, len(parquetMagic))
	if _, err := io.ReadFull(file, buf); err != nil {
		return nil, fmt.Errorf("read magic of raw part %s: %w", path, err)
	}
	return buf, nil
}

func decompressZstdFrame(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // path comes from our own archive walk.
	if err != nil {
		return fmt.Errorf("open zstd part %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // destination is a scratch path we chose.
	if err != nil {
		return fmt.Errorf("create decompressed part %s: %w", dst, err)
	}
	decoder, err := zstd.NewReader(in)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("open zstd reader for %s: %w", src, err)
	}
	defer decoder.Close()
	if _, err := io.Copy(out, decoder); err != nil {
		_ = out.Close()
		return fmt.Errorf("decompress %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close decompressed part %s: %w", dst, err)
	}
	return nil
}
