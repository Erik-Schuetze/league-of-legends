#!/bin/sh
# scripts/offsite-verify.sh - does the raw-archive backup work when the
# destination is NOT a path on this cluster?
#
# The nightly repository default is /var/lib/lolstats/backups/restic, which is a
# directory on the same NFS export as the archive it protects. Plan risk R6
# wants a copy that survives the loss of that export, so the question this
# script answers is narrow and testable:
#
#   "if RESTIC_REPOSITORY pointed at a destination elsewhere, would the backup
#    and the restore actually work, with credentials supplied only as
#    environment variables out of the lolstats-restic Secret?"
#
# It answers it by building a real remote repository and doing a real
# round-trip against it. It never touches the nightly repository or the live
# archive, and it never claims more than it tested:
#
#   sh scripts/offsite-verify.sh --docker    (default) S3-compatible endpoint in
#       Docker on this machine: restic init/backup/check/restore over HTTP with
#       AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, restored tree compared to the
#       source file by file. This is the transport and credential path that
#       Cloudflare R2, Backblaze B2 and any S3 gateway use.
#   sh scripts/offsite-verify.sh --cluster   the real archive, restored into a
#       scratch directory on the cluster volume and compared against the live
#       tree, using the same restic image and the real Secret.
#
# What NONE of this proves: that a copy exists off the premises. There is no
# off-site destination. Until the owner names one and the repository is pointed
# at it, R6 is accepted and unmet, and this script prints exactly that.
# Runbook: docs/runbooks/restore-raw.md.

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.agent-artifacts/offsite-verify"
NS=lolstats
MODE=docker
KEEP=0
KEEP_SCRATCH=0

NET=lolstats-offsite-verify
MINIO=lolstats-offsite-verify-minio
IO=lolstats-offsite-verify-io
VOL_SRC=lolstats-offsite-verify-src
VOL_OUT=lolstats-offsite-verify-out
VOL_MINIO=lolstats-offsite-verify-minio-data
MINIO_IMAGE=quay.io/minio/minio:latest
MC_IMAGE=quay.io/minio/mc:latest
RESTIC_IMAGE=restic/restic:0.19.1
AK=lolstats-offsite-verify
SK="$(printf '%s' 'offsite-verify-local-passphrase-not-a-secret' | shasum -a 256 | cut -c1-40)"
PW="$(printf '%s' 'offsite-verify-local-restic-password' | shasum -a 256 | cut -c1-40)"

while [ $# -gt 0 ]; do
  case "$1" in
    --docker) MODE=docker ;;
    --cluster) MODE=cluster ;;
    --keep) KEEP=1 ;;
    --keep-scratch) KEEP_SCRATCH=1 ;;
    -h|--help) sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "offsite-verify: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

mkdir -p "$WORK"
LOG="$WORK/$MODE.log"

# The body runs inside a `| tee` pipeline, so `exit` in a function would end
# only the subshell and the script would still exit 0 on a failure. A sentinel
# file keeps the exit code honest while the output still streams live.
SENTINEL="$WORK/.failed"
fail() { printf 'FAIL %s\n' "$*"; : >"$SENTINEL"; exit 1; }
note() { printf '%s\n' "$*"; }

# kubectl wait --for=condition=complete waits out its whole timeout on a Job
# that has failed, because the condition it is watching never goes true. This
# returns as soon as either outcome is known: 0 complete, 1 failed, 2 timeout.
wait_job() {
  name="$1"
  limit="$2"
  deadline=$(( $(date +%s) + limit ))
  while :; do
    ok=$(kubectl -n "$NS" get job "$name" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)
    no=$(kubectl -n "$NS" get job "$name" -o jsonpath='{.status.failed}' 2>/dev/null || true)
    [ "${ok:-0}" = "1" ] && return 0
    case "${no:-0}" in ''|0) ;; *) return 1 ;; esac
    [ "$(date +%s)" -gt "$deadline" ] && return 2
    sleep 5
  done
}

