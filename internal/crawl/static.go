package crawl

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/Erik-Schuetze/league-of-legends/internal/contract"
	"github.com/Erik-Schuetze/league-of-legends/internal/obs"
	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// Data Dragon is public, unauthenticated and versioned by patch. The crawler
// mirrors it into the same immutable archive as the Riot payloads, for the same
// reason: the join keys that make a match row readable (champion ids, item ids,
// rune ids) are mutable upstream and would otherwise be lost.
const (
	// DefaultDDragonURL is the Data Dragon origin. It is a constant with an
	// override because the static-sync test points it at a fake server.
	DefaultDDragonURL = "https://ddragon.leagueoflegends.com"
	// DefaultDDragonLocale is the locale the published aggregates are named
	// in. The site is English in v1.
	DefaultDDragonLocale = "en_US"

	KindVersions       = "versions"
	KindChampions      = "champions"
	KindItems          = "items"
	KindRunes          = "runes"
	KindSummonerSpells = "summoner-spells"

	// DefaultStaticAttempts is the retry budget for one document. Static data
	// is available from a CDN that mostly does not fail; a handful of
	// attempts covers a cache miss or a blip without turning a cron run into
	// a hammering loop.
	DefaultStaticAttempts = 3
	// defaultStaticTimeout bounds one document. champion.json is around a
	// megabyte; twenty seconds is generous and still finite.
	defaultStaticTimeout = 20 * time.Second
	// maxStaticBytes caps a document so a misrouted URL cannot fill the disk.
	maxStaticBytes = 64 << 20
	// staticBackoffBase is the delay before the second attempt.
	staticBackoffBase = 250 * time.Millisecond
)

// StaticKinds is the document set v1 retains, in fetch order. The order is not
// alphabetical because versions.json has to be read first: it is what names the
// patch every other document belongs to.
func StaticKinds() []string {
	return []string{KindVersions, KindChampions, KindItems, KindRunes, KindSummonerSpells}
}

// Doer is the part of *http.Client the static sync uses.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// StaticWriter retains one Data Dragon document. The concrete *raw.Writer
// implements it; it is separate from contract.RawWriter because the contract
// describes crawl payloads.
type StaticWriter interface {
	WriteStatic(ctx context.Context, kind, version, locale string, payload []byte, at time.Time) error
}

// ArchiveWriter is everything the seeding, static and backfill commands need
// from the archive: the frozen RawWriter plus static retention.
type ArchiveWriter interface {
	contract.RawWriter
	StaticWriter
}

// StaticOptions is one Data Dragon mirror run.
type StaticOptions struct {
	// Writer is the archive. It is the concrete shape of *raw.Writer widened
	// by StaticWriter: the frozen RawWriter covers crawl payloads only, and
	// static documents are not crawl payloads, so the extra method lives in a
	// local interface rather than in the contract.
	Writer  ArchiveWriter
	Log     *slog.Logger
	Metrics obs.MetricsRecorder
	Clock   riot.Clock
	Now     func() time.Time

	BaseURL string
	// Version pins the patch. Empty means "read versions.json and take the
	// newest", which is what the scheduled run wants.
	Version string
	Locale  string
	// Kinds defaults to StaticKinds(). versions.json is always fetched, even
	// when the caller trims the list, because the patch label is recorded on
	// every document.
	Kinds      []string
	HTTPClient Doer
	Attempts   int
	Timeout    time.Duration
}

// StaticDocument is one retained document.
type StaticDocument struct {
	Kind    string
	URL     string
	Version string
	Locale  string
	Bytes   int
}

// StaticResult is the run summary.
type StaticResult struct {
	Version   string
	Locale    string
	Documents []StaticDocument
}

// StaticSync mirrors the Data Dragon documents for one patch into the archive.
//
// It does not touch the control plane. Static data is a join input for the
// build, not crawl work: nothing in matches or fetch_queue refers to it, and
// the archive partition is the only durable record needed.
func StaticSync(ctx context.Context, opts StaticOptions) (StaticResult, error) {
	opts.normalizeStatic()
	result := StaticResult{Locale: opts.Locale}

	if opts.Writer == nil {
		return result, errors.New("crawl: static sync needs a raw writer")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: opts.Timeout}
	}

	version := opts.Version
	if version == "" {
		payload, err := opts.fetch(ctx, httpClient, "/api/versions.json")
		if err != nil {
			return result, fmt.Errorf("ddragon versions: %w", err)
		}
		version, err = latestVersion(payload)
		if err != nil {
			return result, err
		}
		if err := opts.store(ctx, KindVersions, version, payload); err != nil {
			return result, err
		}
		result.Documents = append(result.Documents, StaticDocument{
			Kind: KindVersions, URL: opts.BaseURL + "/api/versions.json", Version: version,
			Locale: opts.Locale, Bytes: len(payload),
		})
	} else {
		// A pinned version still retains versions.json: an operator who pinned
		// a patch wants the list that patch came from, and it is one request.
		payload, err := opts.fetch(ctx, httpClient, "/api/versions.json")
		if err != nil {
			return result, fmt.Errorf("ddragon versions: %w", err)
		}
		if err := opts.store(ctx, KindVersions, version, payload); err != nil {
			return result, err
		}
		result.Documents = append(result.Documents, StaticDocument{
			Kind: KindVersions, URL: opts.BaseURL + "/api/versions.json", Version: version,
			Locale: opts.Locale, Bytes: len(payload),
		})
	}
	result.Version = version

	for _, kind := range opts.Kinds {
		if kind == KindVersions {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		docPath, ok := staticDocumentPath(kind, version, opts.Locale)
		if !ok {
			return result, fmt.Errorf("ddragon: unknown document kind %q", kind)
		}
		payload, err := opts.fetch(ctx, httpClient, docPath)
		if err != nil {
			return result, fmt.Errorf("ddragon %s: %w", kind, err)
		}
		if err := opts.store(ctx, kind, version, payload); err != nil {
			return result, err
		}
		result.Documents = append(result.Documents, StaticDocument{
			Kind: kind, URL: opts.BaseURL + docPath, Version: version,
			Locale: opts.Locale, Bytes: len(payload),
		})
		opts.Log.Debug("static document retained", "kind", kind, "bytes", len(payload), "version", version)
	}
	if err := opts.Writer.Flush(ctx); err != nil {
		return result, fmt.Errorf("flush archive: %w", err)
	}
	opts.Log.Info("static sync finished",
		"version", version, "locale", opts.Locale, "documents", len(result.Documents))
	return result, nil
}

