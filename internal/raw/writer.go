package raw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	kzstd "github.com/klauspost/compress/zstd"
	"github.com/parquet-go/parquet-go"
	parquetzstd "github.com/parquet-go/parquet-go/compress/zstd"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

const (
	partPrefix  = "part-"
	partSuffix  = ".parquet.zst"
	partTmp     = ".tmp"
	partDirPerm = 0o750

	// partTmpStaleAfter is how old an abandoned temporary part must be before
	// a later writer deletes it.
	//
	// A `.tmp` is only ever renamed into place by the finalize that publishes
	// it, so one that is still lying there was never committed and deleting it
	// costs no archived data. Deleting a *live* one would cost the rows its
	// writer is still holding in memory, so the threshold sits far above the
	// longest a part can legitimately stay open: a worker batch is bounded by
	// JobBatch*JobTimeout (twenty jobs of twenty-five seconds today) and the
	// archive has exactly one writer per partition.
	partTmpStaleAfter = time.Hour

	// maxPartIndexSkips bounds the indices one open steps over when it finds
	// them already taken. Exceeding it means the directory holds something a
	// human should look at, and saying so beats looping.
	maxPartIndexSkips = 64
)

// DefaultRowsPerPart is the rotation threshold. Twenty thousand rows of match
// summaries is roughly forty megabytes before compression: big enough that a
// day of crawling is a handful of files, small enough that a part is cheap to
// rewrite after a crash.
const DefaultRowsPerPart = 20000

// ErrClosed is returned by writes after Close. The crawler treats it as fatal:
// a closed archive means the run is shutting down.
var ErrClosed = errors.New("raw: archive writer is closed")

// Options configures an archive writer.
type Options struct {
	// Root is the archive root, e.g. "var/raw". Partitions are created below
	// it; nothing else is read.
	Root string
	// RowsPerPart rotates the open part once it holds this many rows. Zero
	// means DefaultRowsPerPart.
	RowsPerPart int
	// CompressionLevel is the zstd level. Zero means the library default,
	// which is level 3 - the point where Parquet's two other costs (row
	// group encoding, dictionary pages) still dominate the CPU budget.
	CompressionLevel int
	// Metrics receives the compressed byte count. Optional.
	Metrics obs.MetricsRecorder
	// Now is the clock the stale-part reaper reads. Nil means the real clock.
	// It is injectable so that a test can age an abandoned part without
	// sleeping for partTmpStaleAfter.
	Now func() time.Time
}

// Writer appends payloads to the archive.
//
// It keeps one open part per partition directory and buffers rows in memory
// until a row group is full, so a caller that writes ten rows and never
// flushes has written nothing. The worker flushes after every batch and on
// shutdown for exactly that reason.
type Writer struct {
	opts    Options
	metrics obs.MetricsRecorder

	mu           sync.Mutex
	matchParts   map[string]*partWriter[MatchRow]
	leagueParts  map[string]*partWriter[LeagueRow]
	staticParts  map[string]*partWriter[StaticRow]
	accountParts map[string]*partWriter[AccountRow]
	closed       bool
}

// Writer implements the frozen archive interface.
var _ contract.RawWriter = (*Writer)(nil)

// New returns a writer over root. It creates no directories: a run that never
// fetches a match should not leave an empty partition behind.
func New(opts Options) (*Writer, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, errors.New("raw: archive root is required")
	}
	if opts.RowsPerPart == 0 {
		opts.RowsPerPart = DefaultRowsPerPart
	}
	if opts.RowsPerPart < 0 {
		return nil, fmt.Errorf("raw: rows per part must not be negative: %d", opts.RowsPerPart)
	}
	if opts.CompressionLevel < 0 {
		return nil, fmt.Errorf("raw: compression level must not be negative: %d", opts.CompressionLevel)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Writer{
		opts:         opts,
		metrics:      opts.Metrics,
		matchParts:   map[string]*partWriter[MatchRow]{},
		leagueParts:  map[string]*partWriter[LeagueRow]{},
		staticParts:  map[string]*partWriter[StaticRow]{},
		accountParts: map[string]*partWriter[AccountRow]{},
	}, nil
}

