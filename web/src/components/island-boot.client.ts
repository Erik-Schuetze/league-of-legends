/*
 * The loader both islands share.
 *
 * It is the only part of an island that runs with the page: it waits until the
 * island is about to become visible and only then imports the runtime. A page
 * whose second island is never reached, or a reader who never scrolls, never
 * pays for it. Nothing here touches the DOM of the island, so an island that
 * never loads is simply the server-rendered table it already was.
 *
 * Nothing here looks an entry point up by name: the call site has to hand the
 * function over in `pick`, so the loader can no longer invent one - the
 * `module.init` that made both islands boot and then do nothing. In a
 * TypeScript module a missing symbol there is a type error. The two call sites
 * are Astro `<script>` blocks, which `tsc` does not read, so
 * `scripts/check-island-contract.mjs` holds them to the same rule.
 */

export type IslandInit = (root: HTMLElement) => void;

/*
 * The roots this loader has already handed to a runtime, so that a second call
 * for the same selector cannot boot an island twice. It lives in memory rather
 * than in `data-island-booted`, because that attribute is the page's record of
 * a boot that *finished*: a root that was only claimed - still waiting for the
 * observer, or for a chunk that never arrived - must not carry it.
 */
const claimed = new WeakSet<HTMLElement>();

export function startIslands<Module>(
  selector: string,
  load: () => Promise<Module>,
  pick: (module: Module) => IslandInit,
): void {
  for (const root of document.querySelectorAll<HTMLElement>(selector)) {
    if (claimed.has(root) || root.dataset.islandReady === 'true') continue;
    claimed.add(root);

    const run = (): void => {
      void load().then((module) => {
        pick(module)(root);
        root.dataset.islandBooted = 'true';
      });
    };

    if (!('IntersectionObserver' in window)) {
      run();
      continue;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          observer.disconnect();
          run();
        }
      },
      { rootMargin: '200px 0px' },
    );
    observer.observe(root);
  }
}
