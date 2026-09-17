#!/usr/bin/env bash
#
# verify-patch-rollover.sh - exercise a whole patch rollover end to end, in a
# namespace of its own, and record the observable signal.
#
# WHAT THIS PROVES (DOD-13 / P6)
#   DOD-13 asks for more than "publish-by-rename is visible" (that is
#   scripts/verify-publish-liveness.sh). It asks that a *patch transition* works
#   as a whole: new payloads arrive, they are ingested, aggregated into agg/v1,
#   published, and the web tier serves the new patch with no restart and no gap
#   - including the edge cases that only appear when the patch really changes:
#
#     1. the transition window: what protects the *set* of files that must agree
#        (manifest, partition directories, tier-list cells)?
#     2. a brand-new partition with no prior data: does the tier render an
#        honest empty state instead of an empty table, a 500, or - worst -
#        the previous patch's numbers under the new patch's label?
#     3. rollback: what is the operator's revert, and is the old patch still
#        served afterwards?
#     4. idempotence: re-running the rollover must not double-count or duplicate
#        a partition.
#
# HOW, AND WHAT IT IS NOT
#   A real patch change is a production event that happens when Riot ships one.
#   This script does NOT wait for that and does NOT claim to have observed one.
#   It builds the transition from the repository's own fixture archive
#   (fixtures/agg/raw/riot/match-v5, 14 matches) plus a derived next-day
#   partition, in a private namespace, on a private RWX volume, using the same
#   binary, the same config keys and the same code path as production. What is
#   real here is the mechanism and its failure modes; what is synthetic is the
#   data and the calendar. docs/PATCH-ROLLOVER-EVIDENCE.md reports it that way.
#
#   The tier is reached over its own ClusterIP Service from inside the
#   namespace. Namespace `web`, the shared Caddy and deploy/base are not touched
#   at all; neither is the live lolstats namespace or its volume.
#
# USAGE
#   scripts/verify-patch-rollover.sh --apply [--ns NAME] [--image REF] [--keep]
#                                     [--soak-seconds N] [--hz N] [--pre-seconds N]
#
#   --apply is required: this script creates a namespace, a PVC, pods and jobs.
#   Every object it creates is deleted on exit (namespace included) unless
#   --keep is given, in which case it prints the exact delete command.
#
# REQUIREMENTS
#   kubectl with a context that can create namespaces and PVs, python3 (>=3.8)
#   on the caller's machine, and bin/duckdb (the pinned v1.4.5 binary) for the
#   parquet conversion step.
#
set -euo pipefail

SCRIPT_NAME="$(basename "$0")"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ART_ROOT="${REPO_ROOT}/.agent-artifacts/rollover"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
EVID="${ART_ROOT}/runs/${RUN_ID}"
YAML_DIR="${EVID}/yaml"

NS="lolstats-rollover"
KEEP=0
SOAK_SECONDS=120
HZ=2

# How long the reader samples the old tree before the build is started. It has
# to be long enough that the "before" side of the transition is measured rather
# than assumed: a handful of ticks on the wrong side of the change proves
# nothing about what the tick before it looked like.
PRE_SECONDS=30
WINDOW_END_A="2026-09-15"
WINDOW_DAYS="6"
NEXT_DAY="2026-09-16"
PATCH_A="16.18"
PATCH_B="16.19"

# The digest the live lolstats-go-web pods run. :latest is pushed by other lanes
# and must never be used here: a moving image would make the measurements
# unreproducible and could pull unrelated changes into the drill.
IMAGE="ghcr.io/erik-schuetze/league-of-legends@sha256:109a30839762f777fbe982813d9e94465bd708b593cdb6fdc1c635c911d3a660"
BUSYBOX="busybox:1.36@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
ROLES="top jungle mid bottom support"

say()  { printf '\n== %s\n' "$*" >&2; }
note() { printf '   %s\n' "$*" >&2; }
die()  { printf '\n!! %s\n' "$*" >&2; exit 1; }
k()    { kubectl -n "$NS" "$@"; }
raw()  { kubectl "$@"; }

usage() {
  sed -n '2,49p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --apply) APPLY=1 ;;
    --ns) NS="${2:?--ns needs a value}"; shift ;;
    --image) IMAGE="${2:?--image needs a value}"; shift ;;
    --keep) KEEP=1 ;;
    --soak-seconds) SOAK_SECONDS="${2:?--soak-seconds needs a value}"; shift ;;
    --hz) HZ="${2:?--hz needs a value}"; shift ;;
    --pre-seconds) PRE_SECONDS="${2:?--pre-seconds needs a value}"; shift ;;
    -h|--help) usage 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
  shift
done
case "${APPLY:-0}" in 1) ;; *) usage 1 ;; esac

# --- guards -----------------------------------------------------------------
# This script is only safe against a namespace it owns. Refuse anything that
# could be serving traffic for real.
case "$NS" in
  web|lolstats|default|kube-system|caddy)
    die "refusing to run against namespace '$NS': it is not a drill namespace" ;;
esac
case "$NS" in
  lolstats-*) ;;
  *) die "namespace must start with 'lolstats-' (got '$NS'), so nothing live can be named this" ;;
esac
case "$IMAGE" in
  *@sha256:*) ;;
  *) die "--image must be a digest-pinned reference, not a tag (got '$IMAGE')" ;;
esac
command -v kubectl >/dev/null || die "kubectl not found"
command -v python3 >/dev/null || die "python3 not found"
for f in fixtures/agg/raw/riot/match-v5 bin/duckdb; do
  [ -e "${REPO_ROOT}/${f}" ] || die "missing ${f}; run from a full checkout of the repository"
done
[ -x "${REPO_ROOT}/bin/duckdb" ] || chmod +x "${REPO_ROOT}/bin/duckdb"

CTX="$(kubectl config current-context)"
say "patch-rollover drill"
note "context      ${CTX}"
note "namespace    ${NS}   (created and deleted by this script)"
note "image        ${IMAGE}"
note "evidence     ${EVID}"
note "window       A=${PATCH_A} window-end ${WINDOW_END_A} days ${WINDOW_DAYS}; B=${PATCH_B} +${NEXT_DAY}"

mkdir -p "$YAML_DIR"

