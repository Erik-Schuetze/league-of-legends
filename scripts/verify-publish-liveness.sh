#!/usr/bin/env bash
# scripts/verify-publish-liveness.sh - the Phase 6 exit criterion "publishing a
# new snapshot by rename makes it visible to the web tier without a restart",
# measured instead of asserted.
#
# The tier reads its snapshot from an NFS-backed RWX PVC mounted read-only at
# /var/lib/lolstats. A read-only NFS mount means attribute caching applies, so
# "the tier did not notice" is ambiguous on its own: it can be the feature
# failing, or the instrument being blind. This script therefore runs a positive
# control *before* it judges anything, and a negative control *after*:
#
#   baseline        capture the exact live bytes (sha256), the rendered marker,
#                   and the identity of the serving pods (uid, creationTimestamp,
#                   restartCount)
#   positive ctrl   republish the SAME snapshot (identical bytes, only the
#                   aggregate timestamp inside the staged copy differs) through
#                   the real rename path, then poll every observable: this proves
#                   a change started in .staging is observable at all
#   wait            with no writes at all, poll the same observables for the same
#                   length of time: anything that moves here is drift, not the
#                   publish
#   restore         every observable must return to exactly the baseline bytes:
#                   proof the observer reacts in both directions
#   real publish    rename the candidate in, then poll with no writes in between:
#                   if it appears, the audience discovered it through the reader
#                   path rather than through a restart
#   identity        re-capture pod uid/creationTimestamp/restartCount: if they
#                   moved, the run is void and this script says so
#   final restore   put the original manifest and partition back byte-for-byte
#                   and verify sha256 returns to the baseline value
#
# The change is published with the project's own binary (cmd/lolstats-aggregate,
# cross-compiled for the node architecture) executed inside a probe pod that has
# the PVC mounted - i.e. through internal/aggregate's real displace+rename path,
# not by writing over the live file in place.
#
#   bash scripts/verify-publish-liveness.sh              # dry run, reads only
#   bash scripts/verify-publish-liveness.sh --apply      # run the whole thing
#
# Requires kubectl context access to the cluster and nothing else. Every pod it
# creates is deleted on exit; the PVC is left byte-identical.

set -uo pipefail

NS="${NS:-lolstats}"
DEPLOY="${DEPLOY:-lolstats-go-web}"
SVC="${SVC:-lolstats-go-web}"
PVC="${PVC:-lolstats-data}"
STAGING_DIR="${STAGING_DIR:-/data/agg/.staging-publish-liveness}"
BACKUP_DIR="${BACKUP_DIR:-/data/agg/.baseline-publish-liveness}"
PROBE_POD="${PROBE_POD:-publish-liveness-probe}"
PROBE_IMAGE="${PROBE_IMAGE:-python:3.12-alpine}"
WAIT_SECONDS="${WAIT_SECONDS:-270}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-240}"
PROBE_AGG_ROOT="${PROBE_AGG_ROOT:-/data/agg}"
AGG_ROOT="/var/lib/lolstats/agg"
PLAN_PATH="${PLAN_PATH:-/tmp/publish-liveness-plan.json}"
MANIFEST_REL="v1/manifest.json"
URL_PREFIX="/agg"
# Served over HTTP at /agg/v1/manifest.json; on the PVC at /var/lib/lolstats/agg/v1/manifest.json.
MANIFEST_URL_REL="${URL_PREFIX}/${MANIFEST_REL}"
# Other lanes' probe pods in this namespace also carry
# app.kubernetes.io/component=web-go, so the tier is identified by the full
# deployment selector; only this pair of labels is the tier under test.
TIER_SELECTOR="app.kubernetes.io/component=web-go,app.kubernetes.io/name=lolstats"
MARKER_STAMP="${MARKER_STAMP:-2026-09-18T04:10:00Z}"

APPLY=0
KEEP_PROBE=0
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    --keep-probe) KEEP_PROBE=1 ;;
    -h|--help) sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

WORKDIR="$(cd "$(dirname "$0")/.." && pwd)/.agent-artifacts/publish-liveness"
mkdir -p "$WORKDIR"

say()  { printf '\n== %s\n' "$*"; }
note() { printf '   %s\n' "$*"; }
die()  { printf '\n!! %s\n' "$*" >&2; EXIT_NOTE="$*"; exit 1; }
raw()  { printf '   | %s\n' "$*"; }

PODS_CREATED=""
PORTFORWARDS=""
POD_LOG_SINCE=""
BASELINE_SHA=""
LIVE_PARTITION=""
RESTORED=0
VOID=0
EXIT_NOTE=""

k() { kubectl -n "$NS" "$@"; }

sha256_of() { python3 -c 'import hashlib,sys;sys.stdout.write(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'; }

# The tier understands `Cache-Control: no-cache`, and the HTML cache policy is
# private/max-age=60, so every read asks for a revalidation and asks curl not to
# keep its own copy: what is being measured has to be the tier's view of the
# PVC, never a local cache in front of it.
fetch() { curl -fsS -m 20 -H 'Cache-Control: no-cache' -H 'Pragma: no-cache' "$1" 2>/dev/null || true; }

manifest_stamps() {
  python3 -c '
import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("\t"); raise SystemExit
print("%s\t%s" % (d.get("generated_at", ""), d.get("latest", {}).get("generated_at", "")))
'
}

data_state() { grep -o 'data-state="[^"]*"' | head -1 | sed 's/data-state="//; s/"$//'; }

rendered_stamps() { grep -oE 'generated [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2} UTC' | sed 's/^generated //' | sort -u | paste -sd, -; }