# The image the cluster actually runs, read out of the manifest, so this test
# cannot pass against a different restic than the CronJob uses.
PINNED_RESTIC=$(awk '/^ *image: restic\//{print $2; exit}' "$ROOT/deploy/base/jobs/backup-archive.yaml")
[ -n "$PINNED_RESTIC" ] || fail "could not read the pinned restic image out of deploy/base/jobs/backup-archive.yaml"

docker_run_restic() {
  # $1 = the shell script; $2 = extra -v arguments (a word list, on purpose)
  script="$1"
  mount="$2"
  # --user 65532 is the uid the restricted Pod Security profile runs as on the
  # cluster. It is why the source tree below lives in a Docker volume rather
  # than a bind mount: on this Mac a container running as any uid other than
  # the mapped host user gets EIO reading a bind mount, which would test the
  # Mac's file sharing instead of restic.
  docker run --rm --network "$NET" --user 65532 \
    $mount \
    -e RESTIC_REPOSITORY="s3:http://$MINIO:9000/lolstats-archive" \
    -e AWS_ACCESS_KEY_ID="$AK" \
    -e AWS_SECRET_ACCESS_KEY="$SK" \
    -e RESTIC_PASSWORD="$PW" \
    -e HOME=/tmp \
    -e RESTIC_CACHE_DIR=/tmp/restic-cache \
    --entrypoint /bin/sh "$PINNED_RESTIC" -c "$script"
}

cleanup_docker() {
  [ "$KEEP" = "1" ] && { note "offsite-verify: containers and volumes left in place (--keep)"; return; }
  docker rm -f "$MINIO" "$IO" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  docker volume rm -f "$VOL_SRC" "$VOL_OUT" "$VOL_MINIO" >/dev/null 2>&1 || true
}

