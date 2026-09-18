<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    /** Uppercase kicker above the title: the page's section. */
    eyebrow?: string;
    title: string;
    lede?: string;
    /** Coverage stamp and other metadata, rendered under the lede. */
    meta?: Snippet;
    actions?: Snippet;
    children?: Snippet;
    labelledby?: string;
    /** Only set to 2 when the header is shown inside another page (the gallery
        specimen, an embedded docs view). A page's own header is an h1. */
    level?: 1 | 2;
  }

  let {
    eyebrow = undefined,
    title,
    lede = undefined,
    meta,
    actions,
    children,
    labelledby,
    level = 1,
  }: Props = $props();

  /* $props.id() is stable between the prerender and hydration pass, unlike a
     random id: a mismatch would break the aria wiring on first paint. */
  const uid = $props.id();
  const id = $derived(labelledby ?? `page-title-${uid}`);
</script>

<header class="page-header" aria-labelledby={id}>
  <div class="stack stack-4">
    {#if eyebrow}
      <p class="eyebrow">{eyebrow}</p>
    {/if}
    <svelte:element this={`h${level}`} id={id}>{title}</svelte:element>
    {#if lede}
      <p class="lede">{lede}</p>
    {/if}
    {#if meta}
      <div class="meta">{@render meta()}</div>
    {/if}
    {#if actions}
      <div class="actions cluster cluster-3">{@render actions()}</div>
    {/if}
  </div>
  {#if children}
    <div class="extra">{@render children()}</div>
  {/if}
</header>

<style>
  .page-header {
    padding-block: var(--space-7) var(--space-6);
  }

  .page-header :global(h1),
  .page-header :global(h2) {
    max-width: var(--measure);
  }

  .meta {
    max-width: var(--measure);
  }

  .actions {
    padding-block-start: var(--space-1);
  }

  .extra {
    margin-block-start: var(--space-6);
  }
</style>
