package crawl

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Erik-Schuetze/league-of-legends/internal/riot"
)

// fakeDDragon is the Data Dragon CDN as the static sync sees it: versioned
// paths, per-path status codes and an optional gzip encoding. It records every
// path it was asked for, because the layout of those requests is part of what
// static-sync has to get right.
type fakeDDragon struct {
	mu sync.Mutex

	bodies   map[string][]byte
	status   map[string]int
	failures map[string]int
	gzip     bool
	hits     map[string]int
	order    []string
}

func newFakeDDragon() *fakeDDragon {
	return &fakeDDragon{
		bodies:   map[string][]byte{},
		status:   map[string]int{},
		failures: map[string]int{},
		hits:     map[string]int{},
	}
}

func (f *fakeDDragon) serve(path string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[path] = body
}

// fail makes the next n requests for path answer with a status.
func (f *fakeDDragon) fail(path string, status, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[path] = status
	f.failures[path] = n
}

func (f *fakeDDragon) hitCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeDDragon) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.order))
	copy(out, f.order)
	return out
}

func (f *fakeDDragon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	f.order = append(f.order, r.URL.Path)
	status := f.status[r.URL.Path]
	if f.failures[r.URL.Path] > 0 {
		f.failures[r.URL.Path]--
	} else {
		status = 0
	}
	body := f.bodies[r.URL.Path]
	gzipIt := f.gzip
	f.mu.Unlock()

	if gzipIt {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = zw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		body = buf.Bytes()
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// ddragonFixture is the checked-in Data Dragon document set served on the
// versioned paths the sync builds.
func ddragonFixture(t *testing.T, server *fakeDDragon, version string) map[string][]byte {
	t.Helper()
	docs := map[string][]byte{
		"/api/versions.json":                                 fixture(t, "ddragon/versions.json"),
		"/cdn/" + version + "/data/en_US/champion.json":      fixture(t, "ddragon/champion.json"),
		"/cdn/" + version + "/data/en_US/item.json":          fixture(t, "ddragon/item.json"),
		"/cdn/" + version + "/data/en_US/runesReforged.json": fixture(t, "ddragon/runesReforged.json"),
		"/cdn/" + version + "/data/en_US/summoner.json":      fixture(t, "ddragon/summoner.json"),
	}
	for path, body := range docs {
		server.serve(path, body)
	}
	return docs
}

func newStaticOptions(t *testing.T, server *fakeDDragon) (StaticOptions, *fakeWriter, *riot.FakeClock) {
	t.Helper()
	writer := newFakeWriter()
	clock := riot.NewFakeClock(testBaseTime())
	_ = server
	return StaticOptions{
		Writer:  writer,
		Log:     testLogger(),
		Clock:   clock,
		Now:     clock.Now,
		BaseURL: "http://ddragon.test",
	}, writer, clock
}

func TestStaticSyncMirrorsEveryDocument(t *testing.T) {
	server := newFakeDDragon()
	docs := ddragonFixture(t, server, "16.20.1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, writer, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL

	result, err := StaticSync(context.Background(), opts)
	if err != nil {
		t.Fatalf("StaticSync: %v", err)
	}
	if result.Version != "16.20.1" {
		t.Fatalf("version = %q, want the newest of the advertised list", result.Version)
	}
	if result.Locale != DefaultDDragonLocale {
		t.Fatalf("locale = %q, want %q", result.Locale, DefaultDDragonLocale)
	}
	if len(result.Documents) != len(StaticKinds()) {
		t.Fatalf("documents = %d, want %d", len(result.Documents), len(StaticKinds()))
	}
	for i, kind := range StaticKinds() {
		if result.Documents[i].Kind != kind {
			t.Fatalf("document %d = %q, want %q: the patch label depends on versions.json being first",
				i, result.Documents[i].Kind, kind)
		}
	}

	got := writer.staticKinds()
	if len(got) != len(StaticKinds()) {
		t.Fatalf("retained %v, want every kind", got)
	}
	for i, kind := range StaticKinds() {
		if got[i] != kind {
			t.Fatalf("retained %v, want %v", got, StaticKinds())
		}
	}

	for path, body := range docs {
		kind := kindForPath(t, path)
		write := writer.staticWriteFor(kind)
		if !bytes.Equal(write.payload, body) {
			t.Fatalf("%s: retained %d bytes, want the %d bytes the CDN served verbatim",
				kind, len(write.payload), len(body))
		}
		if write.version != "16.20.1" || write.locale != DefaultDDragonLocale {
			t.Fatalf("%s: retained as %s/%s", kind, write.version, write.locale)
		}
		if !write.at.Equal(testBaseTime()) {
			t.Fatalf("%s: retained at %s, want the injected clock", kind, write.at)
		}
		if server.hitCount(path) != 1 {
			t.Fatalf("%s: %d requests, want 1", path, server.hitCount(path))
		}
	}
	if writer.flushCount() != 1 {
		t.Fatalf("flushes = %d, want 1", writer.flushCount())
	}
}

func kindForPath(t *testing.T, path string) string {
	t.Helper()
	switch {
	case strings.HasSuffix(path, "/api/versions.json"):
		return KindVersions
	case strings.HasSuffix(path, "/champion.json"):
		return KindChampions
	case strings.HasSuffix(path, "/item.json"):
		return KindItems
	case strings.HasSuffix(path, "/runesReforged.json"):
		return KindRunes
	case strings.HasSuffix(path, "/summoner.json"):
		return KindSummonerSpells
	default:
		t.Fatalf("unexpected path %s", path)
		return ""
	}
}

// A pinned patch still retains versions.json: the list is where that patch came
// from, and it is one request.
func TestStaticSyncPinnedVersionStillReadsVersionsJSON(t *testing.T) {
	server := newFakeDDragon()
	ddragonFixture(t, server, "16.18.1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, writer, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL
	opts.Version = "16.18.1"

	result, err := StaticSync(context.Background(), opts)
	if err != nil {
		t.Fatalf("StaticSync: %v", err)
	}
	if result.Version != "16.18.1" {
		t.Fatalf("version = %q, want the pinned patch", result.Version)
	}
	if server.hitCount("/api/versions.json") != 1 {
		t.Fatalf("versions.json requested %d times, want 1", server.hitCount("/api/versions.json"))
	}
	for _, path := range server.paths() {
		if !strings.Contains(path, "/16.18.1/") && !strings.HasSuffix(path, "/api/versions.json") {
			t.Fatalf("requested %s, want the pinned patch's documents", path)
		}
	}
	if write := writer.staticWriteFor(KindVersions); write.version != "16.18.1" {
		t.Fatalf("versions retained as %q, want the pinned patch", write.version)
	}
}

func TestStaticSyncTrimsTheKindListButAlwaysKeepsVersions(t *testing.T) {
	server := newFakeDDragon()
	ddragonFixture(t, server, "16.20.1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, writer, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL
	opts.Kinds = []string{KindItems}

	result, err := StaticSync(context.Background(), opts)
	if err != nil {
		t.Fatalf("StaticSync: %v", err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("documents = %v, want versions and items", result.Documents)
	}
	if got := writer.staticKinds(); len(got) != 2 || got[0] != KindVersions || got[1] != KindItems {
		t.Fatalf("retained %v, want versions then items", got)
	}
	if server.hitCount("/cdn/16.20.1/data/en_US/champion.json") != 0 {
		t.Fatal("a trimmed kind list still fetched the champions document")
	}
}

func TestStaticSyncRejectsAnUnknownKind(t *testing.T) {
	server := newFakeDDragon()
	ddragonFixture(t, server, "16.20.1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, _, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL
	opts.Kinds = []string{KindChampions, "skill-tree"}

	_, err := StaticSync(context.Background(), opts)
	if err == nil {
		t.Fatal("StaticSync accepted an unknown document kind")
	}
	if !strings.Contains(err.Error(), "skill-tree") {
		t.Fatalf("err = %v, want the unknown kind named", err)
	}
}

// Only a transport error or a 5xx is worth repeating. A 404 is a bug in this
// package and repeating it changes nothing.
func TestStaticSyncRetryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		failures   int
		wantErr    bool
		wantHits   int
		wantSleeps int
	}{
		{name: "a 5xx is retried", status: 500, failures: 2, wantHits: 3, wantSleeps: 2},
		{name: "a 4xx is not retried", status: 404, failures: 1, wantErr: true, wantHits: 1, wantSleeps: 0},
		{name: "a permanent 5xx gives up after the attempts budget", status: 503, failures: 9, wantErr: true, wantHits: DefaultStaticAttempts, wantSleeps: DefaultStaticAttempts - 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newFakeDDragon()
			ddragonFixture(t, server, "16.20.1")
			server.fail("/cdn/16.20.1/data/en_US/champion.json", tc.status, tc.failures)
			httpServer := httptest.NewServer(server)
			defer httpServer.Close()

			opts, writer, clock := newStaticOptions(t, server)
			opts.BaseURL = httpServer.URL
			opts.Kinds = []string{KindChampions}

			_, err := StaticSync(context.Background(), opts)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := server.hitCount("/cdn/16.20.1/data/en_US/champion.json"); got != tc.wantHits {
				t.Fatalf("hits = %d, want %d", got, tc.wantHits)
			}
			if got := len(clock.Sleeps()); got != tc.wantSleeps {
				t.Fatalf("sleeps = %v, want %d: the retry has to wait between attempts", clock.Sleeps(), tc.wantSleeps)
			}
			if tc.wantErr && server.hitCount("/cdn/16.20.1/data/en_US/item.json") != 0 {
				t.Fatal("the walk continued past a failed document")
			}
			if !tc.wantErr && len(writer.staticKinds()) != 2 {
				t.Fatalf("retained %v, want versions and champions", writer.staticKinds())
			}
		})
	}
}

// The CDN negotiates gzip when asked, and the retained bytes are the decoded
// document rather than the transport encoding.
func TestStaticSyncDecodesGzippedDocuments(t *testing.T) {
	server := newFakeDDragon()
	docs := ddragonFixture(t, server, "16.20.1")
	server.gzip = true
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, writer, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL

	if _, err := StaticSync(context.Background(), opts); err != nil {
		t.Fatalf("StaticSync: %v", err)
	}
	want := docs["/cdn/16.20.1/data/en_US/champion.json"]
	if got := writer.staticWriteFor(KindChampions).payload; !bytes.Equal(got, want) {
		t.Fatalf("retained %d bytes, want the %d decoded bytes", len(got), len(want))
	}
}

func TestStaticSyncFailsWhenTheVersionsListIsUnreadable(t *testing.T) {
	server := newFakeDDragon()
	server.serve("/api/versions.json", []byte(`{"not":"a list"}`))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, _, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL

	if _, err := StaticSync(context.Background(), opts); err == nil {
		t.Fatal("StaticSync accepted a versions list it could not read")
	}
}

func TestStaticSyncFailsWithoutAWriter(t *testing.T) {
	if _, err := StaticSync(context.Background(), StaticOptions{}); err == nil {
		t.Fatal("StaticSync ran without an archive")
	}
}

func TestStaticSyncReportsAFlushFailure(t *testing.T) {
	server := newFakeDDragon()
	ddragonFixture(t, server, "16.20.1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, writer, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL
	writer.failFlush = errors.New("archive offline")

	if _, err := StaticSync(context.Background(), opts); err == nil {
		t.Fatal("StaticSync ignored a flush failure")
	}
}

func TestStaticSyncStopsOnContextCancellation(t *testing.T) {
	server := newFakeDDragon()
	ddragonFixture(t, server, "16.20.1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	opts, _, _ := newStaticOptions(t, server)
	opts.BaseURL = httpServer.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := StaticSync(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// The zero value of every option must be usable: the command passes a partial
// struct and relies on these defaults.
func TestStaticOptionsDefaults(t *testing.T) {
	opts := StaticOptions{}
	opts.normalizeStatic()

	if opts.BaseURL != DefaultDDragonURL {
		t.Fatalf("base url = %q, want %q", opts.BaseURL, DefaultDDragonURL)
	}
	if opts.Locale != DefaultDDragonLocale {
		t.Fatalf("locale = %q, want %q", opts.Locale, DefaultDDragonLocale)
	}
	if len(opts.Kinds) != len(StaticKinds()) {
		t.Fatalf("kinds = %v, want %v", opts.Kinds, StaticKinds())
	}
	if opts.Attempts != DefaultStaticAttempts {
		t.Fatalf("attempts = %d, want %d", opts.Attempts, DefaultStaticAttempts)
	}
	if opts.Timeout != defaultStaticTimeout {
		t.Fatalf("timeout = %s, want %s", opts.Timeout, defaultStaticTimeout)
	}
	if opts.Log == nil || opts.Metrics == nil || opts.Clock == nil || opts.Now == nil {
		t.Fatalf("defaults not filled: %+v", opts)
	}
	if got := opts.Now(); got.IsZero() {
		t.Fatal("the default clock produced a zero time")
	}
}

func TestStaticBaseURLIsTrimmedOfTrailingSlashes(t *testing.T) {
	opts := StaticOptions{BaseURL: "http://ddragon.test/"}
	opts.normalizeStatic()
	if opts.BaseURL != "http://ddragon.test" {
		t.Fatalf("base url = %q, want the trailing slash removed so paths do not double up", opts.BaseURL)
	}
}

func TestLatestVersion(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
		wantErr bool
	}{
		{
			name:    "the newest patch wins even when the list is out of order",
			payload: string(fixture(t, "ddragon/versions.json")),
			want:    "16.20.1",
		},
		{name: "a single entry", payload: `["16.1.1"]`, want: "16.1.1"},
		{name: "patch numbers compare numerically, not as strings", payload: `["16.9.1","16.10.1"]`, want: "16.10.1"},
		{name: "an empty list is an error", payload: `[]`, wantErr: true},
		{name: "a non-list is an error", payload: `{"versions":[]}`, wantErr: true},
		{name: "unparseable json is an error", payload: `[`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := latestVersion([]byte(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("latestVersion(%s) = %q, want an error", tc.payload, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("latestVersion: %v", err)
			}
			if got != tc.want {
				t.Fatalf("latestVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStaticDocumentPath(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		wantPath string
		wantOK   bool
	}{
		{name: "champions", kind: KindChampions, wantPath: "/cdn/16.20.1/data/en_US/champion.json", wantOK: true},
		{name: "items", kind: KindItems, wantPath: "/cdn/16.20.1/data/en_US/item.json", wantOK: true},
		{name: "runes", kind: KindRunes, wantPath: "/cdn/16.20.1/data/en_US/runesReforged.json", wantOK: true},
		{name: "summoner spells keep the old document name", kind: KindSummonerSpells, wantPath: "/cdn/16.20.1/data/en_US/summoner.json", wantOK: true},
		{name: "versions is not a versioned document", kind: KindVersions, wantOK: false},
		{name: "anything else", kind: "nonsense", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := staticDocumentPath(tc.kind, "16.20.1", "en_US")
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.wantPath {
				t.Fatalf("path = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

// The fixtures are checked in, so a missing one is a test-harness failure
// rather than a crawler bug.
func TestDdragonFixturesExist(t *testing.T) {
	for _, rel := range []string{
		"ddragon/versions.json", "ddragon/champion.json", "ddragon/item.json",
		"ddragon/runesReforged.json", "ddragon/summoner.json",
	} {
		path := filepath.Join("..", "..", "fixtures", filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fixture %s: %v", rel, err)
		}
	}
}