do_docker() {
  command -v docker >/dev/null 2>&1 || fail "docker is not on PATH; use --cluster or install Docker"
  docker info >/dev/null 2>&1 || fail "the Docker daemon is not reachable"

  note "=== offsite-verify: S3-compatible remote repository, in Docker ==="
  note "restic image: $PINNED_RESTIC"
  note "endpoint:      http://$MINIO:9000/lolstats-archive (a container on its own network: not a local path)"
  note

  # A source tree that is a stand-in for the raw archive. It contains real files
  # out of this repository plus generated ones, so the comparison below is
  # against content that is deterministic and re-runnable.
  rm -rf "$WORK/src" "$WORK/restored"
  mkdir -p "$WORK/src/raw" "$WORK/restored"
  for d in sql docs scripts deploy; do cp -R "$ROOT/$d" "$WORK/src/raw/" 2>/dev/null || true; done
  i=0
  while [ "$i" -lt 64 ]; do
    printf 'lolstats archive fixture %04d\n%s\n' "$i" "$(printf 'payload-%04d-' "$i")" >"$WORK/src/raw/fixture-$(printf '%04d' "$i").txt"
    i=$((i + 1))
  done
  chmod -R a+rX "$WORK/src"

  src_files=$(find "$WORK/src" -type f | wc -l | tr -d ' ')
  src_bytes=$(find "$WORK/src" -type f -exec wc -c {} + | awk '{s+=$1} END {print s+0}')
  note "source tree:   $src_files files, $src_bytes bytes"

  docker network create "$NET" >/dev/null 2>&1 || true
  docker rm -f "$MINIO" "$IO" >/dev/null 2>&1 || true
  docker volume rm -f "$VOL_SRC" "$VOL_OUT" "$VOL_MINIO" >/dev/null 2>&1 || true
  docker volume create "$VOL_SRC" >/dev/null || fail "could not create the source volume"
  docker volume create "$VOL_OUT" >/dev/null || fail "could not create the restore volume"
  docker volume create "$VOL_MINIO" >/dev/null || fail "could not create the object-store volume"
  # io is not started; it exists so that the source tree can be put inside a
  # volume (docker cp works on a stopped container) and the restored tree taken
  # back out again.
  docker create --name "$IO" -v "$VOL_SRC:/src" -v "$VOL_OUT:/out" "$PINNED_RESTIC" true >/dev/null \
    || fail "could not create the helper container"
  docker cp "$WORK/src/." "$IO:/src/" || fail "could not copy the fixture tree into the volume"
  # The restore target has to be writable by the uid the restricted Pod
  # Security profile runs as, so it is chowned to that uid here - as root, by a
  # one-shot container, which is also the only thing in this script that runs as
  # root.
  docker run --rm -v "$VOL_OUT:/out" --entrypoint /bin/sh "$PINNED_RESTIC" \
    -c "chown 65532:65532 /out" >/dev/null || fail "could not prepare the restore volume"

  docker run -d --name "$MINIO" --network "$NET" \
    -e MINIO_ROOT_USER="$AK" -e MINIO_ROOT_PASSWORD="$SK" \
    -v "$VOL_MINIO:/data" "$MINIO_IMAGE" server /data >/dev/null \
    || fail "could not start $MINIO_IMAGE"

  note "waiting for the object store to answer ..."
  up=0
  n=0
  while [ "$n" -lt 60 ]; do
    if docker run --rm --network "$NET" "$MC_IMAGE" \
         alias set probe "http://$MINIO:9000" "$AK" "$SK" >/dev/null 2>&1; then up=1; break; fi
    n=$((n + 1))
    sleep 2
  done
  [ "$up" = "1" ] || { cleanup_docker; fail "the object store never became reachable (60 attempts)"; }

  docker run --rm --network "$NET" "$MC_IMAGE" \
    alias set probe "http://$MINIO:9000" "$AK" "$SK" >/dev/null 2>&1 || true
  docker run --rm --network "$NET" "$MC_IMAGE" \
    mb --ignore-existing "probe/lolstats-archive" >/dev/null 2>&1 \
    || { cleanup_docker; fail "could not create the bucket"; }
  note "bucket created: probe/lolstats-archive"
  note

  note "--- restic init + backup to the remote repository ---"
  docker_run_restic "
    set -eu
    mkdir -p /tmp/restic-cache
    restic init
    restic backup --host offsite-verify --tag offsite-verify /src
    restic snapshots --host offsite-verify --last
  " "-v $VOL_SRC:/src:ro" || { cleanup_docker; fail "the backup to the remote repository failed"; }

  note
  note "--- restic check --read-data-subset=25% (is the remote repository readable?) ---"
  docker_run_restic "
    set -eu
    mkdir -p /tmp/restic-cache
    restic check --read-data-subset=25%
  " "" || { cleanup_docker; fail "restic check failed against the remote repository"; }

  note
  note "--- restore into a scratch directory ---"
  docker_run_restic "
    set -eu
    mkdir -p /tmp/restic-cache
    restic restore latest --host offsite-verify --target /out
    echo 'restored:'
    ls -la /out/src | head -20
  " "-v $VOL_OUT:/out" || { cleanup_docker; fail "the restore from the remote repository failed"; }

  note
  note "--- comparison (every file, sha256, source vs restored) ---"
  docker cp "$IO:/out/." "$WORK/restored/" || { cleanup_docker; fail "could not take the restored tree back out of the volume"; }
  [ -d "$WORK/restored/src" ] || { cleanup_docker; fail "the restore produced no /out/src tree"; }
  ( cd "$WORK/src" && find . -type f | LC_ALL=C sort | while read -r f; do shasum -a 256 "$f"; done ) >"$WORK/manifest.src"
  ( cd "$WORK/restored/src" && find . -type f | LC_ALL=C sort | while read -r f; do shasum -a 256 "$f"; done ) >"$WORK/manifest.restored"
  res_files=$(wc -l <"$WORK/manifest.restored" | tr -d ' ')
  if diff -u "$WORK/manifest.src" "$WORK/manifest.restored" >"$WORK/manifest.diff"; then
    note "PASS identical: $src_files/$src_files files, every sha256 equal"
  else
    note "differences:"
    sed -n '1,40p' "$WORK/manifest.diff"
    cleanup_docker
    fail "the restored tree differs from the source tree ($res_files of $src_files files came back)"
  fi
  if diff -r "$WORK/src" "$WORK/restored/src" >"$WORK/tree.diff" 2>&1; then
    note "PASS diff -r reports no difference at all between source and restored"
  else
    note "diff -r output:"; sed -n '1,40p' "$WORK/tree.diff"
    cleanup_docker
    fail "diff -r found a difference the manifest did not"
  fi

  cleanup_docker
  note
  note "mechanism: PROVEN against a real remote S3-compatible repository."
  note "property:  STILL UNMET. This endpoint is on this machine; no destination"
  note "           outside the premises exists, so plan risk R6 stays accepted"
  note "           and unmet. docs/runbooks/restore-raw.md, 'When off-site media appears'."
}