cleanup() {
  local rc=$?
  if [ "$KEEP" = 1 ]; then
    printf '\n-- --keep given. The drill namespace is still there:\n   kubectl delete ns %s\n' "$NS" >&2
  else
    printf '\n-- cleaning up: deleting namespace %s (this removes the PVC and every pod/job in it)\n' "$NS" >&2
    kubectl delete ns "$NS" --ignore-not-found --wait=true >/dev/null 2>&1 || true
    printf '   namespace %s deleted\n' "$NS" >&2
  fi
  return $rc
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---------------------------------------------------------------------------
# The reader: the instrument. It fetches every endpoint the tier exposes for
# this purpose on every tick, reads the tree underneath it fresh (no caching of
# its own), and decides whether what was served agrees with what was on disk.
#
# It is written to a file and shipped into the cluster as a ConfigMap, so the
# soak runs beside the tier rather than through a port-forward: a port-forward
# would put a second moving part between the tier and the measurement.
# ---------------------------------------------------------------------------
cat >"${EVID}/rollover_reader.py" <<'PY'
#!/usr/bin/env python3
"""Fetch, compare against disk, and judge. Two modes:

  sample  loop for --seconds, one record per tick, summary on the last line
  verify  one tick, with hard expectations that must all hold

The instrument has to be able to say "this changed" as well as "this is fine":
`verify` is expected to FAIL while the new patch is unpublished and to PASS
after it flips. A reader that cannot report a difference cannot report a pass.
"""
import argparse
import glob
import hashlib
import html
import json
import os
import re
import statistics
import sys
import time
import urllib.error
import urllib.request

ATTRS = ("champion", "role", "tier", "n", "win_rate", "pick_rate", "ban_rate", "ci95")
ROW_RE = re.compile(r'<tr data-search="[^"]*"(.*?)</tr>', re.S)
BADGE_RE = re.compile(r'data-tier="([^"]*)"')
LABEL_RE = re.compile(r"patch (\d+\.\d+[\.\d]*)")


def emit(obj):
    sys.stdout.write(json.dumps(obj, sort_keys=True) + "\n")
    sys.stdout.flush()


def fetch(url, timeout=10):
    req = urllib.request.Request(url, headers={
        "Cache-Control": "no-cache", "Pragma": "no-cache",
        "User-Agent": "rollover-reader/1",
    })
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.getcode(), resp.read()
    except urllib.error.HTTPError as err:
        return err.code, err.read()
    except Exception as err:
        return 0, ("READER-TRANSPORT-ERROR: %s" % err).encode()


def num(text):
    """Attribute value as a float, so formatting differences are not faults."""
    try:
        return float(text)
    except ValueError:
        return None


def page_rows(html_text):
    """Served rows as tuples, plus the distinct role attribute seen.

    The role is left out of the tuple: it is implied by the route and checked
    separately, so this does not hardcode the tier's internal role enum.
    """
    rows, role_attrs = [], set()
    for chunk in ROW_RE.findall(html_text):
        values = {}
        for name in ATTRS:
            m = re.search(r'data-v-%s="([^"]*)"' % name, chunk)
            values[name] = html.unescape(m.group(1)) if m else ""
        badge = BADGE_RE.search(chunk)
        role_attrs.add(values["role"])
        rows.append((
            values["champion"], int(values["n"] or 0),
            num(values["win_rate"]), num(values["pick_rate"]),
            num(values["ban_rate"]), num(values["ci95"]),
            html.unescape(badge.group(1)).strip() if badge else "",
        ))
    return sorted(rows), role_attrs


def disk_rows(part, names, role):
    with open(part) as handle:
        block = json.load(handle)
    rows = []
    for cell in block["cells"]:
        if str(cell["role"]).lower() != role:
            continue
        rows.append((
            names.get(str(cell["champion_id"]), str(cell["champion_id"])),
            int(cell["n"]), float(cell["win_rate"]),
            float(cell["pick_rate"]), float(cell["ban_rate"]),
            float(cell["ci95_half_width"]), str(cell["tier"]),
        ))
    return sorted(rows)


def find_partition(root, patch):
    hits = sorted(glob.glob(os.path.join(root, "v1", "p", patch, "*", "*", "*", "tierlist.json")))
    return hits[0] if hits else None


def read_manifest(root):
    """The manifest, read fresh every time. Returns (doc, sha, error)."""
    path = os.path.join(root, "v1", "manifest.json")
    try:
        with open(path, "rb") as handle:
            blob = handle.read()
    except FileNotFoundError:
        return None, None, "absent"
    except Exception as err:
        return None, None, "unreadable: %s" % err
    sha = hashlib.sha256(blob).hexdigest()
    try:
        return json.loads(blob), sha, ""
    except Exception as err:
        return None, sha, "torn: %s" % err


def latest_patch(doc):
    """The patch the manifest names as latest.

    The manifest's `latest` field is not a string: it is the whole Partition
    object that is current, exactly as it appears in `partitions`. Only its
    `patch` is the pointer a reader follows, and every comparison in this file
    is against that, so it is extracted in one place.
    """
    latest = (doc or {}).get("latest")
    if isinstance(latest, dict):
        return latest.get("patch") or ""
    return latest or ""


def patch_label(html_text):
    if "Patch not published" in html_text:
        return "not published"
    m = LABEL_RE.search(html_text)
    return m.group(1) if m else "?"


def load_champions(path):
    """champion id -> name, from the dataset the tier's names also come from.

    The file is {"ddragon_version": ..., "champions": [...]}, so a dict is not
    necessarily a map: iterating values() would only ever see the version
    string and the champion list and would silently produce an empty map, which
    would make every served row look wrong.
    """
    with open(path) as handle:
        blob = json.load(handle)
    if isinstance(blob, dict):
        entries = blob.get("champions")
        if not isinstance(entries, list):
            entries = [v for v in blob.values() if isinstance(v, dict)]
    else:
        entries = blob
    out = {}
    for entry in entries:
        if isinstance(entry, dict) and "id" in entry and "name" in entry:
            out[str(entry["id"])] = entry["name"]
    return out


def sha256_bytes(blob):
    return hashlib.sha256(blob).hexdigest()


def tick(base, agg, prev, names, newest, roles):
    """One observation: every endpoint, plus the tree as it is right now.

    Returns a record dict. `faults` is the judgement; nothing else is.
    """
    started = time.time()
    faults = []
    statuses = {}
    rec = {"t": "sample", "at": time.strftime("%Y-%m-%dT%H:%M:%S.", time.gmtime()),
           "epoch": round(started, 3)}

    doc, msha, merr = read_manifest(agg)
    rec["manifest_sha"] = msha
    rec["manifest_error"] = merr
    latest = latest_patch(doc)
    rec["manifest_latest"] = latest
    if merr:
        faults.append("manifest %s" % merr)

    # The exported bytes are the strongest oracle there is: compare the sha256
    # of what the tier served against the sha256 of the file on disk, in both
    # the live tree and the frozen pre-change tree. A sample matching only the
    # frozen tree is stale (the tier is answering yesterday's question);
    # matching neither is incoherent.
    code, body = fetch("%s/explore/export.json" % base)
    statuses["export"] = code
    rec["export_status"] = code
    rec["export_sha"] = sha256_bytes(body) if code == 200 else None
    cur_part = find_partition(agg, latest) if latest else None
    prev_part = find_partition(prev, latest) if (latest and prev) else None
    rec["current_partition_sha"] = sha256_bytes(open(cur_part, "rb").read()) if cur_part else None
    rec["previous_partition_sha"] = sha256_bytes(open(prev_part, "rb").read()) if prev_part else None
    matches_current = bool(cur_part) and rec["export_sha"] == rec["current_partition_sha"]
    matches_previous = bool(prev_part) and rec["export_sha"] == rec["previous_partition_sha"]
    rec["matches_current"] = matches_current
    rec["matches_previous"] = matches_previous
    if code != 200:
        faults.append("export status %s" % code)
    elif not (matches_current or matches_previous):
        faults.append("incoherent: served export matches no tree")

    # Readiness must agree with the tree, not merely return 200.
    code, body = fetch("%s/readyz" % base)
    statuses["readyz"] = code
    rec["readyz_status"] = code
    try:
        rz = json.loads(body)
        rec["readyz_latest"] = rz.get("latest_patch")
        rec["readyz_patches"] = rz.get("patches")
    except Exception:
        rec["readyz_latest"] = None
    if code != 200:
        faults.append("readyz status %s" % code)
    elif rec["readyz_latest"] != latest:
        faults.append("readyz latest %r != manifest latest %r" % (rec["readyz_latest"], latest))

    # Every published route has to render exactly the cells the manifest's
    # patch has on disk. This is what catches the silent wrong answer: the
    # previous patch's numbers under the new patch's label.
    served_patch = None
    for role in roles:
        code, body = fetch("%s/tier-list/%s" % (base, role))
        statuses["tier-list/%s" % role] = code
        if code != 200:
            faults.append("tier-list/%s status %s" % (role, code))
            continue
        text = body.decode("utf-8", "replace")
        rows, _ = page_rows(text)
        label = patch_label(text)
        served_patch = served_patch or label
        if label != latest and label != "not published":
            faults.append("tier-list/%s labelled %s, manifest says %s" % (role, label, latest))
        if cur_part:
            try:
                want = disk_rows(cur_part, names, role)
            except KeyError:
                want = None
            if want is not None and rows != want:
                faults.append("tier-list/%s rows != disk cells for %s" % (role, latest))
    rec["served_patch"] = served_patch

    # The upcoming patch, asked for by name. Before publication this must be an
    # honest empty state; after it must be the new patch's own cells. Anything
    # else - a 500, an empty table, or the previous patch's numbers - is a
    # fault, and this is the only route that can tell those apart.
    patches = set()
    if isinstance(doc, dict):
        for part in (doc.get("partitions") or []):
            if isinstance(part, dict) and part.get("patch"):
                patches.add(str(part["patch"]))
    code, body = fetch("%s/patch/%s/tier-list/top" % (base, newest))
    statuses["upcoming"] = code
    if code != 200:
        faults.append("patch/%s status %s" % (newest, code))
    else:
        text = body.decode("utf-8", "replace")
        urows, _ = page_rows(text)
        rec["upcoming_label"] = patch_label(text)
        rec["upcoming_rows"] = len(urows)
        # Serving the upcoming patch while the manifest points elsewhere is
        # fine as long as the manifest still lists it as a published partition
        # (the manifest is the index; `latest` is only a pointer). Serving it
        # when it is not listed is a silent wrong answer.
        if rec["upcoming_label"] == newest and newest not in patches:
            faults.append("patch/%s served %s cells although the manifest does not list it"
                          % (newest, newest))
        if rec["upcoming_label"] == "not published" and urows:
            faults.append("patch/%s says not published but rendered %d rows"
                          % (newest, len(urows)))

    rec["http_statuses"] = statuses
    rec["faults"] = faults
    rec["sample_seconds"] = round(time.time() - started, 4)
    return rec


def run_sample(args, names, roles):
    deadline = time.time() + args.seconds
    samples, fault_total, fault_kinds = 0, 0, {}
    stale, incoherent, statuses = 0, 0, {}
    served = set()
    first_sha, manifest_flip_at, served_flip_at, ticks = None, None, None, 0
    same_tick = None
    durations = []
    while time.time() < deadline:
        rec = tick(args.base_url, args.agg_root, args.previous_root, names,
                   args.newest, roles)
        samples += 1
        ticks += 1
        durations.append(rec["sample_seconds"])
        if first_sha is None:
            first_sha = rec["manifest_sha"]
        if rec["manifest_sha"] and rec["manifest_sha"] != first_sha and manifest_flip_at is None:
            manifest_flip_at = rec["epoch"]
        if rec["served_patch"] == args.newest and served_flip_at is None:
            served_flip_at = rec["epoch"]
            same_tick = (manifest_flip_at == rec["epoch"])
        if rec["served_patch"]:
            served.add(rec["served_patch"])
        if rec["matches_previous"] and not rec["matches_current"]:
            stale += 1
        if not (rec["matches_current"] or rec["matches_previous"]) and rec["export_status"] == 200:
            incoherent += 1
        for key, value in rec["http_statuses"].items():
            statuses.setdefault(str(value), 0)
            statuses[str(value)] += 1
        for fault in rec["faults"]:
            fault_total += 1
            kind = re.sub(r"\d+(\.\d+)*", "N", fault.split(" ")[0] + " " + " ".join(fault.split()[1:3]))
            fault_kinds[kind] = fault_kinds.get(kind, 0) + 1
        if args.emit_samples:
            emit(rec)
        time.sleep(max(0.0, (1.0 / args.hz) - rec["sample_seconds"]))
    summary = {
        "t": "summary", "samples": samples, "fault_total": fault_total,
        "fault_kinds": fault_kinds, "stale_samples": stale,
        "incoherent_samples": incoherent, "http_statuses": statuses,
        "served_patches": sorted(served), "manifest_first_sha": first_sha,
        "served_flip_epoch": served_flip_at,
        "flip_to_served_seconds": (None if served_flip_at is None or manifest_flip_at is None
                                   else round(max(0.0, served_flip_at - manifest_flip_at), 3)),
        "flip_and_manifest_change_same_tick": same_tick,
        "ticks_observed": ticks,
        "median_sample_seconds": round(statistics.median(durations), 4) if durations else None,
        "max_sample_seconds": max(durations) if durations else None,
    }
    emit(summary)
    return summary


def run_verify(args, names, roles):
    rec = tick(args.base_url, args.agg_root, args.previous_root, names,
               args.newest, roles)
    if args.expect_503:
        # The fail-closed case: the manifest still advertises the patch but the
        # partition is gone. Every route must refuse, and none may fall back to
        # a stale or a wrong answer. The tick's own judgement is not used here
        # (it judges "agrees with disk", and refusing is the point), so the
        # check is spelled out: status 503 and no data rows anywhere.
        faults = []
        probes = ["readyz", "tier-list/top", "patch/%s/tier-list/top" % args.newest,
                  "explore/export.json"]
        statuses = {}
        for path in probes:
            code, body = fetch("%s/%s" % (args.base_url, path))
            statuses[path] = code
            if code != 503:
                faults.append("%s returned %s, expected 503" % (path, code))
            if b"data-search" in body:
                faults.append("%s served table rows while failing closed" % path)
        rec["expect_503_statuses"] = statuses
        rec["verify_faults"] = faults
        emit(rec)
        return {"t": "verify", "mode": "expect_503", "samples": 1,
                "fault_total": len(faults),
                "fault_kinds": {f: 1 for f in faults}, "verify_faults": faults,
                "expect_503_statuses": statuses}
    faults = list(rec["faults"])
    if args.expect_latest and rec["manifest_latest"] != args.expect_latest:
        faults.append("manifest latest %r, expected %r"
                      % (rec["manifest_latest"], args.expect_latest))
    if args.expect_export_sha and rec["export_sha"] != args.expect_export_sha:
        faults.append("export sha %s, expected %s"
                      % (rec["export_sha"], args.expect_export_sha))
    if args.expect_export_sha and rec["current_partition_sha"] != args.expect_export_sha:
        faults.append("disk partition sha %s, expected %s"
                      % (rec["current_partition_sha"], args.expect_export_sha))
    if args.expect_latest:
        for role in roles:
            code, body = fetch("%s/patch/%s/tier-list/%s" % (args.base_url, args.expect_latest, role))
            text = body.decode("utf-8", "replace")
            part = find_partition(args.agg_root, args.expect_latest)
            got, _ = page_rows(text)
            if code != 200:
                faults.append("patch/%s/tier-list/%s status %s" % (args.expect_latest, role, code))
            elif patch_label(text) != args.expect_latest:
                faults.append("patch/%s/tier-list/%s labelled %s"
                              % (args.expect_latest, role, patch_label(text)))
            elif part and got != disk_rows(part, names, role):
                faults.append("patch/%s/tier-list/%s rows != disk cells" % (args.expect_latest, role))
    if args.also:
        part = find_partition(args.agg_root, args.also)
        code, body = fetch("%s/patch/%s/tier-list/top" % (args.base_url, args.also))
        text = body.decode("utf-8", "replace")
        got, _ = page_rows(text)
        if code != 200:
            faults.append("patch/%s status %s" % (args.also, code))
        elif patch_label(text) != args.also:
            faults.append("patch/%s labelled %s (a parallel route must keep its own numbers)"
                          % (args.also, patch_label(text)))
        elif part and got != disk_rows(part, names, "top"):
            faults.append("patch/%s rows != disk cells" % args.also)
    if args.expect_empty:
        code, body = fetch("%s/patch/%s/tier-list/top" % (args.base_url, args.expect_empty))
        text = body.decode("utf-8", "replace")
        got, _ = page_rows(text)
        if code != 200:
            faults.append("unpublished patch/%s status %s (expected an honest empty state)"
                          % (args.expect_empty, code))
        elif patch_label(text) != "not published":
            faults.append("unpublished patch/%s is labelled %s, not 'not published'"
                          % (args.expect_empty, patch_label(text)))
        elif got:
            faults.append("unpublished patch/%s rendered %d rows; an empty state must be empty"
                          % (args.expect_empty, len(got)))
    rec["verify_faults"] = faults
    emit(rec)
    return {"t": "verify", "samples": 1, "fault_total": len(faults),
            "fault_kinds": {f: 1 for f in faults}, "verify_faults": faults,
            "served_patches": [rec["served_patch"]] if rec["served_patch"] else [],
            "manifest_latest": rec["manifest_latest"], "export_sha": rec["export_sha"],
            "upcoming_label": rec.get("upcoming_label")}


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="mode", required=True)
    for name in ("sample", "verify"):
        p = sub.add_parser(name)
        p.add_argument("--base-url", required=True)
        p.add_argument("--agg-root", required=True)
        p.add_argument("--previous-root", default="")
        p.add_argument("--newest", default="")
        p.add_argument("--champions", required=True)
        p.add_argument("--roles", default="top,jungle,mid,bottom,support")
        if name == "sample":
            p.add_argument("--seconds", type=float, default=120)
            p.add_argument("--hz", type=float, default=2)
            p.add_argument("--emit-samples", action="store_true")
        else:
            p.add_argument("--expect-latest", default="")
            p.add_argument("--expect-export-sha", default="")
            p.add_argument("--also", default="")
            p.add_argument("--expect-empty", default="")
            p.add_argument("--expect-503", action="store_true")
    args = ap.parse_args()
    names = load_champions(args.champions)
    roles = [r for r in args.roles.split(",") if r]
    if args.mode == "sample":
        summary = run_sample(args, names, roles)
    else:
        summary = run_verify(args, names, roles)
    sys.exit(1 if summary["fault_total"] else 0)