# ---------------------------------------------------------------------------
# The reader path. On deliberately generated content the landing page is not
# cached by the tier itself (server.go skips its own cache for data-state !=
# live), so the HTML can be read back as freshly as the JSON can; the control
# snapshot stays riot-match-v5 and therefore keeps both paths independent of the
# tier's HTML cache. Both are read on every sample, and both are reported.
# ---------------------------------------------------------------------------
observe() { # $1 = base url for the svc, $2 = a label shown in the transcript
  local base="$1" label="$2" m p j
  m="$(fetch "$base$MANIFEST_URL_REL")"
  p="$(fetch "$base/")"
  j="$(printf '%s' "$m" | manifest_stamps)"
  printf '%s\thttp-manifest-sha256\t%s\n' "$label" "$(printf '%s' "$m" | sha256_of)"
  printf '%s\thttp-manifest-stamps\t%s\n' "$label" "$(printf '%s' "$j" | tr '\t' ' ')"
  printf '%s\tdata-state\t%s\n' "$label" "$(printf '%s' "$p" | data_state)"
  printf '%s\trendered-stamps\t%s\n' "$label" "$(printf '%s' "$p" | rendered_stamps)"
}

MANIFEST_SHA="" MANIFEST_LATEST="" DATA_STATE="" RENDERED="" HTTP_SHA=""
# One sample, parsed into globals. Returns 0 when it could be read at all.
# The tier dates the page it serves from the manifest's `latest` pointer
# (internal/webtier/artifacts.go sets site.latest = &manifest.Latest), and the
# sitemap from the manifest's own generated_at, so both are read and both are
# required to move.
sample() { # $1 = base url
  local base="$1" m p j
  m="$(fetch "$base$MANIFEST_URL_REL")"
  p="$(fetch "$base/")"
  j="$(printf '%s' "$m" | manifest_stamps)"
  HTTP_SHA="$(printf '%s' "$m" | sha256_of)"
  MANIFEST_SHA="$(printf '%s' "$j" | cut -f1)"
  MANIFEST_LATEST="$(printf '%s' "$j" | cut -f2)"
  DATA_STATE="$(printf '%s' "$p" | data_state)"
  RENDERED="$(printf '%s' "$p" | rendered_stamps)"
  return 0
}

STATE_OF() { printf 'manifest_sha=%s generated_at=%s data_state=%s rendered=%s' "$HTTP_SHA" "$MANIFEST_SHA" "$DATA_STATE" "$RENDERED"; }
wait_for_delete() { # $1 = pod name
  local i
  for i in $(seq 1 60); do
    k get pod "$1" >/dev/null 2>&1 || return 0
    sleep 2
  done
  return 1
}

