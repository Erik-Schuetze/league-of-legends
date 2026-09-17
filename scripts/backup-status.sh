#!/bin/sh
# scripts/backup-status.sh - "is the backup actually working?", answered from
# artifacts instead of from YAML.
#
# The failure this exists for is the one a manifest review cannot see: two
# CronJobs whose YAML is correct, unsuspended and scheduled, with
# `LAST SCHEDULE <none>` - no dump, no snapshot, and nothing anywhere that says
# so. A CronJob that looks right is not a backup; a completed Job with visible
# output is. This script therefore checks the four things that make the
# difference observable:
#
#   1. the CronJobs themselves - are they suspended, and have they ever been
#      scheduled? A schedule that has never fired and is older than one full
#      cycle is a failure, not a "not yet".
#   2. the newest Postgres dump on the volume: does it exist, is it recent, is
#      its manifest beside it, and what does that manifest say the dump holds
#      (the row counts and the schema fingerprint the dump was taken against)?
#   3. the restic repository: does it read, what is the newest snapshot, and how
#      old is it?
#   4. whether the freshness came from a scheduled run or from somebody's
#      `kubectl create job` - printed, because those are different claims.
#
# It reads the volume through a short-lived Job that mounts `lolstats-data`
# read-only and runs as 65532, the uid that owns the artifacts (they are mode
# 0700 by design, so nothing else can read them). Nothing is written to the
# cluster except that Job, which is deleted on the way out.
#
#   sh scripts/backup-status.sh                 # check, exit non-zero if stale
#   sh scripts/backup-status.sh --keep          # leave the Job for inspection
#   sh scripts/backup-status.sh --max-age-hours 50   # tolerate a longer cycle
#
# Exit code 0 means every check passed. Non-zero means at least one thing above
# is missing, unreadable or older than the cycle. Output goes to
# .agent-artifacts/backup-status/ as well as to stdout.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.agent-artifacts/backup-status"
NS=lolstats
NAME=backup-status
MAX_AGE_HOURS=26
KEEP=0

while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1 ;;
    --max-age-hours) shift; MAX_AGE_HOURS="${1:-}" ;;
    --max-age-hours=*) MAX_AGE_HOURS="${1#*=}" ;;
    -h|--help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "backup-status: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

case "$MAX_AGE_HOURS" in
  ''|*[!0-9]*) echo "backup-status: --max-age-hours needs a whole number" >&2; exit 2 ;;
esac

mkdir -p "$WORK"

# The two images come out of the manifests that use them, so this check cannot
# pass against a different digest than the one the cluster runs.
POSTGRES_IMAGE=$(awk '/^ *image: postgres:/{print $2; exit}' "$ROOT/deploy/base/postgres/statefulset.yaml")
RESTIC_IMAGE=$(awk '/^ *image: restic\//{print $2; exit}' "$ROOT/deploy/base/jobs/backup-archive.yaml")
if [ -z "$POSTGRES_IMAGE" ] || [ -z "$RESTIC_IMAGE" ]; then
  echo "backup-status: could not read the pinned images out of the manifests" >&2
  exit 2
fi

# A cutoff in the same shape as the timestamps kubectl prints (RFC3339, UTC, no
# sub-second part), so that "is this older than N hours" is a string comparison
# and no date arithmetic or platform-specific date flag is needed.
cutoff=$(date -u -v-"${MAX_AGE_HOURS}"H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
  || date -u -d "${MAX_AGE_HOURS} hours ago" +%Y-%m-%dT%H:%M:%SZ)

fail=0
note() { printf '%s\n' "$*"; }
bad() { printf 'FAIL %s\n' "$*"; fail=1; }

echo "backup-status: $NS, cycle tolerance ${MAX_AGE_HOURS}h (anything scheduled before $cutoff is stale)"
echo
echo "--- CronJobs (a schedule that has never fired is visible here) ---"
kubectl -n "$NS" get cronjob backup-postgres backup-archive -o custom-columns='NAME:.metadata.name,SCHEDULE:.spec.schedule,TZ:.spec.timeZone,SUSPEND:.spec.suspend,ACTIVE:.status.active[*].name,LAST:.status.lastScheduleTime' 2>/dev/null | sort -u
echo

