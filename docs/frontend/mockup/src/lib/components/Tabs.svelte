<script lang="ts">
  import type { Snippet } from "svelte";

  interface Item {
    id: string;
    label: string;
    /** Optional count rendered after the label, e.g. a row count. */
    count?: number;
  }

  interface Props {
    items: Item[];
    value: string;
    onchange?: (id: string) => void;
    /** Panels are addressed by id. */
    panel: Snippet<[string]>;
    label?: string;
  }

  let { items, value = $bindable(), onchange, panel, label = "Sections" }: Props = $props();

  let refs: HTMLButtonElement[] = $state([]);

  function select(id: string) {
    value = id;
    onchange?.(id);
  }

  function onkeydown(event: KeyboardEvent, index: number) {
    const keys = ["ArrowRight", "ArrowLeft", "Home", "End"];
    if (!keys.includes(event.key)) return;
    event.preventDefault();
    const last = items.length - 1;
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? last
          : event.key === "ArrowRight"
            ? (index + 1) % items.length
            : (index + last) % items.length;
    select(items[next].id);
    refs[next]?.focus();
  }
</script>

<div class="tabs">
  <div class="list" role="tablist" aria-label={label}>
    {#each items as item, i (item.id)}
      <button
        bind:this={refs[i]}
        type="button"
        role="tab"
        id={`tab-${item.id}`}
        aria-selected={value === item.id}
        aria-controls={`panel-${item.id}`}
        tabindex={value === item.id ? 0 : -1}
        onclick={() => select(item.id)}
        onkeydown={(e) => onkeydown(e, i)}
      >
        <span>{item.label}</span>
        {#if item.count !== undefined}
          <span class="count num">{item.count}</span>
        {/if}
      </button>
    {/each}
  </div>
  <div
    class="panel"
    role="tabpanel"
    id={`panel-${value}`}
    aria-labelledby={`tab-${value}`}
    tabindex="-1"
  >
    {@render panel(value)}
  </div>
</div>

<style>
  .list {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    border-block-end: 2px solid var(--c-rule-strong);
  }

  .list button {
    display: inline-flex;
    align-items: baseline;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    background: none;
    border: 0;
    cursor: pointer;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-muted);
    /* The active tab is a raised paper tab: ring plus offset, like every
       other surface. Turning it blue would make it look actionable. */
    box-shadow: none;
    transition: color var(--dur-fast) var(--ease);
  }

  .list button:hover {
    color: var(--c-text-strong);
  }

  .list button[aria-selected="true"] {
    background: var(--c-surface);
    color: var(--c-text-strong);
    box-shadow:
      var(--ring),
      4px -4px 0 var(--c-blue);
  }

  .count {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .panel {
    padding-block-start: var(--space-5);
  }
</style>