# ---------------------------------------------------------------------------
# Cleanup. Registered before anything is created.
# ---------------------------------------------------------------------------
cleanup() {
  local pf
  for pf in $PORTFORWARDS; do kill "$pf" >/dev/null 2>&1; done
  if [ "$KEEP_PROBE" = "0" ]; then
    for p in $PODS_CREATED; do
      k delete pod "$p" --wait=false >/dev/null 2>&1
    done
    for p in $PODS_CREATED; do wait_for_delete "$p" || note "probe pod $p is still terminating"; done
  else
    note "--keep-probe: leaving $PODS_CREATED running"
  fi
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# The probe. It mounts the same PVC the tier does, so a change it makes is a
# change on the same NFS server and the same export.
# ---------------------------------------------------------------------------
create_probe() {
  local node
  node="$(k get pod -l "$TIER_SELECTOR" -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null)"
  say "creating probe pod $PROBE_POD (node ${node:-<any>})"
  local spec
  read -r -d '' spec <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: $PROBE_POD
  namespace: $NS
  labels: {app.kubernetes.io/name: lolstats, app.kubernetes.io/component: publish-liveness-probe}
  annotations: {lolstats.rocks/purpose: "publish-liveness verification, temporary"}
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
    seccompProfile: {type: RuntimeDefault}
  nodeName: ${node:-}
  containers:
    - name: probe
      image: $PROBE_IMAGE
      command: ["sleep", "86400"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: ["ALL"]}
      resources:
        requests: {cpu: 10m, memory: 32Mi}
        limits: {cpu: 200m, memory: 128Mi}
      volumeMounts:
        - {name: data, mountPath: /data}
        - {name: tmp, mountPath: /tmp}
  volumes:
    - name: data
      # The probe stands in for the aggregate writer, which mounts this claim
      # read-write; the tier under test keeps its own read-only mount.
      persistentVolumeClaim: {claimName: $PVC, readOnly: false}
    - name: tmp
      emptyDir: {}
YAML
  printf '%s\n' "$spec" > "$WORKDIR/probe-pod.yaml"
  k delete pod "$PROBE_POD" --ignore-not-found --wait=true >/dev/null 2>&1
  k apply -f "$WORKDIR/probe-pod.yaml" >/dev/null || die "probe pod could not be created"
  PODS_CREATED="$PROBE_POD"
  k wait --for=condition=Ready "pod/$PROBE_POD" --timeout=180s >/dev/null || die "probe pod never became Ready"
  note "probe pod Ready"
}

pexec() { k exec -i "$PROBE_POD" -- "$@"; }

pvc_sha() { # $1 = path below the mount
  pexec python3 -c 'import hashlib,sys;print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1" | tr -d '\r'
}

# A hash of the whole aggregate tree - names, sizes and bytes - so "restored" is
# a statement about the tree, not about one file.
agg_tree_sha() {
  pexec python3 -c '
import hashlib,os,sys
root, skip = sys.argv[1], set(sys.argv[2].split(","))
h = hashlib.sha256()
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = sorted(d for d in dirnames if d not in skip)
    for name in sorted(filenames):
        full = os.path.join(dirpath, name)
        rel = os.path.relpath(full, root)
        h.update(rel.encode()); h.update(b"\0")
        with open(full, "rb") as fh:
            h.update(hashlib.sha256(fh.read()).digest())
print(h.hexdigest())
' "$1" "$2" | tr -d '\r'
}

agg_entries() {
  pexec python3 -c '
import os,sys
root, skip = sys.argv[1], set(sys.argv[2].split(","))
out = []
for dirpath, dirnames, filenames in os.walk(root):
    dirnames[:] = sorted(d for d in dirnames if d not in skip)
    for name in sorted(filenames):
        out.append(os.path.relpath(os.path.join(dirpath, name), root))
print("\n".join(sorted(out)))
' "$1" "$2" | tr -d '\r' | grep -v '^$' | sort
}

SCRATCH_DIRS=".staging-publish-liveness,.baseline-publish-liveness"

# Names of everything at the top of the mount and at the top of the aggregate
# root. We only ever write below the aggregate root, so this listing has to be
# unchanged even though its contents are not part of the byte-for-byte proof.
pvc_listing() {
  pexec sh -c "ls -1a /data; echo --; ls -1a $PROBE_AGG_ROOT" | tr -d '\r' | sort
}

# ---------------------------------------------------------------------------
# The injector. It runs inside the probe pod, which mounts the PVC read-write.
# It builds a complete clone of the live snapshot in a staging directory beside
# the live one (rename(2) cannot cross filesystems, so staging has to be on the
# aggregate root itself - the same constraint internal/aggregate/build.go
# documents), moves the clone's aggregate timestamp, and then publishes it with
# internal/aggregate's own displace-and-rename path.
# ---------------------------------------------------------------------------
injector() {
  pexec env \
    AGG_ROOT="$PROBE_AGG_ROOT" \
    STAGING_DIR="$STAGING_DIR" \
    BACKUP_DIR="$BACKUP_DIR" \
    PLAN_PATH="$PLAN_PATH" \
    MARKER_STAMP="$MARKER_STAMP" \
    LOLSTATS_AGG_BIN="${PROBE_BIN:-/tmp/lolstats-aggregate}" \
    SCRATCH_DIRS="$SCRATCH_DIRS" \
    python3 - "$@" <<'PYEOF'
import hashlib, json, os, re, shutil, subprocess, sys, time

AGG = os.environ["AGG_ROOT"]
STAGING = os.environ["STAGING_DIR"]
BACKUP = os.environ["BACKUP_DIR"]
PLAN_PATH = os.environ["PLAN_PATH"]
MARKER = os.environ["MARKER_STAMP"]
BIN = os.environ["LOLSTATS_AGG_BIN"]
SCRATCH = set(os.environ["SCRATCH_DIRS"].split(","))
MANIFEST_REL = "v1/manifest.json"
SOURCE = "riot-match-v5"
MARKER_NANO = None


def log(msg):
    print("   | %s" % msg, flush=True)


def shafile(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def top(name):
    return name.split(os.sep, 1)[0]


def tree_files(root):
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SCRATCH)
        for name in filenames:
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root)
            if top(rel) in SCRATCH:
                continue
            out.append(rel)
    return sorted(out)


def copy_tree(src_root, dst_root, rels, preserve):
    for rel in rels:
        src = os.path.join(src_root, rel)
        dst = os.path.join(dst_root, rel)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        (shutil.copy2 if preserve else shutil.copy)(src, dst)


def mark_length(base, want):
    """Render `base` (RFC3339 with a Z) at exactly `want` characters."""
    core, _, rest = base.partition(".")
    if not rest:
        core, rest = base.rstrip("Z"), "Z"
    frac = ""
    if rest and rest != "Z":
        frac, _, rest = rest.partition("Z")
        rest = "Z"
    tail = rest or "Z"
    fixed = len(core) + 1 + len(tail)
    digits = max(want - fixed, 0)
    if want <= len(core) + len(tail):
        return core + tail
    return "%s.%s%s" % (core, (frac + "0" * digits)[:digits], tail)


def stamp_date(stamp):
    return stamp.split("T", 1)[0]


def sub_once(raw, old, new, label):
    if len(old) != len(new):
        raise SystemExit("%s: replacement is not length-preserving (%d vs %d)"
                         % (label, len(old), len(new)))
    if raw.count(old) != 1:
        raise SystemExit("%s: %r occurs %d times, refusing to guess"
                         % (label, old, raw.count(old)))
    return raw.replace(old, new, 1)


def pick_partition(manifest):
    parts = list(manifest.get("partitions") or [])
    latest = manifest.get("latest")
    if latest and latest.get("patch"):
        return latest
    if not parts:
        raise SystemExit("manifest lists no partitions")
    return max(parts, key=lambda p: (p["patch"], p["region"], p["queue"], p["bracket"]))


def partition_dir(p):
    return "v1/p/%s/%s/%s/%s" % (p["patch"], p["region"], p["queue"], p["bracket"])


STAMP_RE = re.compile(rb'"generated_at"(\s*:\s*")([^"]*)"')


def patch_timestamps(path, marker):
    """Move every generated_at in `path` to `marker`, byte length preserved.

    The replacement is a pure in-place substitution of the timestamp string, so
    the document keeps its formatting, its key order and - deliberately - its
    exact size. What changes on disk is the value and the file's mtime, which is
    what a reader keying on (size, mtime) can see and what a real build leaves
    behind.
    """
    raw = open(path, "rb").read()
    seen = {}

    def repl(match):
        old = match.group(2).decode()
        new = mark_length(marker, len(old))
        seen[old] = new
        return b'"generated_at"' + match.group(1) + new.encode() + b'"'

    out = STAMP_RE.sub(repl, raw)
    if not seen:
        raise SystemExit("%s: no generated_at to move" % path)
    if len(out) != len(raw):
        raise SystemExit("%s: byte length changed (%d -> %d), refusing"
                         % (path, len(raw), len(out)))
    tmp = path + ".marker-incoming"
    with open(tmp, "wb") as fh:
        fh.write(out)
    shutil.copymode(path, tmp)
    os.replace(tmp, path)
    return seen


def run_binary(args):
    log("$ %s" % " ".join(args))
    proc = subprocess.run(args, capture_output=True, text=True)
    for line in (proc.stdout or "").splitlines():
        log("  " + line)
    for line in (proc.stderr or "").splitlines():
        log("! " + line)
    return proc.returncode


def stage():
    if os.path.isdir(STAGING):
        shutil.rmtree(STAGING)
    if os.path.isdir(BACKUP):
        shutil.rmtree(BACKUP)

    live_files = tree_files(AGG)
    os.makedirs(BACKUP, exist_ok=True)
    copy_tree(AGG, BACKUP, live_files, preserve=True)
    log("snapshot of %d live file(s) kept in %s" % (len(live_files), BACKUP))

    manifest_path = os.path.join(AGG, MANIFEST_REL)
    live_manifest = json.load(open(manifest_path))
    if live_manifest.get("source") != SOURCE:
        raise SystemExit("live manifest source is %r, not %r: a reader would not call this a live snapshot"
                         % (live_manifest.get("source"), SOURCE))
    part = partition_dir(pick_partition(live_manifest))
    log("partition the manifest points at: %s" % part)

    copy_tree(AGG, STAGING, [r for r in live_files if top(r) == "v1"], preserve=False)
    log("staged a clone with fresh mtimes, as a build leaves behind")

    for rel in (os.path.join(part, "tierlist.json"), MANIFEST_REL):
        path = os.path.join(STAGING, rel)
        if not os.path.isfile(path):
            raise SystemExit("staged %s is missing" % rel)
        moves = patch_timestamps(path, MARKER)
        log("%s: " % rel + ", ".join("%s -> %s" % kv for kv in sorted(moves.items())))
        staged_size = os.path.getsize(path)
        log("%s: %d bytes after the substitution" % (rel, staged_size))

    before = shafile(os.path.join(STAGING, MANIFEST_REL))
    before_size = os.path.getsize(os.path.join(STAGING, MANIFEST_REL))

    if not os.path.isfile(BIN):
        raise SystemExit("aggregate binary %s is not in the pod" % BIN)
    nano = mark_length(MARKER, 30)
    rc = run_binary([BIN, "manifest", "--agg", STAGING, "--source", SOURCE,
                     "--generated-at", nano])
    if rc != 0:
        raise SystemExit("the project's own manifest writer failed")
    log("asked the writer for generated_at=%s" % nano)

    after_size = os.path.getsize(os.path.join(STAGING, MANIFEST_REL))
    staged_manifest = json.load(open(os.path.join(STAGING, MANIFEST_REL)))
    if after_size != before_size:
        log("note: the manifest writer changed the manifest size %d -> %d" % (before_size, after_size))
    else:
        log("the manifest writer produced the same size (%d bytes)" % after_size)

    plan = {
        "agg_root": AGG,
        "marker": MARKER,
        "partition": part,
        "manifest_before_binary_sha256": before,
        "manifest_before_binary_size": before_size,
        "manifest_staged_sha256": shafile(os.path.join(STAGING, MANIFEST_REL)),
        "manifest_staged_size": after_size,
        "manifest_staged_generated_at": staged_manifest["generated_at"],
        "manifest_staged_latest_generated_at": staged_manifest["latest"]["generated_at"],
        "manifest_staged_partitions": [
            {"patch": p["patch"], "region": p["region"], "queue": p["queue"],
             "bracket": p["bracket"], "generated_at": p["generated_at"]}
            for p in staged_manifest["partitions"]
        ],
        "baseline_files": {rel: {"sha256": shafile(os.path.join(BACKUP, rel)),
                                 "size": os.path.getsize(os.path.join(BACKUP, rel))}
                           for rel in live_files},
        "published": [],
        "published_at": None,
        "restored_at": None,
        "restore_mismatches": [],
    }
    with open(PLAN_PATH, "w") as fh:
        json.dump(plan, fh, indent=2, sort_keys=True)
    log("staged manifest: generated_at=%s latest.generated_at=%s"
        % (staged_manifest["generated_at"], staged_manifest["latest"]["generated_at"]))
    log("plan written to %s (%d baselined files)" % (PLAN_PATH, len(live_files)))
    return 0


def load_plan():
    with open(PLAN_PATH) as fh:
        return json.load(fh)


def save_plan(plan):
    with open(PLAN_PATH, "w") as fh:
        json.dump(plan, fh, indent=2, sort_keys=True)


def trash_name():
    return ".trash-%d-%d" % (os.getpid(), time.time_ns())


def publish():
    """Publish the staged snapshot. This is internal/aggregate/publish.go's
    sequence, reproduced step for step because there is no `publish` subcommand
    to call: Publish is reached from `build`, which needs Postgres, and the only
    other writer of this tree is a CronJob that is not running. The order is the
    part that matters and it is kept exactly: displace the live partition into a
    trash directory beside v1, rename the staged partition in, and rename the
    manifest in last so the landing page never advertises a partition whose files
    are not in place yet.
    """
    plan = load_plan()
    part = plan["partition"]
    staged_dir = os.path.join(STAGING, part)
    live_dir = os.path.join(AGG, part)
    staged_manifest = os.path.join(STAGING, MANIFEST_REL)
    live_manifest = os.path.join(AGG, MANIFEST_REL)

    for path in (staged_dir, staged_manifest):
        if not os.path.exists(path):
            raise SystemExit("staged %s is missing" % path)

    trash = os.path.join(AGG, trash_name())
    os.makedirs(AGG, mode=0o755, exist_ok=True)
    os.makedirs(trash, mode=0o750, exist_ok=True)
    displaced = False
    try:
        if os.path.isdir(live_dir):
            os.rename(live_dir, os.path.join(trash, "old-0"))
            displaced = True
            log("displaced %s -> %s/old-0" % (part, os.path.basename(trash)))
        os.rename(staged_dir, live_dir)
        log("renamed staged %s into place" % part)
        os.rename(staged_manifest, live_manifest)
        log("renamed staged %s into place (manifest last)" % MANIFEST_REL)
    except OSError as exc:
        log("publish failed: %s" % exc)
        if displaced:
            try:
                os.rename(os.path.join(trash, "old-0"), live_dir)
                log("restored the displaced partition")
            except OSError as restore_exc:
                log("could not restore the displaced partition: %s" % restore_exc)
        raise
    finally:
        shutil.rmtree(trash, ignore_errors=True)

    plan["published"] = [part, MANIFEST_REL]
    plan["published_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    plan["live_manifest_sha256_after_publish"] = shafile(live_manifest)
    plan["live_manifest_size_after_publish"] = os.path.getsize(live_manifest)
    save_plan(plan)
    log("published at %s: %s sha256=%s size=%d"
        % (plan["published_at"], MANIFEST_REL, plan["live_manifest_sha256_after_publish"],
           plan["live_manifest_size_after_publish"]))
    return 0


def restore():
    """Put the tree back. Every file is copied back from the snapshot with its
    mode and its mtime, directory mtimes are put back too, and anything the run
    left behind that the snapshot does not have is reported and removed."""
    plan = load_plan()
    baseline = plan["baseline_files"]
    mismatches = []

    live_now = tree_files(AGG)
    for rel in live_now:
        if rel not in baseline:
            log("removing %s: not part of the snapshot" % rel)
            os.remove(os.path.join(AGG, rel))
            mismatches.append("removed stray %s" % rel)

    for rel, want in baseline.items():
        src = os.path.join(BACKUP, rel)
        dst = os.path.join(AGG, rel)
        if not os.path.isfile(src):
            mismatches.append("missing from the snapshot: %s" % rel)
            continue
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        shutil.copy2(src, dst)

    dirs = set()
    for dirpath, dirnames, _ in os.walk(BACKUP):
        for name in dirnames:
            dirs.add(os.path.relpath(os.path.join(dirpath, name), BACKUP))
        if dirpath != BACKUP:
            dirs.add(os.path.relpath(dirpath, BACKUP))
    for rel in sorted(dirs):
        src = os.path.join(BACKUP, rel)
        dst = os.path.join(AGG, rel)
        if os.path.isdir(src) and os.path.isdir(dst):
            stat = os.stat(src)
            os.utime(dst, ns=(stat.st_atime_ns, stat.st_mtime_ns))

    got = tree_files(AGG)
    if sorted(got) != sorted(baseline):
        mismatches.append("the file list differs from the snapshot")
    for rel, want in baseline.items():
        path = os.path.join(AGG, rel)
        if not os.path.isfile(path):
            mismatches.append("not restored: %s" % rel)
            continue
        if shafile(path) != want["sha256"]:
            mismatches.append("sha256 differs: %s" % rel)
        if os.path.getsize(path) != want["size"]:
            mismatches.append("size differs: %s" % rel)

    plan["restored_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    plan["restore_mismatches"] = mismatches
    plan["live_manifest_sha256_after_restore"] = shafile(os.path.join(AGG, MANIFEST_REL))
    save_plan(plan)
    log("restored %d file(s) at %s; manifest sha256=%s"
        % (len(baseline), plan["restored_at"], plan["live_manifest_sha256_after_restore"]))
    for item in mismatches:
        log("MISMATCH %s" % item)
    return 0 if not mismatches else 1


def scratch_clean():
    for path in (STAGING, BACKUP):
        if os.path.isdir(path):
            shutil.rmtree(path)
            log("removed %s" % path)
    for name in sorted(os.listdir(AGG)):
        if name.startswith((".trash-", ".incoming", ".staging-")):
            shutil.rmtree(os.path.join(AGG, name), ignore_errors=True)
            log("removed left-over %s" % name)
    return 0


def residue():
    plan = load_plan()
    parent = os.path.dirname(AGG)
    left = []
    for name in sorted(os.listdir(AGG)):
        if name.startswith((".trash-", ".incoming", ".staging-", ".baseline-")):
            left.append("agg/" + name)
    for entry in sorted(os.listdir(parent)):
        if entry.startswith((".publish-liveness", ".baseline-publish")):
            left.append(entry)
    mismatches = []
    for rel, want in plan["baseline_files"].items():
        path = os.path.join(AGG, rel)
        if not os.path.isfile(path):
            mismatches.append("missing: %s" % rel)
        elif shafile(path) != want["sha256"]:
            mismatches.append("sha256 differs: %s" % rel)
    log("residue: %s" % (", ".join(left) if left else "none"))
    log("content: %s" % (", ".join(mismatches) if mismatches else "every baselined file matches"))
    print(json.dumps({"residue": left, "content_mismatches": mismatches}))
    return 0 if not left and not mismatches else 1


def state():
    plan = load_plan()
    log("plan: partition=%s marker=%s published_at=%s restored_at=%s"
        % (plan["partition"], plan["marker"], plan["published_at"], plan["restored_at"]))
    log("manifest: baseline=%s staged=%s live_now=%s"
        % (plan["baseline_files"][MANIFEST_REL]["sha256"],
           plan["manifest_staged_sha256"], shafile(os.path.join(AGG, MANIFEST_REL))))
    return 0


COMMANDS = {"stage": stage, "publish": publish, "restore": restore,
            "scratch-clean": scratch_clean, "residue": residue, "state": state}

if __name__ == "__main__":
    if len(sys.argv) < 2 or sys.argv[1] not in COMMANDS:
        raise SystemExit("usage: injector {%s}" % "|".join(sorted(COMMANDS)))
    sys.exit(COMMANDS[sys.argv[1]]())
PYEOF
}
# ---------------------------------------------------------------------------
# Reading the record.
# ---------------------------------------------------------------------------
record() { # $1 = phase, $2 = note
  sample "$SVC_URL"
  LAST_HTTP_SHA="$HTTP_SHA"
  LAST_GEN="$MANIFEST_SHA"
  LAST_LATEST="$MANIFEST_LATEST"
  LAST_STATE="$DATA_STATE"
  LAST_RENDERED="$RENDERED"
  LAST_PVC_SHA="$(pvc_sha "$PROBE_AGG_ROOT/$MANIFEST_REL")"
  printf '%-8s %-20s svc-sha=%s gen=%s latest=%s state=%s rendered=%s pvc-sha=%s\n' \
    "$1" "$2" "${LAST_HTTP_SHA:0:12}" "$LAST_GEN" "$LAST_LATEST" "$LAST_STATE" "$LAST_RENDERED" "${LAST_PVC_SHA:0:12}"
}

POLL_SECONDS=""
poll_until_gen() { # $1 = phase, $2 = expected manifest generated_at, $3 = expected latest
  local deadline=$(( $(date +%s) + TIMEOUT_SECONDS )) i=0
  POLL_SECONDS=""
  while [ "$(date +%s)" -lt "$deadline" ]; do
    i=$((i + 1))
    record "$1" "poll+$((i * 5))s"
    if [ "$LAST_GEN" = "$2" ] && [ "$LAST_LATEST" = "$3" ]; then
      POLL_SECONDS="$((i * 5))"
      return 0
    fi
    sleep 5
  done
  return 1
}

drift_window() { # $1 = seconds with no writes at all
  local end=$(( $(date +%s) + $1 )) i=0
  while [ "$(date +%s)" -lt "$end" ]; do
    sleep 5
    i=$((i + 1))
    record "drift" "noswrites+$((i * 5))s"
  done
}

pod_table() {
  k get pods -l "$TIER_SELECTOR" -o json | python3 -c '
import json,sys
d = json.load(sys.stdin)
for p in sorted(d["items"], key=lambda p: p["metadata"]["name"]):
    m = p["metadata"]
    restarts = sum(c.get("restartCount", 0) for c in (p["status"].get("containerStatuses") or []))
    print("\t".join([m["name"], m["uid"], m["creationTimestamp"], str(restarts), p["status"].get("phase", "")]))
'
}

pod_signature() { pod_table | cut -f1,2,3,4; }

pod_names() { pod_table | cut -f1 | tr '\n' ' '; }

pf_start() { # $1 = target, $2 = local port, $3 = remote port
  local log="$WORKDIR/port-forward-$2.log"
  k port-forward "$1" "$2:$3" >"$log" 2>&1 &
  local pid=$!
  PORTFORWARDS="$PORTFORWARDS $pid"
  local i
  for i in $(seq 1 40); do
    curl -fsS -m 3 -o /dev/null "http://127.0.0.1:$2/" 2>/dev/null && { note "port-forward $1 -> 127.0.0.1:$2"; return 0; }
    sleep 0.5
  done
  note "port-forward $1 -> 127.0.0.1:$2 did not come up; see $log"
  return 1
}

# ---------------------------------------------------------------------------
# Preflight. The measurement is only meaningful against the real reader over
# the real mount, so every assumption is checked rather than believed.
# ---------------------------------------------------------------------------
preflight() {
  say "preflight"
  local fixtures
  fixtures="$(k get deploy "$DEPLOY" -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="LOLSTATS_AGG_FIXTURES")].value}')"
  note "deploy/$DEPLOY LOLSTATS_AGG_FIXTURES=${fixtures:-<unset>}"
  [ "$fixtures" = "off" ] || die "the tier is not reading the aggregate root (fixtures=$fixtures); this test would measure fixtures"

  local mounts
  mounts="$(k get deploy "$DEPLOY" -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[?(@.name=="data")].mountPath}/{.spec.template.spec.containers[0].volumeMounts[?(@.name=="data")].readOnly}')"
  note "deploy/$DEPLOY data mount: $mounts"
  k get deploy "$DEPLOY" -o jsonpath='{.spec.template.spec.volumes[?(@.name=="data")].persistentVolumeClaim.claimName}' | grep -qx "$PVC" \
    || die "the tier does not mount $PVC"

  local writers
  writers="$(k get cronjob --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | tr -d ' ' | grep -c 'aggregate' || true)"
  note "cronjobs matching 'aggregate' in $NS: $writers (a scheduled writer, not a running one)"
  k get job --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | tr -d ' ' | grep 'aggregate' | grep . \
    && die "an aggregate job is running; another writer would make this uninterpretable" || true
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------
say "publish-liveness: $NS/$DEPLOY over pvc/$PVC, $( [ "$APPLY" = 1 ] && echo APPLY || echo 'dry run' )"
preflight

say "starting read-only port-forwards"
SVC_URL=""
pf_start "svc/$SVC" 18820 80 || die "cannot reach svc/$SVC"
SVC_URL="http://127.0.0.1:18820"

say "probe pod"
create_probe
pf_start "pod/$(pod_names | awk '{print $1}')" 18821 8080 || note "per-pod forward 1 unavailable"

if [ "$APPLY" = 0 ]; then
  say "dry run: baseline and tier identity only"
  record baseline "before"
  k get deploy "$DEPLOY" -o jsonpath='{.metadata.uid}{" generation="}{.metadata.generation}{"\n"}'
  pod_table
  note "re-run with --apply to run the controls, the publish and the restore"
  exit 0
fi

# ---------------------------------------------------------------------------
say "building the aggregate binary for the nodes (linux/amd64) and handing it to the probe"
mkdir -p "$WORKDIR"
( cd "$(dirname "$0")/.." && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$WORKDIR/lolstats-aggregate" ./cmd/lolstats-aggregate ) \
  || die "cross-compile failed"
note "$(file "$WORKDIR/lolstats-aggregate" 2>/dev/null || echo built) $(wc -c <"$WORKDIR/lolstats-aggregate" | tr -d ' ') bytes"
k cp "$WORKDIR/lolstats-aggregate" "$NS/$PROBE_POD:/tmp/lolstats-aggregate" >/dev/null || die "kubectl cp failed"
pexec chmod +x /tmp/lolstats-aggregate || die "chmod in the probe failed"
pexec /tmp/lolstats-aggregate help >"$WORKDIR/aggregate-help.txt" 2>&1 || true
note "the binary runs in the pod:"
raw "$(head -3 "$WORKDIR/aggregate-help.txt" | tr '\n' ' ')"

pull_plan() { k cp "$NS/$PROBE_POD:$PLAN_PATH" "$WORKDIR/plan.json" >/dev/null 2>&1 || true; }
plan_field() { python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$WORKDIR/plan.json" "$1"; }

# A previous aborted run could have left its own scratch behind. Clearing it
# here (and only it: the prefixes below are this script's own) means the
# "nothing left behind" comparison at the end starts from a clean mount.
say "clearing this script's scratch directories, if an earlier run left any"
injector scratch-clean || die "could not clear stale scratch"

# ---------------------------------------------------------------------------
say "baseline: identity of the pods that are serving, and the exact live bytes"
PODS_BEFORE="$(pod_table)"
printf '%s\n' "$PODS_BEFORE" | while IFS= read -r line; do raw "$line"; done
BASELINE_SHA="$(pvc_sha "$PROBE_AGG_ROOT/$MANIFEST_REL")"
BASELINE_TREE="$(agg_tree_sha "$PROBE_AGG_ROOT" "$SCRATCH_DIRS")"
BASELINE_FILES="$(agg_entries "$PROBE_AGG_ROOT" "$SCRATCH_DIRS" | wc -l | tr -d ' ')"
note "manifest sha256 = $BASELINE_SHA"
note "tree sha256 over $BASELINE_FILES files = $BASELINE_TREE"
BASELINE_LISTING="$(pvc_listing)"
record baseline "before"
BASELINE_GEN="$LAST_GEN"
BASELINE_LATEST="$LAST_LATEST"
BASELINE_RENDERED="$LAST_RENDERED"
BASELINE_STATE="$LAST_STATE"
case "$BASELINE_STATE" in
  live) note "the tier reports data-state=live, so it is reading a riot-match-v5 aggregate root" ;;
  *) die "the tier reports data-state=$BASELINE_STATE; a live snapshot is not being served, so a publish test would prove nothing" ;;
esac

say "staging the injected snapshot (a clone of the live one, marker only)"
injector stage || die "staging failed"
pull_plan
MARKER_IN_DOC="$(plan_field manifest_staged_generated_at)"
MARKER_IN_LATEST="$(plan_field manifest_staged_latest_generated_at)"
note "the snapshot to publish advertises generated_at=$MARKER_IN_DOC, latest.generated_at=$MARKER_IN_LATEST"

# ---------------------------------------------------------------------------
say "POSITIVE CONTROL: publish it and see whether the tier can notice a change at all"
injector publish || die "publish failed"
pull_plan
if poll_until_gen control "$MARKER_IN_DOC" "$MARKER_IN_LATEST"; then
  CONTROL_SECONDS="$POLL_SECONDS"
  note "control PASS: within ${POLL_SECONDS}s of the rename the tier reported generated_at=$MARKER_IN_DOC and rendered '$LAST_RENDERED'"
  CONTROL_PASS=1
else
  note "control FAIL: after ${TIMEOUT_SECONDS}s the tier still reports $LAST_GEN while the bytes on the PVC are $(pvc_sha "$PROBE_AGG_ROOT/$MANIFEST_REL")"
  note "that is the instrument, not the feature: this run cannot judge the rename path"
  CONTROL_PASS=0
fi

say "waiting ${WAIT_SECONDS}s with no writes, to see what moves on its own"
drift_window "$WAIT_SECONDS"
CONTROL_DRIFT="$LAST_GEN|$LAST_LATEST"

say "restoring the original bytes, then watching the tier move back"
injector restore || die "restore failed"
pull_plan
if poll_until_gen reverse "$BASELINE_GEN" "$BASELINE_LATEST"; then
  REVERSE_SECONDS="$POLL_SECONDS"
  note "the tier returned to generated_at=$BASELINE_GEN within ${POLL_SECONDS}s, rendered '$LAST_RENDERED'"
  REVERSE_PASS=1
else
  note "the tier did not return to $BASELINE_GEN within ${TIMEOUT_SECONDS}s"
  REVERSE_PASS=0
fi

# ---------------------------------------------------------------------------
say "REAL TEST: publishes two, with no writes in between, watched only through the readers"
injector stage || die "re-staging failed"
pull_plan
record real-a before
PRE_GEN="$LAST_GEN"
injector publish || die "second publish failed"
pull_plan
PUBLISH_AT="$(plan_field published_at)"
note "published_at=$PUBLISH_AT (no restart, no rollout, no other write followed)"
PODS_AFTER_PUBLISH="$(pod_table)"
if poll_until_gen real-a "$MARKER_IN_DOC" "$MARKER_IN_LATEST"; then
  HOT_SECONDS="$POLL_SECONDS"
  note "the tier served the new snapshot within ${POLL_SECONDS}s, with no restart and no rollout"
  HOT_PASS=1
else
  note "the tier still reports generated_at=$LAST_GEN ${TIMEOUT_SECONDS}s after the rename"
  HOT_PASS=0
fi
record real-a settled

say "change restored a second time, so the tree is left as it was found"
injector restore || die "final restore failed"
pull_plan
record final restore
if poll_until_gen final "$BASELINE_GEN" "$BASELINE_LATEST"; then
  note "the tier is back on generated_at=$BASELINE_GEN, rendered '$LAST_RENDERED'"
else
  note "the tier did not come back to $BASELINE_GEN"
fi

# ---------------------------------------------------------------------------
say "did any pod restart?"
PODS_AFTER="$(pod_table)"
printf '%s\n' "$PODS_AFTER" | while IFS= read -r line; do raw "$line"; done
if [ "$PODS_AFTER" = "$PODS_BEFORE" ]; then
  note "IDENTICAL: same pod names, uids, creationTimestamps and restart counts before and after"
  NO_RESTART=1
else
  note "CHANGED: the tier was restarted, so this run does not prove hot reload"
  diff <(printf '%s\n' "$PODS_BEFORE") <(printf '%s\n' "$PODS_AFTER") | while IFS= read -r line; do raw "$line"; done || true
  NO_RESTART=0
  VOID=1
fi
DEPLOY_AFTER="$(k get deploy "$DEPLOY" -o jsonpath='{.metadata.uid}{" generation="}{.metadata.generation}{"\n"}')"
note "deploy/$DEPLOY $DEPLOY_AFTER"

# ---------------------------------------------------------------------------
say "clearing the scratch the run created, then looking for residue and the sha256 that has to come back"
injector scratch-clean
injector residue | tee "$WORKDIR/residue.txt"
RESIDUE_RC="${PIPESTATUS[0]}"
FINAL_SHA="$(pvc_sha "$PROBE_AGG_ROOT/$MANIFEST_REL")"
FINAL_TREE="$(agg_tree_sha "$PROBE_AGG_ROOT" "$SCRATCH_DIRS")"
FINAL_FILES="$(agg_entries "$PROBE_AGG_ROOT" "$SCRATCH_DIRS" | wc -l | tr -d ' ')"
note "manifest sha256 = $FINAL_SHA (baseline $BASELINE_SHA)"
note "tree sha256 over $FINAL_FILES files = $FINAL_TREE (baseline $BASELINE_TREE)"
if [ "$(pvc_listing)" = "$BASELINE_LISTING" ]; then
  note "the top of the mount and of the aggregate root list exactly what they listed at the start"
else
  note "the top-level listing changed; diff follows"
  diff <(printf '%s\n' "$BASELINE_LISTING") <(pvc_listing) | while IFS= read -r line; do raw "$line"; done || true
fi
RESTORED=1
[ "$FINAL_SHA" = "$BASELINE_SHA" ] || { note "RESTORATION FAILED: the manifest does not match the baseline"; RESTORED=0; }
[ "$FINAL_TREE" = "$BASELINE_TREE" ] || { note "RESTORATION FAILED: the tree does not match the baseline"; RESTORED=0; }
[ "$RESIDUE_RC" = 0 ] || { note "RESIDUE FOUND: see $WORKDIR/residue.txt"; RESTORED=0; }

# ---------------------------------------------------------------------------
say "VERDICT"
cat <<EOF
   control (can the tier notice a change at all)     : $([ "${CONTROL_PASS:-0}" = 1 ] && echo PASS || echo FAIL)
   control held through a ${WAIT_SECONDS}s no-write window        : $([ "${CONTROL_DRIFT:-}" = "$MARKER_IN_DOC|$MARKER_IN_LATEST" ] && echo YES || echo NO)
   the tier moved back when the bytes were restored  : $([ "${REVERSE_PASS:-0}" = 1 ] && echo YES || echo NO)
   new snapshot served after the rename, no restart  : $([ "${HOT_PASS:-0}" = 1 ] && echo YES || echo NO) (visible in ${HOT_SECONDS:-?}s)
   same pod uid/creationTimestamp/restarts throughout: $([ "${NO_RESTART:-0}" = 1 ] && echo YES || echo NO)
   PVC restored, sha256 back to the baseline         : $([ "$RESTORED" = 1 ] && echo YES || echo NO)
   pvc/$PVC left byte-identical                     : $([ "$RESTORED" = 1 ] && echo YES || echo NO)
EOF
rc=0
[ "${CONTROL_PASS:-0}" = 1 ] || rc=3
[ "${HOT_PASS:-0}" = 1 ] || rc=4
[ "${NO_RESTART:-0}" = 1 ] || rc=5
[ "$RESTORED" = 1 ] || rc=6
say "exit $rc"
exit $rc
