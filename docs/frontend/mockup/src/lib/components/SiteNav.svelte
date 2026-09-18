<script lang="ts">
  import { page } from "$app/state";

  interface Link {
    href: string;
    label: string;
  }

  const links: Link[] = [
    { href: "/explore", label: "Explore" },
    { href: "/gallery", label: "Gallery" },
    { href: "/about", label: "About" },
    { href: "/disclaimer", label: "Disclaimer" },
  ];

  const isCurrent = (href: string) =>
    href === "/" ? page.url.pathname === "/" : page.url.pathname.startsWith(href);
</script>

<header class="nav">
  <div class="shell bar">
    <a class="brand" href="/">
      <span class="mark" aria-hidden="true">LS</span>
      <span class="wordmark">LoL Stats</span>
    </a>
    <nav aria-label="Main">
      <ul class="links">
        {#each links as link (link.href)}
          <li>
            <a
              href={link.href}
              aria-current={isCurrent(link.href) ? "page" : undefined}
            >{link.label}</a>
          </li>
        {/each}
      </ul>
    </nav>
    <a class="cta" href="/explore">Open explorer</a>
  </div>
</header>

<style>
  .nav {
    position: sticky;
    inset-block-start: 0;
    z-index: 30;
    /* The fade is what lets content scroll under the bar without a hard edge:
       solid cream at the top, sand by the bottom of the bar. */
    background: linear-gradient(
      to bottom,
      var(--c-surface) 0%,
      var(--c-surface) 66%,
      var(--bg-fade-50) 82%,
      var(--bg-fade-0) 100%
    );
    backdrop-filter: blur(1px);
  }

  .bar {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    min-block-size: var(--nav-height);
  }

  .brand {
    display: inline-flex;
    align-items: center;
    gap: var(--space-3);
    text-decoration: none;
    color: var(--c-text-strong);
    margin-inline-end: var(--space-3);
  }

  /* The wordmark is the only place Montserrat appears. */
  .mark {
    display: grid;
    place-items: center;
    inline-size: 2.25rem;
    block-size: 2.25rem;
    background: var(--c-blue);
    color: var(--c-cream);
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
    font-size: var(--step--1);
    letter-spacing: 0.02em;
    box-shadow: var(--ring), 4px 4px 0 var(--c-blue);
  }

  .wordmark {
    font-family: var(--font-brand);
    font-weight: var(--fw-bold);
    font-size: var(--step-1);
    letter-spacing: var(--ls-tight);
  }

  nav {
    flex: 1 1 auto;
    min-inline-size: 0;
  }

  .links {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .links a {
    display: inline-block;
    padding: var(--space-2) 0;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-muted);
    text-decoration: none;
    /* The current page is marked by a hard blue underline, not by a fill. */
    box-shadow: inset 0 -4px 0 transparent;
  }

  .links a:hover {
    color: var(--c-text-strong);
  }

  .links a[aria-current="page"] {
    color: var(--c-text-strong);
    box-shadow: inset 0 -4px 0 var(--c-blue);
  }

  .cta {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    background: var(--c-blue);
    color: var(--c-text-on-accent);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    text-decoration: none;
    box-shadow: 4px 4px 0 var(--c-ink);
  }

  .cta:hover {
    color: var(--c-text-on-accent);
    transform: var(--lift);
    box-shadow: 4px 4px 0 var(--c-ink);
  }

  /* Below 768 the bar becomes two rows: brand and CTA stay, the links scroll
     sideways instead of wrapping into a tall stack. Every row keeps the touch
     floor: a 37px link in a horizontally scrolling strip is a mistap. */
  @media (max-width: 767px) {
    .bar {
      flex-wrap: wrap;
      padding-block: var(--space-2);
      row-gap: var(--space-1);
    }

    .brand {
      order: 1;
      min-block-size: var(--touch-target);
    }

    .cta {
      order: 2;
      margin-inline-start: auto;
    }

    nav {
      order: 3;
      flex-basis: 100%;
      overflow-x: auto;
    }

    .links a {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-block-size: var(--touch-target);
      min-inline-size: var(--touch-target);
      padding-inline: var(--space-1);
    }
  }
</style>
