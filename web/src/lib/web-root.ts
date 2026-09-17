import { existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// Where web/ is, on disk, while the build runs.
//
// Astro bundles this library into prerender chunks under web/dist, so
// `import.meta.url` on its own resolves inside the build output rather than the
// source tree. `process.cwd()` is web/ for `npm run build`, for
// `npm --prefix web run build` and for the Makefile target, so the search
// starts there and only then falls back to this module's own location.
//
// Every checked-in file the build reads (src/data, src/fixtures, src/types) is
// located through this constant, so there is exactly one answer to "where is
// the source tree" per build.

function findWebRoot(): string {
  const starts = [process.cwd(), fileURLToPath(new URL('.', import.meta.url))];

  for (const start of starts) {
    let dir = resolve(start);
    for (let depth = 0; depth < 8; depth += 1) {
      if (existsSync(join(dir, 'astro.config.mjs'))) return dir;
      const parent = resolve(dir, '..');
      if (parent === dir) break;
      dir = parent;
    }
  }

  // Nothing matched, so the caller's own relative paths are the best available
  // guess and the resulting "file not found" error names a real path.
  return process.cwd();
}

export const WEB_ROOT = findWebRoot();
