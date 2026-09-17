# Runbook: served-response integrity (the cache replay bug)

Covers the case where `/` and the other pages answer `200 OK` with a body that is
not the page: the web tier's cache replayed a damaged entity. The response looks
internally consistent - the `Content-Length` matches the bytes delivered - so it
is easy to miss unless the check looks at the document itself. The check that
catches it is `scripts/verify-serving.sh`; the config that caused it is
`deploy/base/web/caddyfile.yaml`.

This is not the "the site is down" runbook (`ingest-down.md`) and not the "the
numbers are old" one (`rebuild-aggregates.md`). Everything here answers 200.

## Symptom

- A page renders as a fragment: markup stops mid-tag, no footer, and - on this
  site that is a compliance failure, not just a cosmetic one - no
  `PREVIEW - illustrative data` labelling.
- `curl -D -` shows `Cache-Status: Souin; hit; ...; detail=OTTER`, `Vary:
  Accept-Encoding`, and no `Content-Encoding` (the body is plain HTML even when
  the client asked for a compressed encoding).
- The body is far smaller than the page and stops mid-markup. `Content-Length`
  equals the bytes delivered, so nothing about the headers looks wrong: the tell
  is the size and the absence of `</html>`.
- The file on disk is intact: same size, banner present. The served entity does
  not match the file.
- The first request after a pod start is correct. Every request after it is not,
  until the `ttl` elapses; the request after expiry is a `uri-miss` that serves
  the whole file again.

## Root cause

A defect in the pinned cache module, not in the Caddyfile's own options.

`build/caddy/Dockerfile` pins `cache-handler v0.16.0`, which asks for
`storages/core v0.0.15`. The storage module it also pins, `otter/caddy v0.0.18`,
requires `storages/core v0.0.18`, and minimal version selection takes the
higher one. Core v0.0.18 replays a hit with:

```go
func readResponse(data []byte, req *http.Request) (*http.Response, error) {
	buf := bufPool.Get().(*bytes.Buffer)
	defer func() {
		reader.Reset(buf)
		buf.Reset()
		bufPool.Put(buf)          // returned to the pool here
	}()
	_, _ = reader.WriteTo(buf)     // lz4-decoded entity
	return http.ReadResponse(bufio.NewReader(buf), req)
}
```

The `*http.Response` that comes back reads its `Body` from `buf`, and `buf` is
reset and handed to the pool before the caller has read that body. Only what
`bufio.NewReader` pulled on its first fill - 4096 bytes - survives; the rest is
gone. What reaches the client is therefore a body of `4096 - len(stored header
block)` bytes. The module sets the client-visible `Content-Length` from the body
it actually has, so the damaged response is internally consistent, carries
`200 OK`, and lives for the rest of the `ttl`.

The arithmetic is the fingerprint, and it is deterministic:

| measurement | result |
| --- | --- |
| 35179-byte page, hit | 3790 bytes (`4096 - 306` bytes of header block) |
| 40000-byte probe, hit | 3789 bytes |
| 8388608-byte probe, hit (fully stored) | 3786 bytes |
| same, plus a 500-byte `X-Pad` response header | 3280 bytes, i.e. 509 fewer |
| gzip'd page (10182 hit bytes), hit | 3783 bytes |
| `encode zstd gzip` removed | still truncates |
| `cache` removed | 40000 in, 40000 out |

The first request is always whole because a miss streams the file server's own
response and the module stores its dump in parallel; only the replay is damaged.
That is why a smoke test that requests each URL once cannot see it, and why the
guard requests every page twice.

`storages/core v0.0.19` fixes `readResponse` (it reads straight from the lz4
reader), but that line needs Caddy 2.11 and the same `Dockerfile` records why the
pins are where they are. So the fix here is a config-level one.

## What it is not, and why that matters

The first observation of this defect was a page that was correct on disk before
and after a poisoned `TTL` window, which invites the explanation "a transient
short read was taken, stored, and then served as complete". The measurements do
not support the storage half of that, and it matters, because that explanation
would make the publish path the fix:

- **The store was complete.** An 8388608-byte file replayed as 3786 bytes: a
  stored entity of that size can only have come from reading the whole file, and
  it is the *replay* that is short. Similarly a 3791-byte file replays as 3790,
  because 3791 bytes of body plus a 306-byte header block is 4097 - one byte past
  the fill - and a 40000-byte one replays as 3789.