if __name__ == "__main__":
    main()

PY

# ---------------------------------------------------------------------------
# Fixture pipeline: derive the "next patch" payloads and convert both raw
# archives to the parquet layout and format `lolstats-aggregate build` reads.
# The checked-in fixture is JSONL (readable, diffable); the build reads parquet
# with a single `payload` column, which is what the crawler writes.
# ---------------------------------------------------------------------------
say "materialising the fixture archives"
FIX="${REPO_ROOT}/fixtures/agg/raw/riot/match-v5"
"${REPO_ROOT}/bin/duckdb" --version >/dev/null || die "bin/duckdb is not executable"
DUCK_V="$("${REPO_ROOT}/bin/duckdb" --version)"
case "$DUCK_V" in v1.4.5*) ;; *) die "bin/duckdb is $DUCK_V, not the pinned v1.4.5" ;; esac

python3 - "$REPO_ROOT" "$EVID" <<'PY'
import json
import os
import sys

root, out_dir = sys.argv[1], sys.argv[2]
IN_WINDOW = [1, 2, 3, 4, 5, 6, 7, 8, 13]
ARRIVAL = "2026-09-16"
OLD_VERSION, NEW_VERSION = "16.18.612.9234", "16.19.612.9234"
FLIP_WINS = {1, 2, 3}

raw = os.path.join(root, "fixtures", "agg", "raw", "riot", "match-v5")
by_id = {}
for partition in sorted(os.listdir(raw)):
    with open(os.path.join(raw, partition, "matches.jsonl")) as handle:
        for line in handle:
            if line.strip():
                payload = json.loads(line)
                by_id[payload["metadata"]["matchId"]] = payload