func (o *StaticOptions) normalizeStatic() {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Metrics == nil {
		o.Metrics = obs.NopRecorder{}
	}
	if o.Clock == nil {
		o.Clock = riot.RealClock{}
	}
	if o.Now == nil {
		o.Now = o.Clock.Now
	}
	if strings.TrimSpace(o.BaseURL) == "" {
		o.BaseURL = DefaultDDragonURL
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if strings.TrimSpace(o.Locale) == "" {
		o.Locale = DefaultDDragonLocale
	}
	if len(o.Kinds) == 0 {
		o.Kinds = StaticKinds()
	}
	if o.Attempts <= 0 {
		o.Attempts = DefaultStaticAttempts
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultStaticTimeout
	}
}

func (o *StaticOptions) store(ctx context.Context, kind, version string, payload []byte) error {
	at := o.Now().UTC()
	if err := o.Writer.WriteStatic(ctx, kind, version, o.Locale, payload, at); err != nil {
		return fmt.Errorf("archive static %s: %w", kind, err)
	}
	return nil
}

// fetch retrieves one document, retrying only what is worth retrying: a
// transport error or a 5xx. A 404 on a document path is a bug in this package
// (or a locale/version that does not exist) and repeating it changes nothing.
func (o *StaticOptions) fetch(ctx context.Context, client Doer, path string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= o.Attempts; attempt++ {
		payload, status, err := o.attempt(ctx, client, path)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if status >= 400 && status < 500 {
			return nil, err
		}
		if attempt == o.Attempts {
			break
		}
		delay := staticBackoffBase << (attempt - 1)
		delay += time.Duration(rand.Int63n(int64(delay/2 + 1)))
		o.Log.Warn("static fetch failed; retrying",
			"url", o.BaseURL+path, "attempt", attempt, "err", err, "retry_in", delay.String())
		if err := o.Clock.Sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (o *StaticOptions) attempt(ctx context.Context, client Doer, path string) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, o.BaseURL+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	body, readErr := decodeStaticBody(resp)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	if closeErr != nil {
		return nil, resp.StatusCode, fmt.Errorf("close body: %w", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("unexpected status %d (%s)",
			resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return body, resp.StatusCode, nil
}

func decodeStaticBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer func() { _ = zr.Close() }()
		reader = zr
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maxStaticBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxStaticBytes {
		return nil, fmt.Errorf("document larger than %d bytes", maxStaticBytes)
	}
	return payload, nil
}

// staticDocumentPath maps a kind onto its Data Dragon path. The path shape is
// per-kind because Data Dragon is not uniform: the rune tree is "reforged" and
// the summoner spell document is still called summoner.json.
func staticDocumentPath(kind, version, locale string) (string, bool) {
	switch kind {
	case KindChampions:
		return fmt.Sprintf("/cdn/%s/data/%s/champion.json", version, locale), true
	case KindItems:
		return fmt.Sprintf("/cdn/%s/data/%s/item.json", version, locale), true
	case KindRunes:
		return fmt.Sprintf("/cdn/%s/data/%s/runesReforged.json", version, locale), true
	case KindSummonerSpells:
		return fmt.Sprintf("/cdn/%s/data/%s/summoner.json", version, locale), true
	default:
		return "", false
	}
}

// latestVersion reads the newest entry of versions.json.
//
// The list is ordered newest first by Data Dragon, but the newest entry is also
// the maximum by patch number, and taking the maximum is the property worth
// relying on: a list ordering is a formatting detail, and a client that trusts
// it silently pins an old patch if the order ever changes.
func latestVersion(payload []byte) (string, error) {
	var versions []string
	if err := json.Unmarshal(payload, &versions); err != nil {
		return "", fmt.Errorf("ddragon versions: decode: %w", err)
	}
	latest := ""
	for _, v := range versions {
		if patchGreater(v, latest) {
			latest = v
		}
	}
	if latest == "" {
		return "", errors.New("ddragon versions: empty list")
	}
	return latest, nil
}

// patchGreater compares two Data Dragon versions numerically, segment by
// segment. Lexicographic comparison would rank "9.1" above "16.18".
func patchGreater(a, b string) bool {
	as, bs := splitNumeric(a), splitNumeric(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

func splitNumeric(version string) []int {
	parts := strings.Split(version, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		value := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			value = value*10 + int(r-'0')
		}
		out = append(out, value)
	}
	return out
}