// WriteMatch appends one fetched match to the match partition of its fetch
// date. The payload is the exact response body when the client retained it and
// a re-encoding of the typed fields otherwise.
func (w *Writer) WriteMatch(ctx context.Context, match riot.MatchDTO, meta contract.MatchMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if meta.FetchedAt.IsZero() {
		return fmt.Errorf("raw: match %s has no fetch time", meta.MatchID)
	}
	row := matchRow(match, meta)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	dir := MatchDir(w.opts.Root, meta.PartitionDate())
	part, ok := w.matchParts[dir]
	if !ok {
		part = newPartWriter[MatchRow](dir, w.opts, w.metrics)
		w.matchParts[dir] = part
	}
	return part.append(row)
}

// WriteLeagueEntries appends one ladder page. The page is stored as a single
// row because seeding consumes a whole page at a time and the page's exact
// shape is what makes a thin frontier traceable to the request that caused it.
func (w *Writer) WriteLeagueEntries(ctx context.Context, entries []riot.LeagueEntryDTO, meta contract.LeagueMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if meta.FetchedAt.IsZero() {
		return errors.New("raw: league page has no fetch time")
	}
	payload, err := riot.LeagueEntriesPayload(entries)
	if err != nil {
		return fmt.Errorf("raw: encode league page: %w", err)
	}
	row := LeagueRow{
		Region:         strings.ToUpper(meta.Region),
		QueueType:      meta.QueueType,
		Tier:           meta.Tier,
		Division:       meta.Division,
		Entries:        narrowInt32(int64(len(entries))),
		PayloadVersion: LeaguePayloadVersion,
		FetchedAt:      meta.FetchedAt.UTC(),
		Payload:        string(payload),
		PayloadSHA256:  sha256Hex(payload),
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	dir := LeagueDir(w.opts.Root, meta.FetchedAt.UTC().Format(time.DateOnly), meta.Region)
	part, ok := w.leagueParts[dir]
	if !ok {
		part = newPartWriter[LeagueRow](dir, w.opts, w.metrics)
		w.leagueParts[dir] = part
	}
	return part.append(row)
}

// WriteStatic appends one Data Dragon document. kind is the document family
// ("champions", "items", "runes", "summoner-spells", "versions"), which is a
// partition column because a reader always wants exactly one of them.
func (w *Writer) WriteStatic(ctx context.Context, kind, version, locale string, payload []byte, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if kind == "" {
		return errors.New("raw: static document has no kind")
	}
	if at.IsZero() {
		return errors.New("raw: static document has no fetch time")
	}
	row := StaticRow{
		Kind:           kind,
		Version:        version,
		Locale:         locale,
		PayloadVersion: StaticPayloadVersion,
		FetchedAt:      at.UTC(),
		Payload:        string(payload),
		PayloadSHA256:  sha256Hex(payload),
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	dir := StaticDir(w.opts.Root, at.UTC().Format(time.DateOnly), kind)
	part, ok := w.staticParts[dir]
	if !ok {
		part = newPartWriter[StaticRow](dir, w.opts, w.metrics)
		w.staticParts[dir] = part
	}
	return part.append(row)
}

// WriteAccount appends one ACCOUNT-V1 lookup. It is a concrete extra rather than
// part of the frozen RawWriter surface, because identity lookups are an
// operator-initiated action and not a step of the crawl loop.
func (w *Writer) WriteAccount(ctx context.Context, account riot.AccountDTO, payload []byte, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if at.IsZero() {
		return errors.New("raw: account lookup has no fetch time")
	}
	row := AccountRow{
		PUUID:          account.PUUID,
		GameName:       account.GameName,
		TagLine:        account.TagLine,
		PayloadVersion: AccountPayloadVersion,
		FetchedAt:      at.UTC(),
		Payload:        string(payload),
		PayloadSHA256:  sha256Hex(payload),
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	dir := AccountDir(w.opts.Root, at.UTC().Format(time.DateOnly))
	part, ok := w.accountParts[dir]
	if !ok {
		part = newPartWriter[AccountRow](dir, w.opts, w.metrics)
		w.accountParts[dir] = part
	}
	return part.append(row)
}

// Flush finalises every open part. Parts stay open afterwards - the worker
// flushes between batches without paying to reopen a partition it is still
// filling.
func (w *Writer) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finalizeAll()
}

// Root is the archive root this writer was opened with. It is exposed because
// the control plane records a raw_uri per match: the uri has to be derived from
// the writer that actually holds the payload, not from a second copy of the
// configuration that could disagree with it.
func (w *Writer) Root() string { return w.opts.Root }

// Close flushes and refuses further writes.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return w.finalizeAll()
}

// finalizeAll is called with the lock held.
func (w *Writer) finalizeAll() error {
	var errs []error
	for _, part := range w.matchParts {
		errs = append(errs, part.finalize())
	}
	for _, part := range w.leagueParts {
		errs = append(errs, part.finalize())
	}
	for _, part := range w.staticParts {
		errs = append(errs, part.finalize())
	}
	for _, part := range w.accountParts {
		errs = append(errs, part.finalize())
	}
	return errors.Join(errs...)
}

// partWriter is one open Parquet part file.
//
// It is generic over the row type because Parquet's writer is: the two APIs
// share the rotation, atomic-publish and accounting logic and nothing else.
type partWriter[T any] struct {
	dir     string
	opts    Options
	metrics obs.MetricsRecorder

	file      *os.File
	enc       *parquet.GenericWriter[T]
	tmpPath   string
	finalPath string
	rows      int
}

func newPartWriter[T any](dir string, opts Options, metrics obs.MetricsRecorder) *partWriter[T] {
	return &partWriter[T]{dir: dir, opts: opts, metrics: metrics}
}

// append writes one row, opening or rotating the part as needed.
func (p *partWriter[T]) append(row T) error {
	if p.enc == nil {
		if err := p.open(); err != nil {
			return err
		}
	}
	if _, err := p.enc.Write([]T{row}); err != nil {
		return fmt.Errorf("raw: write %s: %w", p.tmpPath, err)
	}
	p.rows++
	if p.opts.RowsPerPart > 0 && p.rows >= p.opts.RowsPerPart {
		return p.finalize()
	}
	return nil
}

// open creates the next free part path in the directory. The index is derived
// from the directory listing rather than from a counter, so a restarted
// process appends to yesterday's sequence instead of overwriting it.
//
// A writer that dies between its first write to a part and the flush that
// publishes it leaves `part-NNNNN.parquet.zst.tmp` behind, and that file must
// not be able to stop the partition forever: the listing steps over indices
// that are already taken, and a temporary part old enough to be abandoned is
// reaped first.
func (p *partWriter[T]) open() error {
	if err := os.MkdirAll(p.dir, partDirPerm); err != nil {
		return fmt.Errorf("raw: create partition %s: %w", p.dir, err)
	}
	index, stale, err := scanPartDir(p.dir, p.opts.Now())
	if err != nil {
		return err
	}
	p.reap(stale)
	return p.create(index)
}

// reap removes abandoned temporary parts. It is best-effort housekeeping, not
// recovery - create steps over a taken index on its own - which is why a
// failure to remove one is not worth failing a write for.
func (p *partWriter[T]) reap(stale []string) {
	for _, path := range stale {
		_ = os.Remove(path)
	}
}

// create opens the first free temporary part at or after index. The create is
// exclusive, so an index that is already taken is stepped over rather than
// disturbed: under the archive's one-writer-per-partition model the only thing
// that can be holding it is a writer that died, and on NFS a same-name create
// over a live writer's part would be worse than a skipped index.
func (p *partWriter[T]) create(index int) error {
	for skip := 0; skip < maxPartIndexSkips; skip++ {
		p.finalPath = filepath.Join(p.dir, fmt.Sprintf("%s%05d%s", partPrefix, index, partSuffix))
		p.tmpPath = p.finalPath + partTmp
		file, err := os.OpenFile(p.tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, partDirPerm)
		if err == nil {
			p.file = file
			p.enc = parquet.NewGenericWriter[T](file, parquet.Compression(compressionCodec(p.opts.CompressionLevel)))
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("raw: create part %s: %w", p.tmpPath, err)
		}
		index++
	}
	return fmt.Errorf("raw: create part in %s: the first %d part indices are already taken", p.dir, maxPartIndexSkips)
}

// finalize writes the footer, syncs, and renames the part into place. On any
// failure the temporary file is removed so the next attempt starts clean: a
// part that cannot be published must not be left where a reader would find it.
func (p *partWriter[T]) finalize() error {
	if p.enc == nil {
		return nil
	}
	err := p.enc.Close()
	if closeErr := p.file.Sync(); err == nil {
		err = closeErr
	}
	if closeErr := p.file.Close(); err == nil {
		err = closeErr
	}
	p.enc, p.file = nil, nil
	if err != nil {
		_ = os.Remove(p.tmpPath)
		return fmt.Errorf("raw: finalise %s: %w", p.tmpPath, err)
	}
	if err := os.Rename(p.tmpPath, p.finalPath); err != nil {
		_ = os.Remove(p.tmpPath)
		return fmt.Errorf("raw: publish %s: %w", p.finalPath, err)
	}
	p.rows = 0
	if p.metrics == nil {
		return nil
	}
	if info, err := os.Stat(p.finalPath); err == nil {
		p.metrics.AddRawBytesWritten(info.Size())
	}
	return nil
}

// scanPartDir lists dir once: it returns the index a new part should use and
// the temporary parts old enough to have been abandoned.
//
// finalize publishes with a rename, so a `.tmp` sibling is never a committed
// part and never raises the index - a stale one is stepped over by create and
// reaped once it is older than partTmpStaleAfter. Two processes writing one
// partition is out of scope - the plan gives the archive exactly one writer -
// which is what makes "older than an hour" a safe definition of abandoned.
func scanPartDir(dir string, now time.Time) (int, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("raw: list partition %s: %w", dir, err)
	}
	cutoff := now.Add(-partTmpStaleAfter)
	highest := 0
	var stale []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, partPrefix) {
			continue
		}
		switch {
		case strings.HasSuffix(name, partSuffix):
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, partPrefix), partSuffix))
			if err != nil || n <= highest {
				continue
			}
			highest = n
		case strings.HasSuffix(name, partSuffix+partTmp):
			info, err := entry.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			stale = append(stale, filepath.Join(dir, name))
		}
	}
	return highest + 1, stale, nil
}