for cj in backup-postgres backup-archive; do
  if ! kubectl -n "$NS" get cronjob "$cj" >/dev/null 2>&1; then
    bad "$cj: the CronJob does not exist"
    continue
  fi
  created=$(kubectl -n "$NS" get cronjob "$cj" -o jsonpath='{.metadata.creationTimestamp}')
  last=$(kubectl -n "$NS" get cronjob "$cj" -o jsonpath='{.status.lastScheduleTime}')
  if [ -z "$last" ]; then
    # Distinguish "not yet" from "never", which is the whole point: a CronJob
    # created less than one cycle ago has simply not reached its first fire
    # time, and saying otherwise would be a false alarm.
    case "$created" in
      ""|null) bad "$cj: never scheduled, and its creation time is unreadable" ;;
      *)
        if [ "$created" \< "$cutoff" ]; then
          bad "$cj: LAST SCHEDULE is <none> although it was created at $created - it has never fired"
        else
          note "OK   $cj: created $created, has not reached its first fire time yet"
        fi
        ;;
    esac
  else
    if [ "$last" \< "$cutoff" ]; then
      bad "$cj: last scheduled at $last, older than ${MAX_AGE_HOURS}h - the scheduler is not running it"
    else
      note "OK   $cj: last scheduled at $last"
    fi
  fi
done

echo
echo "--- recent Jobs for those CronJobs (scheduled vs manual) ---"
kubectl -n "$NS" get jobs -o custom-columns='NAME:.metadata.name,OWNER:.metadata.ownerReferences[*].name,CREATED:.metadata.creationTimestamp,SUCCEEDED:.status.succeeded,FAILED:.status.failed' 2>/dev/null \
  | grep -E 'NAME|backup-postgres|backup-archive' || note "(none)"
