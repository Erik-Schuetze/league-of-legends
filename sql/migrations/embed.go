// Package migrations embeds the SQL migrations so the crawler can create its own
// schema.
//
// They are embedded rather than read from disk because the ingest image is
// distroless: there is no shell to run a migration tool in, no package manager
// to install one with, and no guarantee that any directory outside the binary is
// mounted. Embedding also makes the binary and its schema one artifact, which is
// what lets a rollout be a single image tag rather than an image tag and a
// question about who already ran the migration.
//
// The files are numbered `<version>_<name>.up.sql` / `.down.sql`. A migration
// without a `.down.sql` partner still applies; only the reverse step needs one.
package migrations

import "embed"

// FS holds sql/migrations/0001_init.up.sql and every later migration. The `all:`
// prefix is required because the directory contains only .sql files, which are
// excluded by the default embed rules.
//
//go:embed all:*.sql
var FS embed.FS