- **No writer is needed.** The same truncation is reproduced against synthetic
  files that are written once and never touched, at every size above the fill.
- **Response headers change the length.** Adding a 500-byte `X-Pad` response
  header moves the replay from 3789 to 3280 bytes. A disk read cannot do that.
- **An incomplete upstream response is not stored at all.** With a stand-in
  upstream that declares `Content-Length: 5000` (and, separately, `1000`) but
  sends 210 bytes, both the capped and the uncapped config produced *no*
  `Cache-Status` header on either request - the module declined to cache it -
  while a complete 210-byte response from the same upstream was stored and
  replayed as a hit. So a torn read, if one happened, would not become a cached
  entity.
- **A torn read could not be produced here at all.** Truncating an 8 MB served
  file to 300 KB three seconds into a rate-limited transfer still delivered all
  8388608 bytes on this stack (sendfile and the page cache), so H1 was not
  reachable locally to test end to end; the bullet above is the closest
  available evidence, and it is evidence about the store path, not about NFS.

Conclusion: the serving defect is the replay path, and the config fix below is
what addresses it. The publish path is a separate question, and it is already
rename-based (see "Residual risk").

## What the config does about it

`deploy/base/web/caddyfile.yaml` sets `max_cacheable_body_bytes 2048` in the
global `cache` block, with the derivation in a comment above it. Keeping every
stored entity under the 4096-byte fill - 2048 bytes of body plus the ~320-byte
header block `file_server` emits is ~2.4 KB - means the module never stores
anything it could later replay damaged. Longer responses are forwarded with
`detail=UPSTREAM-RESPONSE-TOO-LARGE` and are complete.

Measured on the real image with the Caddyfile extracted from the ConfigMap, as
uid 1000, against a copy of the volume: 2048 bytes cached and whole, 2049 bytes
and every size above it forwarded and whole, nothing truncated at any size.

`deploy/base/web/deployment.yaml` carries `lolstats.dev/caddyfile-revision`,
which is what rolls the tier when the Caddyfile changes - a ConfigMap mounted with
`subPath` does not update a running pod on its own. Bump it with the change.

## The check

```sh
sh scripts/verify-serving.sh                       # $LOLSTATS_SITE_URL, else 127.0.0.1:8080
sh scripts/verify-serving.sh https://stats.example.org
kubectl -n lolstats port-forward svc/lolstats-web 8080:80 & sh scripts/verify-serving.sh
```