target_dir = os.path.join(out_dir, "jsonl-b", "riot", "match-v5", "dt=" + ARRIVAL)
os.makedirs(target_dir, exist_ok=True)
written = []
with open(os.path.join(target_dir, "matches.jsonl"), "w") as handle:
    for index in IN_WINDOW:
        source = by_id["EUW1_%010d" % index]
        out = json.loads(json.dumps(source))
        info = out["info"]
        if info["gameVersion"] != OLD_VERSION or info["queueId"] != 420 or info["platformId"] != "EUW1":
            raise SystemExit("fixture %s is not the in-window EUW1/420 %s payload"
                             % (source["metadata"]["matchId"], OLD_VERSION))
        # Three mechanical edits and nothing else. The id renumbering keeps the
        # new payloads distinct to the de-duplication key; gameVersion is what
        # makes it a different patch; the win flip is what makes the new
        # patch's *numbers* differ, without which the drill could not tell
        # "served the new patch" from "served the old numbers under a new label".
        out["metadata"]["matchId"] = "EUW1_%010d" % (100 + index)
        info["gameVersion"] = NEW_VERSION
        winner = 200 if index in FLIP_WINS else 100
        for team in info["teams"]:
            team["win"] = team["teamId"] == winner
        for participant in info["participants"]:
            participant["win"] = participant["teamId"] == winner
        handle.write(json.dumps(out, sort_keys=True) + "\n")
        written.append(out["metadata"]["matchId"])
print("derived %d next-patch payloads: %s" % (len(written), " ".join(written)))
PY

convert() {
  "${REPO_ROOT}/bin/duckdb" -batch -init /dev/null <<SQL
COPY (
  SELECT payload
  FROM (SELECT unnest(string_split(decode(content), chr(10))) AS payload
        FROM read_blob('$1'))
  WHERE length(payload) > 0
) TO '$2' (FORMAT PARQUET, COMPRESSION ZSTD);
SQL
}

rm -rf "${EVID}/raw-a" "${EVID}/raw-b"
for partition in "${FIX}"/dt=*/; do
  name="$(basename "$partition")"
  mkdir -p "${EVID}/raw-a/riot/match-v5/${name}"
  convert "${partition}matches.jsonl" "${EVID}/raw-a/riot/match-v5/${name}/part-00001.parquet"
done
mkdir -p "${EVID}/raw-b/dt=${NEXT_DAY}"
convert "${EVID}/jsonl-b/riot/match-v5/dt=${NEXT_DAY}/matches.jsonl" \
        "${EVID}/raw-b/dt=${NEXT_DAY}/part-00001.parquet"
note "raw-a: fixture archive as checked in, $(ls "${EVID}/raw-a/riot/match-v5" | wc -l | tr -d ' ') partitions"
note "raw-b: the new patch's first night, dt=${NEXT_DAY}"

# ---------------------------------------------------------------------------
# Cluster objects. All of them carry an explicit namespace: an object without
# metadata.namespace silently lands in the context's current namespace, which
# is how an earlier attempt put drill pods in `default`.
# ---------------------------------------------------------------------------
say "creating the drill namespace and its volume"
raw apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: ${NS}
  labels:
    app.kubernetes.io/name: lolstats
    app.kubernetes.io/component: rollover-drill
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${NS}-data
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
spec:
  accessModes: [ReadWriteMany]
  storageClassName: nfs-client
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${NS}-config
  namespace: ${NS}
data:
  LOLSTATS_RAW_ROOT: "/var/lib/lolstats/raw"
  LOLSTATS_AGG_ROOT: "/var/lib/lolstats/agg"
  # Never answer from fixtures: this tier must be answerable only out of the
  # tree the drill publishes.
  LOLSTATS_AGG_FIXTURES: "off"
  # The production floor is 100 games, which a 14-match fixture archive can
  # never reach. 2 is the value the repository's own fixture build uses, and it
  # is what makes a suppressed cell and a published cell both exist here.
  LOLSTATS_AGG_MIN_CELL_N: "2"
  LOLSTATS_AGG_MIN_CONFIDENT_SHARE: "0.15"
  LOLSTATS_AGG_MAX_REJECTED_ROWS: "25"
  LOLSTATS_AGG_DUCKDB_MEMORY_LIMIT: "512MiB"
  LOLSTATS_AGG_DUCKDB_THREADS: "2"
  LOLSTATS_AGG_DUCKDB_TEMP_DIR: "/tmp"
  LOLSTATS_AGG_DUCKDB_MAX_TEMP_SIZE: "1GiB"
  LOLSTATS_WEB_ADDR: ":8080"
  LOLSTATS_METRICS_ADDR: ":9091"
  LOLSTATS_FIXTURES_DIR: "/web/src/fixtures"
  LOLSTATS_SITE_URL: "http://127.0.0.1:18080"
---
apiVersion: v1
kind: Pod
metadata:
  name: rollover-seed
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
spec:
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: seed
      image: ${BUSYBOX}
      imagePullPolicy: IfNotPresent
      # The seed doubles as the operator's shell on the volume: the tier image
      # is distroless (kubectl exec cannot even find a shell in it), so every
      # disk-side operation - the fixture copy, the frozen oracle, the
      # out-of-band withdrawal - runs here.
      command: ["sh", "-c", "mkdir -p /var/lib/lolstats/raw/riot/match-v5 && sleep 86400"]
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
      volumeMounts:
        - name: data
          mountPath: /var/lib/lolstats
      resources:
        requests: {cpu: 10m, memory: 16Mi}
        limits: {memory: 128Mi}
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: ${NS}-data
EOF
kubectl -n "$NS" wait --for=condition=Ready pod/rollover-seed --timeout=180s >/dev/null
kubectl -n "$NS" exec rollover-seed -c seed -- sh -c 'grep " /var/lib/lolstats " /proc/mounts || true' > "${EVID}/nfs-mount-options.txt" 2>&1 || true
note "mount options: $(cat "${EVID}/nfs-mount-options.txt")"

exec_seed() { kubectl -n "$NS" exec rollover-seed -c seed -- sh -c "$1"; }
# `kubectl cp` builds the tar with Go's archiver, so it does not add the
# AppleDouble `._*` sidecar files that the system tar on macOS does. Those
# sidecars are fatal here: the build refuses any file in the archive that is
# not a parquet or a zstd frame ("unrecognised file magic"), so a `tar | kubectl
# exec tar` copy has to be disabled with COPYFILE_DISABLE. Using kubectl cp
# avoids the problem at the source.
copy_in() {  # copy_in <local dir> <remote dir>  (copies contents, not the directory)
  kubectl -n "$NS" cp "$1/." "rollover-seed:$2"
}

say "seeding the archive and the new patch's payloads"
exec_seed 'mkdir -p /var/lib/lolstats/raw/riot/match-v5 /var/lib/lolstats/staging /var/lib/lolstats/agg'
copy_in "${EVID}/raw-a/riot/match-v5" /var/lib/lolstats/raw/riot/match-v5
copy_in "${EVID}/raw-b" /var/lib/lolstats/staging
exec_seed 'find /var/lib/lolstats/raw /var/lib/lolstats/staging -type f | sort' | tee "${EVID}/seeded.txt" >&2
STRAY="$(exec_seed 'find /var/lib/lolstats/raw /var/lib/lolstats/staging -name "._*" -o -name ".DS_Store" | wc -l')"
[ "$(printf '%s' "$STRAY" | tr -d ' ')" = "0" ] || die "the archive contains $STRAY macOS sidecar files; the build rejects those"