do_cluster() {
  command -v kubectl >/dev/null 2>&1 || fail "kubectl is not on PATH"
  POSTGRES_IMAGE=$(awk '/^ *image: postgres:/{print $2; exit}' "$ROOT/deploy/base/postgres/statefulset.yaml")
  [ -n "$POSTGRES_IMAGE" ] || fail "could not read the pinned postgres image out of the statefulset"

  note "=== offsite-verify: restore the REAL archive into scratch, on the cluster ==="
  note "A fresh snapshot is taken first, so the comparison below is against a"
  note "known-good point in time rather than against whatever was there earlier."
  note

  kubectl -n "$NS" delete job offsite-verify-snapshot --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NS" create job offsite-verify-snapshot --from=cronjob/backup-archive >/dev/null \
    || fail "could not start the archive snapshot job (see docs/runbooks/restore-raw.md)"
  note "snapshot job started; waiting for it (this is the nightly job's own command) ..."
  wait_job offsite-verify-snapshot 900 \
    || { kubectl -n "$NS" logs job/offsite-verify-snapshot --all-containers || true; fail "the snapshot job did not complete (a snapshot is a prerequisite for a restore)"; }
  kubectl -n "$NS" logs job/offsite-verify-snapshot --all-containers | tail -12

  rm -f "$WORK/cluster-manifest.yaml"
  sed -e "s|@@RESTIC@@|$PINNED_RESTIC|" \
      -e "s|@@POSTGRES@@|$POSTGRES_IMAGE|" \
      -e "s|@@KEEP_SCRATCH@@|$KEEP_SCRATCH|" <<'MANIFEST' >"$WORK/cluster-manifest.yaml"
apiVersion: batch/v1
kind: Job
metadata:
  name: offsite-verify-restore
  labels:
    app.kubernetes.io/name: lolstats
    app.kubernetes.io/component: offsite-verify
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 3600
  ttlSecondsAfterFinished: 3600
  template:
    metadata:
      labels:
        app.kubernetes.io/name: lolstats
        app.kubernetes.io/component: offsite-verify
    spec:
      restartPolicy: Never
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      initContainers:
        - name: restore
          image: @@RESTIC@@
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -eu
              export HOME=/tmp RESTIC_CACHE_DIR=/tmp/restic-cache
              mkdir -p "$RESTIC_CACHE_DIR"
              raw="${LOLSTATS_RAW_ROOT:-/var/lib/lolstats/raw}"
              # The scratch target is inside the volume rather than on the
              # container filesystem, which is read-only by design.
              target=/var/lib/lolstats/scratch/offsite-verify
              mkdir -p "$target"
              echo "restoring the newest snapshot of $raw into $target (the live tree is only read)"
              restic restore latest --host "${RESTIC_HOST:-lolstats-archive}" \
                --target "$target" --include "$raw" --verbose
              echo "restored tree:"
              du -sh "$target$raw"
          envFrom:
            - configMapRef:
                name: lolstats-config
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
              mountPath: /var/lib/lolstats
            - name: tmp
              mountPath: /tmp
      containers:
        - name: compare
          image: @@POSTGRES@@
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -eu
              raw="${LOLSTATS_RAW_ROOT:-/var/lib/lolstats/raw}"
              # $raw is already absolute, so the live path is itself and the
              # restored one is the same path under the scratch target.
              live="$raw"
              back="/var/lib/lolstats/scratch/offsite-verify$raw"
              [ -d "$back" ] || { echo "FAIL no restored tree at $back"; exit 1; }
              n_live=$(find "$live" -type f | wc -l | tr -d ' ')
              n_back=$(find "$back" -type f | wc -l | tr -d ' ')
              b_live=$(find "$live" -type f -exec wc -c {} + | awk '{s+=$1} END {print s+0}')
              b_back=$(find "$back" -type f -exec wc -c {} + | awk '{s+=$1} END {print s+0}')
              echo "live archive:      $n_live files, $b_live bytes"
              echo "restored snapshot: $n_back files, $b_back bytes"
              diff -r "$live" "$back" >/tmp/diff.txt 2>&1 || true
              # Files the ingest worker published *after* the snapshot was taken
              # are legitimately absent from it; anything else is a real
              # difference and fails the run.
              grep -v "^Only in $live" /tmp/diff.txt >/tmp/real.txt || true
              if [ -s /tmp/real.txt ]; then
                echo "FAIL the restored tree differs from the live archive:"
                head -40 /tmp/real.txt
                exit 1
              fi
              later=$(grep -c "^Only in $live" /tmp/diff.txt || true)
              echo "PASS every file in the snapshot came back byte-identical"
              echo "PASS $later file(s) were published after the snapshot and are correctly absent from it"
              if [ "$KEEP_SCRATCH" = "1" ]; then
                echo "scratch kept at $back (--keep-scratch)"
              else
                rm -rf "$back"
                echo "scratch removed from the volume"
              fi
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
          env:
            - name: KEEP_SCRATCH
              value: "@@KEEP_SCRATCH@@"
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: lolstats-data
        - name: tmp
          emptyDir: {}
MANIFEST
  kubectl -n "$NS" delete job offsite-verify-restore --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NS" apply -f "$WORK/cluster-manifest.yaml" >/dev/null || fail "could not create the restore Job"
  note "restore Job created; waiting (the restore itself is minutes, not seconds) ..."
  rc=0
  wait_job offsite-verify-restore 3600 || rc=$?
  [ "$rc" = "2" ] && note "offsite-verify: the restore Job hit the hour-long deadline"
  kubectl -n "$NS" logs job/offsite-verify-restore --all-containers --prefix || true
  if [ "$KEEP" = "1" ]; then
    note "offsite-verify: Jobs left in place (--keep)"
  else
    kubectl -n "$NS" delete job offsite-verify-restore --ignore-not-found >/dev/null 2>&1 || true
    kubectl -n "$NS" delete job offsite-verify-snapshot --ignore-not-found >/dev/null 2>&1 || true
  fi
  [ "$rc" = "0" ] || fail "the restore Job did not complete; see the output above"
  note
  note "mechanism: PROVEN against the real archive and the real snapshot."
  note "property:  STILL UNMET - the snapshot restored from is a path on this"
  note "           cluster's own volume. R6 stays accepted and unmet."
}

rm -f "$SENTINEL"
case "$MODE" in
  docker) do_docker 2>&1 | tee "$LOG" ;;
  cluster) do_cluster 2>&1 | tee "$LOG" ;;
esac

if [ -f "$SENTINEL" ]; then
  rm -f "$SENTINEL"
  echo "offsite-verify: FAILED (see the FAIL line above); full output in $LOG"
  exit 1
fi
echo "offsite-verify: finished; full output in $LOG"
