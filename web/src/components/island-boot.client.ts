/*
 * The loader both islands share.
 *
 * It is the only part of an island that runs with the page: it waits until the
 * island is about to become visible and only then imports the runtime. A page
 * whose second island is never reached, or a reader who never scrolls, never
 * pays for it. Nothing here touches the DOM of the island, so an island that
 * never loads is simply the server-rendered table it already was.
 */

export interface IslandModule {
  init: (root: HTMLElement) => void;
}

export function startIslands(selector: string, load: () => Promise<IslandModule>): void {
  for (const root of document.querySelectorAll<HTMLElement>(selector)) {
    if (root.dataset.islandBooted === 'true') continue;
    root.dataset.islandBooted = 'true';

    const run = (): void => {
      void load().then((module) => module.init(root));
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