# ---------------------------------------------------------------------------
# Renderers. `kubectl apply` of a Job whose pod has already completed is a
# no-op that reports Complete immediately and whose `logs` are the *previous*
# run's: a re-run that never happened is indistinguishable from one that did.
# Every runner below therefore deletes the Job first.
# ---------------------------------------------------------------------------
render_build_job() {
  local file="$1" name="$2" window_end="$3" crawl="$4"
  local init=""
  if [ "$crawl" = yes ]; then
    # Idempotent on purpose: the new day's partition is copied in only if it is
    # not already there, which is what makes a re-run of the rollover safe.
    init="      initContainers:
        - name: crawl
          image: ${BUSYBOX}
          imagePullPolicy: IfNotPresent
          command:
            - sh
            - -c
            - |
              set -eu
              if [ -e /var/lib/lolstats/raw/riot/match-v5/dt=${NEXT_DAY} ]; then
                  echo \"crawl: dt=${NEXT_DAY} is already in the archive; nothing to copy\"
                  exit 0
              fi
              cp -a /var/lib/lolstats/staging/dt=${NEXT_DAY} /var/lib/lolstats/raw/riot/match-v5/
              ls -l /var/lib/lolstats/raw/riot/match-v5/dt=${NEXT_DAY}
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: [\"ALL\"]
          resources:
            requests: {cpu: 10m, memory: 16Mi}
            limits: {memory: 128Mi}
          volumeMounts:
            - name: data
              mountPath: /var/lib/lolstats
"
  fi
  cat > "$file" <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${name}
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 900
  template:
    metadata:
      labels:
        app.kubernetes.io/component: rollover-drill
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
${init}      containers:
        - name: aggregate
          image: ${IMAGE}
          imagePullPolicy: IfNotPresent
          command: ["/lolstats-aggregate"]
          args: ["build", "-window-end", "${window_end}", "-window-days", "${WINDOW_DAYS}"]
          envFrom:
            - configMapRef:
                name: ${NS}-config
          env:
            - name: GOMEMLIMIT
              value: 480MiB
          resources:
            requests: {cpu: 100m, memory: 256Mi}
            limits: {cpu: "2", memory: 1Gi}
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /var/lib/lolstats
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: ${NS}-data
        - name: tmp
          emptyDir: {}
EOF
}

render_manifest_job() {
  local file="$1" name="$2" patch="$3"
  cat > "$file" <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${name}
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 300
  template:
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: manifest
          image: ${IMAGE}
          imagePullPolicy: IfNotPresent
          command: ["/lolstats-aggregate"]
          args: ["manifest", "--agg", "/var/lib/lolstats/agg",
                 "--source", "riot-match-v5", "--patch", "${patch}"]
          envFrom:
            - configMapRef:
                name: ${NS}-config
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /var/lib/lolstats
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: ${NS}-data
        - name: tmp
          emptyDir: {}
EOF
}

# The reader runs in its own pod, on its own mount of the same NFS volume. A
# change only one of the two clients can see is exactly what is being measured,
# so the instrument must not share the tier's mount namespace.
render_reader_job() {
  local file="$1" name="$2" seconds="$3"; shift 3
  local args="" a
  for a in "$@"; do
    args="${args}            - \"$(printf '%s' "$a" | sed 's/\\/\\\\/g; s/"/\\"/g')\"
"
  done
  cat > "$file" <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${name}
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
spec:
  backoffLimit: 0
  activeDeadlineSeconds: $((seconds + 120))
  template:
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: reader
          image: python:3.12-alpine
          imagePullPolicy: IfNotPresent
          command: ["python", "/src/rollover_reader.py"]
          args:
${args}          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /var/lib/lolstats
              readOnly: true
            - name: src
              mountPath: /src
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: ${NS}-data
        - name: src
          configMap:
            name: ${NS}-reader
        - name: tmp
          emptyDir: {}
EOF
}

run_job() {  # run_job <file> <job> <timeout>
  local file="$1" job="$2" timeout="${3:-900}"
  kubectl -n "$NS" delete job "$job" --ignore-not-found --wait=true >/dev/null
  local applied deadline succeeded failed
  applied="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  kubectl -n "$NS" apply -f "$file" >/dev/null
  deadline=$(( $(date +%s) + timeout ))
  while :; do
    succeeded="$(kubectl -n "$NS" get job "$job" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
    failed="$(kubectl -n "$NS" get job "$job" -o jsonpath='{.status.failed}' 2>/dev/null || true)"
    # `kubectl wait --for=condition=complete` does not stop for a Job that will
    # never complete: it sits until the timeout, which hides the failure for a
    # quarter of an hour. Poll both terminal conditions instead.
    if [ -n "${failed:-}" ] && [ "$failed" != "0" ]; then
      kubectl -n "$NS" logs "job/$job" --all-containers > "${EVID}/${job}-logs.txt" 2>&1 || true
      kubectl -n "$NS" describe job "$job" > "${EVID}/${job}-describe.txt" 2>&1 || true
      die "job/$job failed (see ${EVID}/${job}-logs.txt)"
    fi
    if [ -n "${succeeded:-}" ] && [ "$succeeded" != "0" ]; then
      break
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      kubectl -n "$NS" logs "job/$job" --all-containers > "${EVID}/${job}-logs.txt" 2>&1 || true
      die "job/$job did not finish within ${timeout}s"
    fi
    sleep 2
  done
  printf '%s applied=%s completed=%s\n' "$job" "$applied" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "${EVID}/${job}-jobtime.txt"
  note "$job applied ${applied}, completed $(date -u +%Y-%m-%dT%H:%M:%SZ)"
}

# ---------------------------------------------------------------------------
# The tier, and the two identities that decide whether the test is valid.
# ---------------------------------------------------------------------------
say "deploying the tier in the drill namespace"
cat > "${YAML_DIR}/web.yaml" <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: rollover-web
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
    app.kubernetes.io/name: rollover-web
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/component: rollover-drill
      app.kubernetes.io/name: rollover-web
  template:
    metadata:
      labels:
        app.kubernetes.io/component: rollover-drill
        app.kubernetes.io/name: rollover-web
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: web
          image: ${IMAGE}
          imagePullPolicy: IfNotPresent
          command: ["/lolstats-web"]
          envFrom:
            - configMapRef:
                name: ${NS}-config
          env:
            - name: GOMEMLIMIT
              value: 224MiB
            - name: LOLSTATS_AGG_FIXTURES
              value: "off"
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet: {path: /readyz, port: http}
            periodSeconds: 2
            failureThreshold: 30
          livenessProbe:
            httpGet: {path: /healthz, port: http}
            periodSeconds: 10
            failureThreshold: 6
          resources:
            requests: {cpu: 50m, memory: 64Mi}
            limits: {cpu: 500m, memory: 256Mi}
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /var/lib/lolstats
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: ${NS}-data
        - name: tmp
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: rollover-web
  namespace: ${NS}
  labels:
    app.kubernetes.io/component: rollover-drill
spec:
  # ClusterIP only: no Ingress, no LoadBalancer, no external name. Nothing on
  # the shared edge or the shared Caddy can see this tier.
  type: ClusterIP
  selector:
    app.kubernetes.io/component: rollover-drill
    app.kubernetes.io/name: rollover-web
  ports:
    - name: http
      port: 8080
      targetPort: http
EOF
kubectl -n "$NS" apply -f "${YAML_DIR}/web.yaml" >/dev/null
kubectl -n "$NS" rollout status deployment/rollover-web --timeout=240s >/dev/null
BASE="http://rollover-web.${NS}.svc.cluster.local:8080"

identities() {  # pod identities: name, uid, creationTimestamp, restartCount
  kubectl -n "$NS" get pods -l app.kubernetes.io/name=rollover-web -o json \
    | python3 -c '
import json, sys
doc = json.load(sys.stdin)
for pod in sorted(doc["items"], key=lambda p: p["metadata"]["name"]):
    m, s = pod["metadata"], pod["status"]
    print("%s uid=%s created=%s restarts=%d" % (
        m["name"], m["uid"], m["creationTimestamp"],
        (s.get("containerStatuses") or [{}])[0].get("restartCount", -1)))
'
}

# The reader's source and the champion-id map it needs to translate the disk
# cells' numeric champion ids into the names the tier renders.
kubectl -n "$NS" create configmap "${NS}-reader" \
  --from-file=rollover_reader.py="${EVID}/rollover_reader.py" \
  --from-file=champions.json="${REPO_ROOT}/internal/webtier/data/champions.json" \
  --dry-run=client -o yaml | kubectl -n "$NS" apply -f - >/dev/null

READER_ARGS=(--agg-root /var/lib/lolstats/agg --champions /src/champions.json --newest "$PATCH_B")

run_reader() {  # run_reader <file> <job> <seconds> [mode and args...]
  local file="$1" job="$2" seconds="$3"; shift 3
  render_reader_job "$file" "$job" "$seconds" "$@" --base-url "$BASE" "${READER_ARGS[@]}"
  run_job "$file" "$job" "$((seconds + 120))"
  kubectl -n "$NS" logs "job/$job" > "${EVID}/${job}.log" 2>&1 || true
  tail -n 1 "${EVID}/${job}.log"
}

expect_no_faults() {  # expect_no_faults <log> <description>
  local log="$1" what="$2"
  python3 - "$log" "$what" <<'PY'
import json, sys
path, what = sys.argv[1], sys.argv[2]
lines = [json.loads(l) for l in open(path) if l.strip().startswith("{")]
summary = lines[-1]
faults = summary.get("fault_total", 0)
if faults:
    print("FAIL %s: %d faults %s" % (what, faults, json.dumps(summary.get("fault_kinds"))), file=sys.stderr)
    sys.exit(1)
print("PASS %s: 0 faults across %d samples" % (what, summary.get("samples", 0)))
PY
}

# Wait for a Job to reach a terminal state, and return 0 for Succeeded, 1 for
# Failed, 2 for timeout. `kubectl wait --for=condition=complete` is not usable
# here: for a Job that will never complete it sits until the timeout, which
# turns a two-second failure into a fifteen-minute hang.
wait_job_terminal() {  # wait_job_terminal <job> <timeout>
  local job="$1" timeout="$2" deadline phase rc=2
  deadline=$(( $(date +%s) + timeout ))
  while :; do
    phase="$(kubectl -n "$NS" get pod -l "job-name=${job}" -o jsonpath='{.items[0].status.phase}' 2>/dev/null || true)"
    case "$phase" in
      Succeeded) rc=0; break ;;
      Failed)    rc=1; break ;;
    esac
    if [ "$(date +%s)" -ge "$deadline" ]; then rc=2; break; fi
    sleep 2
  done
  kubectl -n "$NS" logs "job/$job" > "${EVID}/${job}.log" 2>&1 || true
  return "$rc"
}

# The negative controls: the reader must FAIL when the state is not the one
# being asserted. A reader that cannot report a difference cannot report a pass
# either, which is the whole reason NFS attribute caching has to be ruled out
# rather than assumed away. This returns 0 only if the reader really failed.
reader_fails_as_expected() {  # reader_fails_as_expected <file> <job> <seconds> [args...]
  local file="$1" job="$2" seconds="$3"; shift 3
  render_reader_job "$file" "$job" "$seconds" "$@" --base-url "$BASE" "${READER_ARGS[@]}"
  kubectl -n "$NS" delete job "$job" --ignore-not-found --wait=true >/dev/null
  kubectl -n "$NS" apply -f "$file" >/dev/null
  local rc
  rc=0
  wait_job_terminal "$job" 180 || rc=$?
  [ "$rc" = 1 ] && return 0
  return 1
}