It fetches `/`, `/tier-list/support/`, `/champions/ahri/`, `/about/`,
`/healthz` and `/agg/v1/manifest.json`, and for each page it makes three requests:
a cache-busting one (`?lolstats-verify=<run id>`, so the cache key is new and the
body is the file server's own), then the miss, then the hit. Per response it
asserts that the status is 200, that the delivered byte count equals the declared
`Content-Length`, that a page ends in `</html>`, that it carries the banner its
own declared data state requires, and that its bytes equal the cache-busting
reference response. It exits non-zero on any failure. Against the unfixed config
it reports 16 failures and names the byte counts; against the fixed one, all pass.

The assertions that catch this defect are the `</html>`, banner and
reference-equality ones. The length-versus-delivered one is kept because it
catches other damage, but it would pass on a poisoned hit: the module rewrites
`Content-Length` to the truncated length, so the damaged response is internally
consistent. Comparing the two plain requests with each other is not enough on its
own either, for the same reason: a run that starts after the entry is already
poisoned sees 3790 bytes twice, and two identical damaged bodies agree. That is
what the third, cache-busting request is for - it cannot be a hit, so it is the
one response that a poisoned entry cannot imitate. A single request per URL
likewise passes, because the miss is whole.

The banner assertion is state-aware: the site renders three honest states (demo,
live, no-data) with different wording, so a page must carry the banner for the
state it declares, and a page that declares no state fails. By default the check
also requires that state to be `demo`, because the deployed tier builds with no
Riot key and must label itself as illustrative; set `LOLSTATS_EXPECT_STATE=live`
or `no-data` when checking a tier built from a real or empty snapshot:

```sh
LOLSTATS_EXPECT_STATE=live sh scripts/verify-serving.sh http://127.0.0.1:18082
```

Run it after a publish, after a Caddyfile change, and after any change to
`build/caddy/Dockerfile`. It is the only check in the repo that reads the served
responses rather than the built files.

Two cheaper checks cover the same labelling requirement from the other side, and
both run in the launch gate (`sh scripts/compliance-check.sh`): check 6 compares
the approved wording in the site source (`web/src/lib/legal.ts`) against its
declaration, and check 11 walks every page in `web/dist` and fails if any page is
missing the banner its state declares or does not end in `</html>`. Check 11
cannot see a serving-layer defect - the built files were never damaged - so it is
the guard above, not check 11, that catches this bug.

## Reproduce locally, without the cluster

```sh
docker build -f build/caddy/Dockerfile -t lolstats-caddy:localtest .
awk '/^  Caddyfile: \|/{f=1;next} f{sub(/^    /,"");print}' \
  deploy/base/web/caddyfile.yaml > .agent-artifacts/repro/Caddyfile
docker volume create lolrepro
docker run -d --name lolrepro-caddy -u 1000:1000 -p 18082:8080 \
  -v lolrepro:/var/lib/lolstats \
  -v "$PWD/.agent-artifacts/repro/Caddyfile":/etc/caddy/Caddyfile:ro \
  lolstats-caddy:localtest
sh scripts/verify-serving.sh http://127.0.0.1:18082
docker rm -f lolrepro-caddy && docker volume rm lolrepro
```

`awk` is how the Caddyfile is taken out of the ConfigMap: it is an indented block
in a CR, so the first extracted line should be `{` - check that before trusting a
result. Running the image as uid 1000 matters, because that is the uid the tier
runs as.

To see the defect itself, delete the `max_cacheable_body_bytes` line, reload, and
request any page twice. Two further experiments separate this cause from a torn
read:

```sh
# An upstream whose body ends before its declared Content-Length is not cached:
# point a cache-wrapped reverse_proxy at a socket that answers
# "Content-Length: 5000" and sends 210 bytes, and note that neither request
# carries a Cache-Status header, while a complete 210-byte answer is cached.
# Mid-transfer truncation does not shorten what the client receives on this
# stack: truncate an 8 MB served file during a rate-limited transfer
# (curl --limit-rate) and all 8388608 bytes still arrive.
```

## The confirmation run

Real image, Caddyfile rendered from the overlay and served as uid 1000, against a
private copy of a demo tree whose `index.html` is 34582 bytes and carries the
banner. Three plain requests for `/` per config, same pod, only the cap line
differs:

| config | request | Content-Length | delivered | `</html>` | `PREVIEW` | Cache-Status |
| --- | --- | --- | --- | --- | --- | --- |
| cap present | 1 | 34582 | 34582 | 1 | 1 | `fwd=uri-miss; detail=UPSTREAM-RESPONSE-TOO-LARGE` |
| cap present | 2 | 34582 | 34582 | 1 | 1 | `fwd=uri-miss; detail=UPSTREAM-RESPONSE-TOO-LARGE` |
| cap present | 3 | 34582 | 34582 | 1 | 1 | `fwd=uri-miss; detail=UPSTREAM-RESPONSE-TOO-LARGE` |
| cap deleted | 1 | 34582 | 34582 | 1 | 1 | `fwd=uri-miss; stored` |
| cap deleted | 2 | 3790 | 3790 | 0 | 0 | `hit; ttl=59; detail=OTTER` |
| cap deleted | 3 | 3790 | 3790 | 0 | 0 | `hit; ttl=59; detail=OTTER` |

Every row is `200 OK`. The uncapped pair is the reported defect to the byte: the
reply's own `Content-Length` matches its short body, so nothing about the
response admits it is wrong, and the page has lost its tail and its mandatory
labelling. Against the capped pod the guard exits 0 with every page whole on the
reference, the miss and the hit; against the uncapped one it exits 1 with ten
failures, all of them cache hits and all of them 3790-byte bodies.

### Can a short entity be stored and later served as a complete `200 OK`?

This is the question the fix has to answer: not whether a torn read happens, but
whether a shortened response can enter the cache and outlive whatever produced it.
Same image, same pod settings, uid 1000, `file_server` rather than a proxy, with
`/probe/index.html` on disk holding only the first 17000 bytes of the real page
(no `</html>`, no banner):

| request | cap present | cap deleted |
| --- | --- | --- |
| 1, short file on disk | 200, 17000 B, `fwd=uri-miss; detail=UPSTREAM-RESPONSE-TOO-LARGE` | 200, 17000 B, `fwd=uri-miss; stored` |
| 2, same file | 200, 17000 B, still uncached | 200, **3790 B**, `hit; ttl=59; detail=OTTER` |
| 3, after the file is completed to 34582 B | 200, **34582 B**, whole | 200, **3790 B**, `hit` - still poisoned |

Two things follow. A response the file server considers complete is stored
whatever its length, so a half-written file does become a cache entry, and the
replay bug then shortens it further; the cap refuses anything over 2048 bytes
*before* the store decision, so nothing retains it, and the moment the writer
finishes the file the very next request is correct. Without the cap the same file
stays broken for every visitor for a full `ttl` *after* it has been repaired,
which is the reported sequence: `hit` -> TTL expiry -> `fwd=uri-miss; stored` ->
correct.

The cap is not a general defence, though: a truncated page *under* 2048 bytes is
stored and replayed as a hit for a full `ttl` by both configs, because it fits
inside the one buffer fill the replay bug preserves (measured: a 1200-byte
truncated page, `fwd=uri-miss; stored` then `hit; ttl=59`, still 1200 bytes).
What stops that class is the publish path, not the cache config.

## Residual risk

- **The fix removes replay damage, not staleness.** What is stored can still be
  up to `ttl` old, and a *complete* old entity is served as a hit; that is
  ordinary cache behaviour and expected for a tree that is republished nightly.
  What is no longer possible is the case this runbook is about: a complete entity
  replayed truncated. Incomplete responses do not enter the cache at all (the
  evidence is in "What it is not" above), so the remaining exposure is content
  that is old but whole.
- **The cap is derived, not magic.** A page whose *header block* grew past ~2 KB
  while its body stayed under the cap would truncate again. Nothing in this route
  does that, and the guard would catch it.
- **`site-build` swaps the served directory in two renames**
  (`mv site .site-previous`, `mv .site-staging site`), so a reader can see the
  tree absent for the gap between them: a 404, not a truncated page. An atomic
  swap of a directory onto a non-empty directory is not possible with plain
  `rename(2)`; the options are a symlink swap (which itself needs a grace period
  before the old target is deleted) or `renameat2(RENAME_EXCHANGE)`. Not done
  here, because it is not the cause of this defect and it changes the layout that
  the jobs and the runbooks assume. Worth doing if that gap ever shows up in a
  log.
- **The Go jobs' own atomicity is out of scope here.** `docs/aggregation.md`
  section 7 records that the aggregate tree publishes by rename with
  `manifest.json` last, and `static-sync`'s subcommand does not exist yet
  (`deploy/base/jobs/static-sync.yaml` says so); anything those jobs do
  in-place is discussed there, not here.
- **A small partial file would still be cached.** If a writer ever created a
  page in place, a reader could see it half-written; nothing in the cache
  prevents storing a *complete-looking* short response that is under the cap, and
  a partial 1 KB page would then be served as a hit for up to `ttl` (measured:
  a 1200-byte truncated page is stored and replayed, `hit; ttl=59`). Nothing on
  this path does that: `site/` is written by `site-build` into a staging
  directory and swapped in by rename, so a reader sees the old tree or the new
  one, never a file in progress. That is why the publish path should stay
  rename-based: the cap bounds replay damage, and the rename bounds what there is
  to replay.
- **H1 (a torn read on NFS) was not reproduced and is not needed** to explain
  this defect, but neither is it excluded on the cluster. The evidence that an
  incomplete response is not stored comes from a controlled `reverse_proxy`
  upstream that declared a larger `Content-Length` than it sent (no `Cache-Status`
  header on either request); a `file_server` response whose body ends before its
  declared length could **not** be produced on this stack - a rate-limited 8 MB
  transfer still delivered all 8388608 bytes - so on the cluster it is untested.
  Above 2048 bytes the cap settles it either way, because such a response is
  refused before the store decision. What remains is the publish path: if a
  future job publishes small files in place, the store path's refusal of
  incomplete responses is the only thing between a torn read and a cached partial
  page, and it should stay rename-based.
- **Revisit the cap when the pins move.** With Caddy 2.11 and
  `storages v0.0.19+` in `build/caddy/Dockerfile`, the underlying bug is gone and
  this cap should be raised or removed, with the guard run afterwards.
- **`encode zstd gzip` stays as it is.** Brotli is not in the official image's
  module set and `encode br` fails config adaptation at start-up; the earlier
  hypothesis that a compressed entity was being replayed uncompressed was measured
  and disproved (gzip -9 of the home page is 7177 bytes, not 3790).
