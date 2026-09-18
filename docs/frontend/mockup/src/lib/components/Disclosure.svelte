<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    title: string;
    /** Open on first render. Accordions hold secondary detail, so closed is
     *  the default. */
    open?: boolean;
    /** Rendered under the title when closed: one line of what is inside. */
    summary?: string;
    children: Snippet;
  }

  let { title, open = false, summary = undefined, children }: Props = $props();
  // svelte-ignore state_referenced_locally
  // The panel adopts the prop as its starting state and owns it afterwards.
  let expanded = $state(open);
  const uid = $props.id();
  const id = `disclosure-${uid}`;
</script>

<div class="disclosure">
  <h3 class="heading">
    <button
      type="button"
      class="trigger"
      aria-expanded={expanded}
      aria-controls={id}
      onclick={() => (expanded = !expanded)}
    >
      <span class="marker num" aria-hidden="true">{expanded ? "−" : "+"}</span>
      <span class="grow">
        <span class="title">{title}</span>
        {#if summary && !expanded}
          <span class="summary">{summary}</span>
        {/if}
      </span>
    </button>
  </h3>
  <div class="panel" {id} hidden={!expanded}>
    <div class="panel-inner prose">{@render children()}</div>
  </div>
</div>

<style>
  .disclosure {
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
  }

  .heading {
    font-size: var(--step-1);
    font-weight: var(--fw-regular);
    margin: 0;
  }

  .trigger {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
    inline-size: 100%;
    min-block-size: var(--touch-target);
    padding: var(--space-3) var(--space-4);
    background: none;
    border: 0;
    text-align: start;
    cursor: pointer;
  }

  .marker {
    font-weight: var(--fw-bold);
    color: var(--c-blue);
  }

  .title {
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-tight);
    color: var(--c-text-strong);
  }

  .summary {
    display: block;
    font-family: var(--font-body);
    font-size: var(--step--1);
    font-weight: var(--fw-regular);
    color: var(--c-text-muted);
    margin-block-start: var(--space-1);
  }

  .panel-inner {
    padding: 0 var(--space-4) var(--space-5);
    border-block-start: 2px solid var(--c-rule);
    padding-block-start: var(--space-4);
    display: grid;
    gap: var(--space-4);
    align-content: start;
  }
</style>