DISK_SHA() { exec_seed "sha256sum /var/lib/lolstats/agg/v1/p/$1/*/*/*/tierlist.json | cut -d' ' -f1"; }
MANIFEST_SHA() { exec_seed "sha256sum /var/lib/lolstats/agg/v1/manifest.json | cut -d' ' -f1"; }
# NB: busybox wget emits no body for a non-2xx, so a fault page captured with this
# helper would be a 0-byte file that reads as missing evidence. Step 9 records the
# status and a note for its 503s instead; the fault prose itself is reproduced
# locally (docs/PATCH-ROLLOVER-EVIDENCE.md 10.3 and 16.2), because no client in
# this drill can capture a 5xx body. A body-capturing client would fix that.
capture_page() { exec_seed "wget -q -O - '${BASE}$1'" > "$2" 2>/dev/null || true; }
status_of() { exec_seed "wget -q -S -O /dev/null '${BASE}$1' 2>&1 | sed -n 's/^ *HTTP\\/[0-9.]* \\([0-9]*\\).*/\\1/p' | head -1"; }
served_rows() { grep -c 'data-search' "$1" || true; }

# ---------------------------------------------------------------------------
# Step 1: publish the pre-rollover patch. Everything the drill asserts is
# relative to this state, so it is verified before anything moves.
# ---------------------------------------------------------------------------
say "step 1 - publish ${PATCH_A} (the pre-rollover patch)"
render_build_job "${YAML_DIR}/job-a.yaml" rollover-agg-a "$WINDOW_END_A" no
run_job "${YAML_DIR}/job-a.yaml" rollover-agg-a 900
kubectl -n "$NS" logs job/rollover-agg-a > "${EVID}/agg-a.log" 2>&1 || true
tee -a "${EVID}/builds.txt" < "${EVID}/agg-a.log" >&2
SHA_A="$(DISK_SHA "$PATCH_A")"
note "${PATCH_A} partition sha256 ${SHA_A}"

run_reader "${YAML_DIR}/verify-baseline.yaml" verify-baseline 20 \
  verify --expect-latest "$PATCH_A" --expect-export-sha "$SHA_A" | tee "${EVID}/verify-baseline.txt"
expect_no_faults "${EVID}/verify-baseline.log" "baseline: ${PATCH_A} served from its own bytes" >&2

# ---------------------------------------------------------------------------
# Step 2: item 2, the honest empty state - and the positive control for the
# instrument itself. NFS attribute caching means "nothing changed" can be the
# instrument lying, so the reader is shown to be able to report a difference
# before its verdict is trusted: it must PASS on the empty state that is really
# there and FAIL on the patch that is not published yet.
# ---------------------------------------------------------------------------
say "step 2 - honest empty state for ${PATCH_B}, and the reader's negative control"
run_reader "${YAML_DIR}/verify-empty-pre.yaml" verify-empty-pre 20 \
  verify --expect-empty "$PATCH_B" --expect-latest "$PATCH_A" 2>&1 | tee "${EVID}/verify-empty-pre.txt"
expect_no_faults "${EVID}/verify-empty-pre.log" "unpublished ${PATCH_B} renders an honest empty state" >&2
capture_page "/patch/${PATCH_B}/tier-list/top" "${EVID}/empty-pre-${PATCH_B}.html"
note "unpublished ${PATCH_B} page: $(served_rows "${EVID}/empty-pre-${PATCH_B}.html") data rows, $(wc -c < "${EVID}/empty-pre-${PATCH_B}.html" | tr -d ' ') bytes"

if reader_fails_as_expected "${YAML_DIR}/control-negative.yaml" control-negative 20 \
     verify --expect-latest "$PATCH_B"; then
  note "negative control: the same reader FAILS on ${PATCH_B} before publication, so a later PASS is a real observation"
  python3 - "${EVID}/control-negative.log" <<'PY'
import json, sys
lines = [json.loads(l) for l in open(sys.argv[1]) if l.strip().startswith("{")]
print("   it failed with: %s" % json.dumps(lines[-1].get("verify_faults")))
PY
else
  die "the reader reported ${PATCH_B} as served while it is unpublished: the instrument cannot detect the difference it is measuring"
fi

# ---------------------------------------------------------------------------
# Step 3: freeze the pre-change tree. Without a frozen copy, "the tier served
# the new bytes" and "the tier served the old bytes" are the same measurement:
# the live tree is overwritten by the transition.
# ---------------------------------------------------------------------------
say "step 3 - freeze the pre-change tree as the staleness oracle"
exec_seed 'rm -rf /var/lib/lolstats/previous && mkdir -p /var/lib/lolstats/previous && cp -a /var/lib/lolstats/agg /var/lib/lolstats/previous/agg'
note "frozen: $(exec_seed 'find /var/lib/lolstats/previous -name tierlist.json | wc -l') tier-list files"
BEFORE_ID="$(identities)"
printf '%s\n' "$BEFORE_ID" > "${EVID}/web-identity-before.txt"
kubectl -n "$NS" get pods -o wide > "${EVID}/pods-before.txt" 2>&1 || true
note "tier pod identity before: $(printf '%s' "$BEFORE_ID")"

# ---------------------------------------------------------------------------
# Step 4: the transition, with the reader already sampling. The reader starts
# first and the build runs *inside* its window, so every tick around the change
# is captured rather than inferred.
# ---------------------------------------------------------------------------
say "step 4 - run the rollover with the reader sampling (${SOAK_SECONDS}s at ${HZ}Hz)"
render_reader_job "${YAML_DIR}/soak-post.yaml" rollover-soak "$SOAK_SECONDS" \
  sample --seconds "$SOAK_SECONDS" --hz "$HZ" --emit-samples \
  --previous-root /var/lib/lolstats/previous/agg \
  --base-url "$BASE" "${READER_ARGS[@]}"
kubectl -n "$NS" delete job rollover-soak --ignore-not-found --wait=true >/dev/null
kubectl -n "$NS" apply -f "${YAML_DIR}/soak-post.yaml" >/dev/null
kubectl -n "$NS" wait --for=condition=Ready pod -l job-name=rollover-soak --timeout=180s >/dev/null
sleep "$PRE_SECONDS"
note "reader has been sampling for ${PRE_SECONDS}s; the archive change happens now"

render_build_job "${YAML_DIR}/job-b.yaml" rollover-agg-b "$NEXT_DAY" yes
run_job "${YAML_DIR}/job-b.yaml" rollover-agg-b 900
kubectl -n "$NS" logs job/rollover-agg-b > "${EVID}/agg-b.log" 2>&1 || true
kubectl -n "$NS" logs job/rollover-agg-b -c crawl > "${EVID}/agg-b-crawl.log" 2>&1 || true
tee -a "${EVID}/builds.txt" < "${EVID}/agg-b.log" >&2

if ! wait_job_terminal rollover-soak $((SOAK_SECONDS + 180)); then
  die "the soak did not finish clean: faults and a failed reader job are the same thing (see ${EVID}/rollover-soak.log)"
fi
cp "${EVID}/rollover-soak.log" "${EVID}/soak-post.log"
kubectl -n "$NS" get pod -l job-name=rollover-soak -o jsonpath='{.items[0].status.containerStatuses[0].imageID}' \
  > "${EVID}/reader-image.txt" 2>&1 || true

say "step 5 - the soak verdict, and the transition window"
python3 - "${EVID}/soak-post.log" "${EVID}/flip-window.txt" "$PATCH_A" "$PATCH_B" <<'PY'
import json, sys
log, out, patch_a, patch_b = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
lines = [json.loads(l) for l in open(log) if l.strip().startswith("{")]
samples, summary = lines[:-1], lines[-1]
first_b = next((i for i, s in enumerate(samples) if s.get("served_patch") == patch_b), None)
report = []
report.append("samples %d  fault_total %d  stale %d  incoherent %d"
              % (summary["samples"], summary["fault_total"],
                 summary["stale_samples"], summary["incoherent_samples"]))
report.append("served patches seen: %s" % summary["served_patches"])
report.append("http statuses: %s" % summary["http_statuses"])
report.append("flip and manifest change in the same tick: %s" % summary["flip_and_manifest_change_same_tick"])
report.append("flip_to_served_seconds %s" % summary["flip_to_served_seconds"])
report.append("median tick %.4fs  max %.4fs" % (summary["median_sample_seconds"], summary["max_sample_seconds"]))
if first_b is not None:
    report.append("samples before the flip: %d  after: %d" % (first_b, len(samples) - first_b))
    report.append("first tick %s  last tick %s" % (samples[0]["at"], samples[-1]["at"]))