func compressionCodec(level int) *parquetzstd.Codec {
	encoderLevel := parquetzstd.SpeedDefault
	if level > 0 {
		encoderLevel = kzstd.EncoderLevelFromZstd(level)
	}
	return &parquetzstd.Codec{Level: encoderLevel}
}

// matchRow maps the frozen metadata onto the archive schema. The match id
// comes from the queue row rather than from the payload: the queue row is what
// the idempotency key is, so a payload that disagrees with it is a data bug
// this row should not hide.
func matchRow(match riot.MatchDTO, meta contract.MatchMeta) MatchRow {
	matchID := meta.MatchID
	if matchID == "" {
		matchID = match.Metadata.MatchID
	}
	queueID := meta.QueueID
	if queueID == 0 {
		queueID = match.Info.QueueID
	}
	gameVersion := meta.GameVersion
	if gameVersion == "" {
		gameVersion = match.Info.GameVersion
	}
	gameCreation := meta.GameCreation
	if gameCreation.IsZero() {
		gameCreation = time.UnixMilli(match.Info.GameCreation).UTC()
	}
	duration := meta.GameDurationS
	if duration == 0 {
		duration = int(match.Info.GameDuration)
	}
	payloadVersion := meta.PayloadVersion
	if payloadVersion == "" {
		payloadVersion = MatchPayloadVersion
	}
	payload := match.RawPayload()
	return MatchRow{
		MatchID:        matchID,
		Region:         strings.ToUpper(meta.Region),
		QueueID:        narrowInt32(int64(queueID)),
		Patch:          meta.Patch,
		GameVersion:    gameVersion,
		GameCreationMS: gameCreation.UnixMilli(),
		GameDurationS:  narrowInt32(int64(duration)),
		PayloadVersion: payloadVersion,
		FetchedAt:      meta.FetchedAt.UTC(),
		Payload:        string(payload),
		PayloadSHA256:  sha256Hex(payload),
	}
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// narrowInt32 converts a JSON number to the column type. Riot's queue ids and
// game durations are far below the int32 ceiling, so a value that does not fit
// means the payload is nonsense; clamping keeps the row and its payload rather
// than discarding either.
func narrowInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}
