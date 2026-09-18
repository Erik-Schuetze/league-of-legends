<script lang="ts">
  interface Crumb {
    href?: string;
    label: string;
  }

  interface Props {
    items: Crumb[];
  }

  let { items }: Props = $props();
</script>

<nav class="crumbs" aria-label="Breadcrumb">
  <ol>
    {#each items as item, i (item.label)}
      <li>
        {#if item.href && i < items.length - 1}
          <a href={item.href}>{item.label}</a>
        {:else}
          <span aria-current="page">{item.label}</span>
        {/if}
      </li>
    {/each}
  </ol>
</nav>

<style>
  .crumbs {
    font-family: var(--font-mono);
    font-size: var(--step--2);
  }

  ol {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  li {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
  }

  li + li::before {
    content: "/";
    color: var(--c-text-muted);
  }

  a {
    color: var(--c-text-muted);
    text-decoration: none;
  }

  a:hover {
    color: var(--c-text-accent-strong);
    text-decoration: underline;
  }

  [aria-current="page"] {
    color: var(--c-text-strong);
    font-weight: var(--fw-bold);
  }

  /* Breadcrumb links sit in a nav landmark, so they are controls: on touch
     each one gets the floor instead of a 18px line box. */
  @media (max-width: 767px) {
    a {
      display: inline-flex;
      align-items: center;
      min-block-size: var(--touch-target);
    }
  }
</style>
