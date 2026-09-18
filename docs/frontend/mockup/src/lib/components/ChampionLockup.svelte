<script lang="ts">
  interface Props {
    name: string;
    slug: string;
    /** Data Dragon champion id. Shown because it is the join key. */
    id?: number;
    href?: string;
    size?: "sm" | "md" | "lg";
    /** No icon: every champion is drawn as a monogram tile. `false` dims the
     *  lockup for champions with no published cell in the current slice. */
    published?: boolean;
  }

  let { name, slug, id = undefined, href = undefined, size = "md", published = true }: Props = $props();

  /* Two initials at most, so "Nunu & Willump" reads NU and "Dr. Mundo" DM. */
  const monogram = $derived(
    name
      .split(/[\s'’&.-]+/)
      .filter((part) => /[a-z]/i.test(part))
      .slice(0, 2)
      .map((part) => part[0]!.toUpperCase())
      .join(""),
  );

  const label = $derived(published ? name : `${name} - no published data in this slice`);
</script>

{#if href && published}
  <a class="lockup" data-size={size} {href}>
    <span class="mono" aria-hidden="true">{monogram}</span>
    <span class="text">
      <span class="name">{name}</span>
      <span class="slug num">{slug}</span>
    </span>
  </a>
{:else}
  <span class="lockup" data-size={size} data-published={published} title={label}>
    <span class="mono" aria-hidden="true">{monogram}</span>
    <span class="text">
      <span class="name">{name}</span>
      <span class="slug num">{slug}{#if id !== undefined}<span class="id"> · id {id}</span>{/if}</span>
    </span>
    {#if !published}
      <span class="visually-hidden"> - no published data in this slice</span>
    {/if}
  </span>
{/if}

<style>
  .lockup {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    text-decoration: none;
    color: inherit;
    line-height: var(--lh-snug);
  }

  a.lockup:hover .name {
    color: var(--c-text-accent-strong);
    text-decoration: underline;
    text-underline-offset: 0.18em;
  }

  .mono {
    display: grid;
    place-items: center;
    inline-size: var(--mark);
    block-size: var(--mark);
    background: var(--c-surface);
    box-shadow: var(--ring);
    font-family: var(--font-mono);
    font-size: calc(var(--mark) * 0.42);
    font-weight: var(--fw-bold);
    color: var(--c-text-accent-strong);
    flex: none;
  }

  .lockup[data-size="sm"] { --mark: 1.75rem; }
  .lockup[data-size="md"] { --mark: 2.25rem; }
  .lockup[data-size="lg"] { --mark: 3.25rem; }

  /* A champion link in a table cell is a target: on touch it takes the floor
     even when the mark inside it is small. */
  @media (max-width: 767px) {
    a.lockup {
      min-block-size: var(--touch-target);
    }
  }

  .text {
    display: grid;
  }

  .name {
    font-family: var(--font-heading);
    font-weight: var(--fw-bold);
    font-size: var(--step--1);
    color: var(--c-text-strong);
  }

  .slug {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  /* A champion with nothing published is dimmed but still listed: absence of
     data is information, so it is never hidden. */
  .lockup[data-published="false"] .mono {
    box-shadow: inset 0 0 0 var(--ring-width) var(--c-rule);
    color: var(--c-text-disabled);
  }

  .lockup[data-published="false"] .name {
    color: var(--c-text-muted);
    font-weight: var(--fw-medium);
  }

  .visually-hidden {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