report.append("")
if first_b is not None and first_b > 0:
    before, after = samples[first_b - 1], samples[first_b]
    report.append("last tick before the flip:")
    report.append("  %s manifest=%s served=%s export=%s upcoming=%s rows=%s"
                  % (before["at"], before["manifest_latest"], before["served_patch"],
                     before.get("export_sha", "")[:12], before.get("upcoming_label"),
                     before.get("upcoming_rows")))
    report.append("first tick after the flip:")
    report.append("  %s manifest=%s served=%s export=%s upcoming=%s rows=%s"
                  % (after["at"], after["manifest_latest"], after["served_patch"],
                     after.get("export_sha", "")[:12], after.get("upcoming_label"),
                     after.get("upcoming_rows")))
    report.append("ticks between them: 1 (one tick: the change is not sub-tick observable, and no intermediate state exists)")
    report.append("no sample was observed with the manifest changed and the old patch still served"
                  if before["manifest_latest"] == patch_a and after["manifest_latest"] == patch_b
                  else "WARNING: the flip is not a clean old->new transition in adjacent ticks")
else:
    report.append("WARNING: the new patch never appeared in the served set")
open(out, "w").write("\n".join(report) + "\n")
print("\n".join(report))
PY
expect_no_faults "${EVID}/soak-post.log" "rollover soak: every tick coherent, nothing stale" >&2


# ---------------------------------------------------------------------------
# Step 6: item 1, the served answers after the transition - and the check that
# the two patches' numbers are genuinely different, without which "served the
# new patch" and "served the old numbers under a new label" are the same
# measurement.
# ---------------------------------------------------------------------------
say "step 6 - what is served after the transition"
SHA_B="$(DISK_SHA "$PATCH_B")"
SHA_B_AT_TRANSITION="$SHA_B"
SHA_A_AFTER="$(DISK_SHA "$PATCH_A")"
capture_page "/patch/${PATCH_B}/tier-list/top" "${EVID}/served-${PATCH_B}.html"
capture_page "/patch/${PATCH_A}/tier-list/top" "${EVID}/served-${PATCH_A}.html"
capture_page "/explore/export.json" "${EVID}/served-export.json"
note "${PATCH_A} cells sha256 ${SHA_A} (before) / ${SHA_A_AFTER} (after)"
note "${PATCH_B} cells sha256 ${SHA_B}"
[ "$SHA_A" = "$SHA_A_AFTER" ] || die "the ${PATCH_A} partition changed during the transition; the drill is not measuring a rollover"
[ "$SHA_A" != "$SHA_B" ] || die "the two patches have identical cells: this drill cannot tell a new patch from a relabelled old one"

run_reader "${YAML_DIR}/verify-post.yaml" verify-post 20 \
  verify --expect-latest "$PATCH_B" --expect-export-sha "$SHA_B" --also "$PATCH_A" | tee "${EVID}/verify-post.txt"
expect_no_faults "${EVID}/verify-post.log" "post-transition: both patches served from their own bytes" >&2

if reader_fails_as_expected "${YAML_DIR}/control-empty-post.yaml" control-empty-post 20 \
     verify --expect-empty "$PATCH_B"; then
  note "the empty-state check now fails for ${PATCH_B}, as it must once the patch is published"
else
  die "the empty-state check still passes for ${PATCH_B} after publication: it does not discriminate"
fi

# ---------------------------------------------------------------------------
# Step 7: item 4, idempotence. The whole rollover is run again, for real, and
# the tree must be unchanged in content: no second partition, no duplicated
# cells, no leftover staging directory.
# ---------------------------------------------------------------------------
say "step 7 - re-run the rollover (idempotence)"
exec_seed 'cat /var/lib/lolstats/agg/v1/manifest.json' > "${EVID}/manifest-after-b.json"
exec_seed "cat \$(find /var/lib/lolstats/agg/v1/p/${PATCH_B} -name tierlist.json)" > "${EVID}/cells-${PATCH_B}-before-rerun.json"
run_job "${YAML_DIR}/job-b.yaml" rollover-agg-b 900
kubectl -n "$NS" logs job/rollover-agg-b > "${EVID}/agg-b-rerun.log" 2>&1 || true
kubectl -n "$NS" logs job/rollover-agg-b -c crawl > "${EVID}/agg-b-rerun-crawl.log" 2>&1 || true
note "crawl on the re-run: $(tail -n 3 "${EVID}/agg-b-rerun-crawl.log" | head -n 1)"
exec_seed 'cat /var/lib/lolstats/agg/v1/manifest.json' > "${EVID}/manifest-after-rerun.json"
exec_seed "cat \$(find /var/lib/lolstats/agg/v1/p/${PATCH_B} -name tierlist.json)" > "${EVID}/cells-${PATCH_B}-after-rerun.json"
exec_seed 'find /var/lib/lolstats/agg -type d -name ".trash-*" | wc -l' > "${EVID}/trash-dirs-after-rerun.txt"
exec_seed "find /var/lib/lolstats/agg/v1/p -name tierlist.json | sort" > "${EVID}/partitions-after-rerun.txt"
[ "$(DISK_SHA "$PATCH_A")" = "$SHA_A" ] || die "the re-run rewrote ${PATCH_A}; a rollover must not touch the previous patch"
[ "$(tr -d '\n' < "${EVID}/trash-dirs-after-rerun.txt")" = "0" ] || die "the re-run left .trash-* directories behind"

python3 - "${EVID}/manifest-after-b.json" "${EVID}/manifest-after-rerun.json" \
          "${EVID}/cells-${PATCH_B}-before-rerun.json" "${EVID}/cells-${PATCH_B}-after-rerun.json" \
          "${EVID}/idempotence.txt" <<'PY'
import json, sys
m_before, m_after, c_before, c_after, out = sys.argv[1:6]
def keys(path):
    doc = json.load(open(path))
    parts = doc.get("partitions") or []
    latest = doc.get("latest")
    latest = latest.get("patch") if isinstance(latest, dict) else latest
    return ([(p.get("patch"), p.get("region"), p.get("queue"), p.get("bracket")) for p in parts],
            latest)
kb, lb = keys(m_before)
ka, la = keys(m_after)
a = json.load(open(c_before))
b = json.load(open(c_after))
lines = []
lines.append("manifest before re-run: %d partitions, %d distinct keys, latest %s" % (len(kb), len(set(kb)), lb))
lines.append("manifest after  re-run: %d partitions, %d distinct keys, latest %s" % (len(ka), len(set(ka)), la))
lines.append("partition set identical: %s" % (kb == ka))
flip = sorted(k for k in set(a) | set(b) if a.get(k) != b.get(k))
lines.append("top-level keys that differ between the two builds: %s" % flip)
lines.append("cells identical: %s" % (a.get("cells") == b.get("cells")))
lines.append("gate/meta identical: %s" % (a.get("gate") == b.get("gate")))
open(out, "w").write("\n".join(lines) + "\n")
print("\n".join(lines))
if not (kb == ka and len(ka) == len(set(ka)) and a.get("cells") == b.get("cells")):
    sys.exit(1)
PY
note "idempotence: $(tr '\n' '; ' < "${EVID}/idempotence.txt")"

# The re-run rebuilt the partition, so the one field a build cannot pin, its
# own generated_at, moved even though every cell is identical. Anything that
# hashes the partition from here on must hash what is on disk now, or the
# later "did the re-index rewrite it" checks would compare against a stale
# number and fail for the wrong reason.
SHA_B="$(DISK_SHA "$PATCH_B")"
note "${PATCH_B} partition sha256 after the re-run: ${SHA_B}"

# ---------------------------------------------------------------------------
# Step 8: item 3, rollback. There is no revert-the-tree lever; there are two
# operator levers. The first is re-indexing the manifest so `latest` points at
# the older patch, non-destructively, leaving the newer patch reachable by
# name. This is the one that restores service to the previous patch.
# ---------------------------------------------------------------------------
say "step 8 - rollback lever 1: re-index the manifest to ${PATCH_A}"
MANIFEST_BEFORE_INDEX="$(MANIFEST_SHA)"
render_manifest_job "${YAML_DIR}/job-reindex-a.yaml" rollover-reindex-a "$PATCH_A"
run_job "${YAML_DIR}/job-reindex-a.yaml" rollover-reindex-a 300
[ "$(MANIFEST_SHA)" != "$MANIFEST_BEFORE_INDEX" ] || die "the re-index did not rewrite the manifest; it is not a real lever"
kubectl -n "$NS" logs job/rollover-reindex-a > "${EVID}/reindex-${PATCH_A}.log" 2>&1 || true
tee -a "${EVID}/builds.txt" < "${EVID}/reindex-${PATCH_A}.log" >&2
run_reader "${YAML_DIR}/verify-revert.yaml" verify-revert 20 \
  verify --expect-latest "$PATCH_A" --also "$PATCH_B" | tee "${EVID}/verify-revert.txt"
expect_no_faults "${EVID}/verify-revert.log" "re-index to ${PATCH_A}: served from ${PATCH_A}'s bytes, ${PATCH_B} still reachable" >&2
[ "$(DISK_SHA "$PATCH_B")" = "$SHA_B" ] || die "the re-index rewrote ${PATCH_B}: it must be non-destructive"