echo
# The read-only reader. Two containers, one per artifact: they fail
# independently and the Job's failure is the sum of them.
#
# The heredoc is quoted, so the `$` in the in-pod scripts stays in the pod where
# it belongs; the four values this script has to substitute are @@...@@ markers
# run through sed instead.
kubectl -n "$NS" delete job "$NAME" --ignore-not-found >/dev/null 2>&1 || true
sed -e "s|@@NAME@@|$NAME|g" \
    -e "s|@@PG_IMAGE@@|$POSTGRES_IMAGE|g" \
    -e "s|@@RESTIC_IMAGE@@|$RESTIC_IMAGE|g" \
    -e "s|@@MAX_AGE_HOURS@@|$MAX_AGE_HOURS|g" \
    -e "s|@@CUTOFF@@|$cutoff|g" <<'MANIFEST' | kubectl -n "$NS" apply -f - >"$WORK/job.create.txt" 2>&1 || {
apiVersion: batch/v1
kind: Job
metadata:
  name: @@NAME@@
  labels:
    app.kubernetes.io/name: lolstats
    app.kubernetes.io/component: backup-status
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 300
  ttlSecondsAfterFinished: 3600
  template:
    metadata:
      labels:
        app.kubernetes.io/name: lolstats
        app.kubernetes.io/component: backup-status
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
        - name: postgres-backups
          image: @@PG_IMAGE@@
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -eu
              root=/live/backups/postgres
              echo "postgres-backups: inspecting $root (read-only mount of lolstats-data)"
              ls -l "$root" 2>&1 || true
              latest="$(cat "$root/LATEST" 2>/dev/null || true)"
              if [ -z "$latest" ]; then echo "FAIL no LATEST marker at $root/LATEST"; exit 1; fi
              dump="$root/daily/$latest"
              if [ ! -f "$dump" ]; then echo "FAIL LATEST names $latest but $root/daily/$latest does not exist"; exit 1; fi
              if [ -z "$(find "$dump" -mmin -$((MAX_AGE_HOURS * 60)))" ]; then
                echo "FAIL the newest dump ($latest) is older than ${MAX_AGE_HOURS}h - the nightly dump is not happening"
                exit 1
              fi
              echo "OK   newest dump: $latest ($(wc -c <"$dump" | tr -d ' ') bytes)"
              echo "OK   sha256: $(sha256sum "$dump" | cut -d' ' -f1)"
              # Parseable *now*, from the stored copy rather than from the run
              # that wrote it: this is the difference between a file that exists
              # and a file that can be restored.
              echo "OK   pg_restore --list reads $(pg_restore --list "$dump" | wc -l | tr -d ' ') TOC entries back out of it"
              if [ -f "$dump.manifest" ]; then
                echo "--- $latest.manifest ---"
                cat "$dump.manifest"
                echo "--- end manifest ---"
              else
                echo "FAIL $latest has no .manifest beside it (the row-count baseline is missing)"
                exit 1
              fi
              echo "OK   retention in force: $(ls -1 "$root/daily" | grep -c '\.dump$') daily, $(ls -1 "$root/weekly" 2>/dev/null | grep -c '\.dump$') weekly dump(s) on the volume"
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /live
              readOnly: true
            - name: tmp
              mountPath: /tmp
          env:
            - name: MAX_AGE_HOURS
              value: "@@MAX_AGE_HOURS@@"
            # `psql` is not needed here, but libpq's defaults are not wanted
            # either: pg_restore --list is a pure file read.
            - name: PGOPTIONS
              value: "-c statement_timeout=0"
        - name: archive-repo
          image: @@RESTIC_IMAGE@@
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -eu
              export HOME=/tmp
              export RESTIC_CACHE_DIR=/tmp/restic-cache
              mkdir -p "$RESTIC_CACHE_DIR"
              if [ -n "${RESTIC_REPOSITORY_OVERRIDE:-}" ]; then export RESTIC_REPOSITORY="$RESTIC_REPOSITORY_OVERRIDE"; fi
              # --no-lock everywhere: this reader is mounted read-only on
              # purpose, and a lock file would make the very first read attempt
              # fail on a read-only filesystem.
              restic() { command restic --no-lock ${RESTIC_EXTRA:-} "$@"; }
              if [ -z "${RESTIC_PASSWORD:-}" ]; then
                echo "FAIL RESTIC_PASSWORD is empty - the lolstats-restic Secret is missing, so no snapshot exists and none can be read"
                exit 1
              fi
              # The single most important line this script prints: whether the
              # snapshot is a copy of the archive or a second name for it. A
              # path is not off-site; anything with a colon is remote.
              case "$RESTIC_REPOSITORY" in
                /*) echo "WARN repository is $RESTIC_REPOSITORY - a path on this cluster's own volume, i.e. NOT an off-site copy (plan risk R6 stays open)" ;;
                *)  echo "OK   repository is remote: $RESTIC_REPOSITORY" ;;
              esac
              if ! restic cat config >/dev/null 2>&1; then
                echo "FAIL no repository can be read at $RESTIC_REPOSITORY"
                exit 1
              fi
              host="${RESTIC_HOST:-lolstats-archive}"
              json="$(restic snapshots --host "$host" --latest 1 --json | tr -d ' \n')"
              case "$json" in
                ''|'[]') echo "FAIL the repository exists but holds no snapshot for host $host"; exit 1 ;;
              esac
              t="$(printf '%s' "$json" | sed -n 's/.*"time":"\([^"]*\)".*/\1/p')"
              # Fixed-width UTC RFC3339 with the fractional part dropped, so
              # "older than" is a string comparison: the image this runs in has
              # no date implementation that parses ISO 8601, and the cutoff is
              # computed once on the host anyway.
              t="${t%%.*}"
              case "$t" in *Z) ;; *) t="${t}Z" ;; esac
              if [ -z "$t" ]; then echo "FAIL could not read a timestamp out of the repository's snapshot list"; exit 1; fi
              echo "OK   newest snapshot taken $t (cycle boundary $CUTOFF)"
              if [ "$t" \< "$CUTOFF" ]; then
                echo "FAIL the newest archive snapshot was taken at $t, before the $CUTOFF boundary - the snapshot is stale"
                exit 1
              fi
              restic snapshots --host "$host" --latest 1
              case "$RESTIC_REPOSITORY" in
                /*) echo "OK   repository size on the volume: $(du -sh "$RESTIC_REPOSITORY" | cut -f1)" ;;
              esac
          envFrom:
            - configMapRef:
                name: lolstats-backup-config
            - secretRef:
                name: lolstats-restic
                optional: true
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
          volumeMounts:
            - name: data
              mountPath: /live
              readOnly: true
            # The repository default is a path on this volume, and the CronJob
            # mounts it at this exact path, so the reader has to see it there
            # too - otherwise "the default destination is readable" would be
            # untested. A remote RESTIC_REPOSITORY_OVERRIDE simply ignores it.
            - name: data
              mountPath: /var/lib/lolstats
              readOnly: true
            - name: tmp
              mountPath: /tmp
            - name: restic-secret
              mountPath: /etc/restic
              readOnly: true
          env:
            - name: MAX_AGE_HOURS
              value: "@@MAX_AGE_HOURS@@"
            - name: CUTOFF
              value: "@@CUTOFF@@"
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: lolstats-data
            readOnly: true
        - name: tmp
          emptyDir: {}
        - name: restic-secret
          secret:
            secretName: lolstats-restic
            optional: true
            defaultMode: 0440
MANIFEST
  echo "backup-status: could not create the reader Job:" >&2
  cat "$WORK/job.create.txt" >&2
  exit 2
}

deadline=$(( $(date +%s) + 420 ))
while :; do
  ok=$(kubectl -n "$NS" get job "$NAME" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)
  no=$(kubectl -n "$NS" get job "$NAME" -o jsonpath='{.status.failed}' 2>/dev/null || true)
  [ "${ok:-0}" = "1" ] && break
  [ "${no:-0}" != "" ] && [ "${no:-0}" -gt 0 ] && break
  if [ "$(date +%s)" -gt "$deadline" ]; then
    bad "the reader Job did not finish within 7 minutes"
    break
  fi
  sleep 5
done

echo "--- artifacts on the volume ---"
kubectl -n "$NS" logs "job/$NAME" --all-containers --prefix >"$WORK/status.log" 2>&1 || true
cat "$WORK/status.log"

# The Job's own container results decide the exit code, so the in-pod checks are
# the ones that fail the script - an unreadable artifact cannot be reported as
# OK by the wrapper.
for c in postgres-backups archive-repo; do
  st=$(kubectl -n "$NS" get job "$NAME" -o jsonpath="{.status.conditions[*].type}" 2>/dev/null || true)
  cs=$(kubectl -n "$NS" get pod -l job-name="$NAME" -o jsonpath="{.items[0].status.containerStatuses[?(@.name=='$c')].state.terminated.exitCode}" 2>/dev/null || true)
  case "$cs" in
    0) note "OK   container $c exited 0" ;;
    ""|null) bad "container $c did not report an exit code ($st)" ;;
    *) bad "container $c exited $cs - see the output above" ;;
  esac
done

if [ "$KEEP" = "1" ]; then
  note "backup-status: Job $NAME left in place (--keep); logs also in $WORK/status.log"
else
  kubectl -n "$NS" delete job "$NAME" --ignore-not-found >/dev/null 2>&1 || true
fi

if [ "$fail" = "0" ]; then
  echo
  echo "backup-status: PASS - a restorable dump and a readable snapshot both exist and are fresh"
else
  echo
  echo "backup-status: FAIL - see the FAIL lines above; docs/runbooks/ has the runbook for each artifact"
fi
exit "$fail"
