package aggregate

import "io/fs"

// Permissions of the aggregate tree.
//
// Two kinds of directory live under the aggregate root and they need opposite
// modes, so the modes are named here rather than repeated as literals.
//
// ## The served tree: 0o755 directories, 0o644 files
//
// The published tree is written by the aggregate job as 65532:65532 with fsGroup
// 65532 (deploy/base/jobs/aggregate.yaml). It used to be read by the Go serving
// tier as well, which ran as the image's distroless nonroot uid - the same 65532
// (deploy/base/web/go-deployment.yaml) - and that Deployment was deleted on
// 2026-09-18, so the writer is the only reader left in the tree.
//
// The modes below predate that and are deliberately left alone in this pass.
// Until 2026-09-17 the tree was also read by two processes that were *not* the
// writer's uid:
//
//   - the site-build CronJob, which ran as 1000:1000 with fsGroup 1000
//     (deploy/base/jobs/site-build.yaml) - the `node` user of node:22-alpine,
//     which prerendered the aggregate over HTTP content into the static site;
//   - the inner Caddy, which served /var/lib/lolstats/agg directly with
//     `file_server` as 1000:1000 (deploy/base/web/deployment.yaml and
//     caddyfile.yaml).
//
// Both were deleted with the static tier and no uid 1000 reader
// remains, so 0o750/0o640 would now be enough on a fresh volume. The values stay
// because the mode is a property of the bytes already on the volume as much as of
// the code that wrote them: agg/ carries 0755/0644 today, tightening it means
// rewriting or chmod-sweeping the tree on the PVC, and a partial sweep would
// leave the two modes mixed. That change is its own decision with its own
// verification, not a comment edit inside a deletion pass.
//
// The group could never bridge the gap. The volume is the nfs-client StorageClass
// (deploy/overlays/homelab), and the kubelet cannot chown an NFS export, so
// fsGroup is not honoured on it: the group on disk stays whatever the writer
// left, and the two workloads did not share a group to begin with. This is not
// theoretical - it is why the pre-existing persistent volumes on that
// provisioner carry mode 0777.
//
// A mode of 0o750 therefore published artifacts that uid 1000 could see the
// directory of but not enter: the aggregate build succeeded, the nightly
// site-build job failed with EACCES, and the failure looked like a bug in the
// site build rather than in these modes. The tree is public web content that
// anonymous readers fetch over HTTPS and it holds no secret, so the modes that
// always work are the ones with the other bits set - traversable directories
// and readable files, nothing writable by anyone but the owner. That is a
// deliberate decision, not a default, and it is the reason these sites carry a
// gosec exception in the source.
//
// ## Everything else: 0o750 directories, 0o640 files
//
// The staging tree, the trash directory that holds displaced artifacts, the
// decompressed raw-archive scratch, and the file-audit breadcrumbs are read by
// this process alone: none of them is under the published path, and no other uid
// has any business in them. They get the tightest mode that still lets
// the owner work, which is also what the linter prefers.
const (
	// publishedDirPerm is the mode of every directory on a served path.
	publishedDirPerm fs.FileMode = 0o755

	// publishedFilePerm is the mode of every served artifact.
	publishedFilePerm fs.FileMode = 0o644

	// privateDirPerm is the mode of a directory this process owns alone.
	privateDirPerm fs.FileMode = 0o750

	// privateFilePerm is the mode of a file this process owns alone.
	privateFilePerm fs.FileMode = 0o640
)