say "step 8b - point the manifest back at ${PATCH_B}"
render_manifest_job "${YAML_DIR}/job-reindex-b.yaml" rollover-reindex-b "$PATCH_B"
run_job "${YAML_DIR}/job-reindex-b.yaml" rollover-reindex-b 300
kubectl -n "$NS" logs job/rollover-reindex-b > "${EVID}/reindex-${PATCH_B}.log" 2>&1 || true
tee -a "${EVID}/builds.txt" < "${EVID}/reindex-${PATCH_B}.log" >&2
run_reader "${YAML_DIR}/verify-restored.yaml" verify-restored 20 \
  verify --expect-latest "$PATCH_B" --expect-export-sha "$SHA_B" --also "$PATCH_A" | tee "${EVID}/verify-restored.txt"
expect_no_faults "${EVID}/verify-restored.log" "restored: ${PATCH_B} served again from its own bytes" >&2

# ---------------------------------------------------------------------------
# Step 9: the fail-closed measurement. The manifest still advertises the patch
# while the partition is gone - the state a half-finished publication or a bad
# rollback would leave. Every route must refuse rather than serve stale or
# wrong numbers, and the withdrawal is made out of band, on the volume, from
# the seed pod.
# ---------------------------------------------------------------------------
say "step 9 - withdraw the ${PATCH_B} partition out of band and see what the tier does"
PART_DIRS="$(exec_seed "find /var/lib/lolstats/agg/v1/p/${PATCH_B} -mindepth 3 -maxdepth 3 -type d")"
[ -n "$PART_DIRS" ] || die "found no ${PATCH_B} partition directory to withdraw"
note "withdrawing: $(printf '%s' "$PART_DIRS" | tr '\n' ' ')"
exec_seed "rm -rf ${PART_DIRS}"
exec_seed 'cat /var/lib/lolstats/agg/v1/manifest.json' > "${EVID}/manifest-after-withdrawal.json"
for p in readyz tier-list/top "patch/${PATCH_B}/tier-list/top" explore/export.json; do
  name="$(printf '%s' "$p" | tr '/' '_')"
  status="$(status_of "/$p")"
  printf '%s %s\n' "$status" "/$p" >> "${EVID}/withdrawn-statuses.txt"
  if [ "$status" = "200" ]; then
    capture_page "/$p" "${EVID}/withdrawn-${name}.html"
  else
    { printf 'HTTP %s - no body captured.\n' "$status"
      printf 'busybox wget writes no body for a non-2xx; the fault prose is\n'
      printf 'reproduced locally in docs/PATCH-ROLLOVER-EVIDENCE.md 10.3 and 16.2.\n'
    } > "${EVID}/withdrawn-${name}.html"
  fi
done
tee -a "${EVID}/withdrawn-statuses.txt" >&2
run_reader "${YAML_DIR}/verify-503.yaml" verify-503 20 verify --expect-503 || true
python3 - "${EVID}/verify-503.log" <<'PY'
import json, sys
lines = [json.loads(l) for l in open(sys.argv[1]) if l.strip().startswith("{")]
summary = lines[-1]
faults = summary.get("fault_total", 0)
statuses = summary.get("expect_503_statuses", {})
print("fail-closed: %s" % json.dumps(statuses))
if faults:
    print("FAIL: %d routes did not refuse: %s" % (faults, summary.get("verify_faults")), file=sys.stderr)
    sys.exit(1)
PY
note "every route refused with 503 while the manifest still advertised ${PATCH_B}"

say "step 9b - restore by re-running the build, then re-index"
run_job "${YAML_DIR}/job-b.yaml" rollover-agg-b 900
kubectl -n "$NS" logs job/rollover-agg-b > "${EVID}/agg-b-restore.log" 2>&1 || true
render_manifest_job "${YAML_DIR}/job-reindex-b2.yaml" rollover-reindex-b2 "$PATCH_B"
run_job "${YAML_DIR}/job-reindex-b2.yaml" rollover-reindex-b2 300
SHA_B_RESTORED="$(DISK_SHA "$PATCH_B")"
run_reader "${YAML_DIR}/verify-final.yaml" verify-final 20 \
  verify --expect-latest "$PATCH_B" --expect-export-sha "$SHA_B_RESTORED" --also "$PATCH_A" | tee "${EVID}/verify-final.txt"
expect_no_faults "${EVID}/verify-final.log" "final state: served export bytes == bytes on disk" >&2

# ---------------------------------------------------------------------------
# Step 10: the validity of the whole measurement. If the tier's pods changed
# UID or creationTimestamp, or a container restarted, then whatever is serving
# the new patch is not the process that was serving the old one, and this test
# says nothing about hot reload. It is reported, not hidden.
# ---------------------------------------------------------------------------
say "step 10 - prove no restart happened"
AFTER_ID="$(identities)"
printf '%s\n' "$AFTER_ID" > "${EVID}/web-identity-after.txt"
kubectl -n "$NS" get pods -o wide > "${EVID}/pods-after.txt" 2>&1 || true
printf '%s\n' "$BEFORE_ID" > "${EVID}/web-identity-before.txt"
if [ "$BEFORE_ID" != "$AFTER_ID" ]; then
  printf '\n!! tier pod identity changed during the drill:\n   before: %s\n   after:  %s\n' \
    "$BEFORE_ID" "$AFTER_ID" >&2
  die "the tier restarted during the transition; the drill cannot claim hot reload"
fi
note "tier pod identity identical before and after: $(printf '%s' "$AFTER_ID")"

python3 - "${EVID}" "$PATCH_A" "$PATCH_B" "$SHA_A" "$SHA_A_AFTER" "$SHA_B_AT_TRANSITION" "$SHA_B" "$SHA_B_RESTORED" "$BEFORE_ID" <<'PY'
import json, os, sys
evid, pa, pb, sha_a, sha_a2, sha_bt, sha_b, sha_b2, ident = sys.argv[1:10]
lines = [json.loads(l) for l in open(os.path.join(evid, "soak-post.log")) if l.strip().startswith("{")]
summary = lines[-1]
samples = lines[:-1]
first_b = next((i for i, s in enumerate(samples) if s.get("served_patch") == pb), None)
out = []
out.append("PATCH ROLLOVER DRILL - VERDICT")
out.append("")
out.append("This was a FIXTURE-DRIVEN transition in a private namespace (%s), on a" % "lolstats-rollover")
out.append("private NFS volume, with the same binary, config keys and code path as")
out.append("production. No live patch rollover was observed. The fixture archive is")
out.append("14 hand-authored matches; the next patch was derived from it by three")
out.append("mechanical edits (id renumbering, gameVersion, and a win flip in the first")
out.append("three matches so the new patch's numbers genuinely differ).")
out.append("")
out.append("patches: %s (pre-rollover) -> %s" % (pa, pb))
out.append("cells sha256 %s %s" % (pa, sha_a))
out.append("cells sha256 %s %s" % (pb, sha_bt))
out.append("  %s unchanged by the transition: %s" % (pa, sha_a == sha_a2))
out.append("  %s/%s cells differ: %s" % (pa, pb, sha_a != sha_bt))
out.append("")
out.append("transition: %d samples, fault_total %d, stale %d, incoherent %d"
           % (summary["samples"], summary["fault_total"], summary["stale_samples"],
              summary["incoherent_samples"]))
out.append("  http statuses across all routes: %s" % summary["http_statuses"])
out.append("  served patch labels observed: %s" % summary["served_patches"])
out.append("  flip and manifest change in the same tick: %s" % summary["flip_and_manifest_change_same_tick"])
out.append("  flip_to_served_seconds: %s" % summary["flip_to_served_seconds"])
out.append("  median tick %.4fs, max %.4fs" % (summary["median_sample_seconds"], summary["max_sample_seconds"]))
if first_b is not None and first_b > 0:
    b, a = samples[first_b - 1], samples[first_b]
    out.append("  last tick before: manifest=%s served=%s export=%s upcoming=%s rows=%s"
               % (b["manifest_latest"], b["served_patch"], (b.get("export_sha") or "")[:12],
                  b.get("upcoming_label"), b.get("upcoming_rows")))
    out.append("  first tick after: manifest=%s served=%s export=%s upcoming=%s rows=%s"
               % (a["manifest_latest"], a["served_patch"], (a.get("export_sha") or "")[:12],
                  a.get("upcoming_label"), a.get("upcoming_rows")))
out.append("")
out.append("non-restart: tier pod identity before == after (%s)" % ident)
out.append("idempotence:  %s" % open(os.path.join(evid, "idempotence.txt")).read().replace("\n", "; ").strip())
out.append("              (%s partition sha256 after the re-run: %s)" % (pb, sha_b))
out.append("rollback:     re-index lever reverts `latest` non-destructively; both patches stay")
out.append("              reachable; the withdrawn-partition state fails closed with 503 on")
out.append("              every route (see withdrawn-statuses.txt).")
out.append("final state:  served export sha == disk sha after restore (%s)" % sha_b2)
out.append("              (%s was %s at the transition; the two later rebuilds kept every cell" % (pb, sha_bt))
out.append("              identical and only moved generated_at, which a build cannot pin)" % ())
out.append("")
out.append("Evidence: %s" % evid)
text = "\n".join(out) + "\n"
open(os.path.join(evid, "VERDICT.txt"), "w").write(text)
print(text)
PY

say "done - the drill namespace is deleted on exit and the volume goes with it"
note "evidence: ${EVID}"







