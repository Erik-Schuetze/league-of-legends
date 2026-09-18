<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    title: string;
    /** Small right-hand slot: a link, a count, a sort control. */
    aside?: Snippet;
    /** Heading level. Sections inside a page are h2 by default. */
    level?: 2 | 3 | 4;
    id?: string;
  }

  let { title, aside, level = 2, id = undefined }: Props = $props();
  const uid = $props.id();
  const headingId = $derived(id ?? `section-${uid}`);
</script>

<div class="section-header">
  <svelte:element this={`h${level}`} id={headingId} class="title">{title}</svelte:element>
  <div class="rule" aria-hidden="true"></div>
  {#if aside}
    <div class="aside">{@render aside()}</div>
  {/if}
</div>

<style>
  .section-header {
    display: flex;
    align-items: baseline;
    gap: var(--space-4);
    margin-block-end: var(--space-5);
  }

  .title {
    flex: 0 0 auto;
    font-size: var(--step-2);
  }

  /* The rule is the section divider: a 2px ink line, not a 1px grey one. */
  .rule {
    flex: 1 1 auto;
    block-size: 2px;
    background: var(--c-rule-strong);
    align-self: center;
    min-inline-size: var(--space-5);
  }

  .aside {
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }
</style>
