<script lang="ts">
  export interface ExportFormat {
    id: string;
    label: string;
    /** Shown under the label so the guest knows what they are getting. */
    note?: string;
  }

  interface Props {
    formats?: ExportFormat[];
    /** Human-readable description of the current view, printed into the file
     *  header so an export is self-describing. */
    scope: string;
    /** Proposed file name without extension. */
    filename: string;
    /** Row count offered as a warning for large exports. */
    rowCount?: number;
    /** Called with the format id. Generating the file is the page's job. */
    onexport: (id: string) => void;
    /** Rendered under the menu: the licence and attribution the export carries. */
    footnote?: string;
  }

  let {
    formats = [
      { id: "csv", label: "CSV", note: "one row per cell, spreadsheet-ready" },
      { id: "json", label: "JSON", note: "the same cells with their envelope" },
    ],
    scope,
    filename,
    rowCount = undefined,
    onexport,
    footnote = undefined,
  }: Props = $props();

  let open = $state(false);
  let active = $state(0);
  let trigger = $state<HTMLButtonElement | null>(null);
  let items = $state<HTMLButtonElement[]>([]);

  function openMenu() {
    open = true;
    active = 0;
    queueMicrotask(() => items[0]?.focus());
  }

  function close(focusTrigger = true) {
    open = false;
    if (focusTrigger) trigger?.focus();
  }

  function onkeydown(event: KeyboardEvent) {
    if (!open) return;
    if (event.key === "Escape") {
      event.preventDefault();
      close();
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      active = (active + 1) % formats.length;
      items[active]?.focus();
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      active = (active - 1 + formats.length) % formats.length;
      items[active]?.focus();
    }
  }

  function choose(id: string) {
    onexport(id);
    close();
  }
</script>

<div class="export">
  <button
    type="button"
    class="trigger"
    bind:this={trigger}
    aria-expanded={open}
    aria-haspopup="menu"
    onclick={() => (open ? close() : openMenu())}
    onkeydown={onkeydown}
  >
    Export
    <span class="caret" aria-hidden="true">▾</span>
  </button>

  {#if open}
    <div class="menu" role="menu" aria-label="Export this view" tabindex="-1" onkeydown={onkeydown}>
      <p class="scope">{scope}</p>
      {#each formats as format, i (format.id)}
        <button
          type="button"
          role="menuitem"
          bind:this={items[i]}
          onclick={() => choose(format.id)}
          onmouseenter={() => (active = i)}
        >
          <span class="label">{format.label}</span>
          {#if format.note}<span class="note">{format.note}</span>{/if}
        </button>
      {/each}
      {#if rowCount !== undefined && rowCount > 5000}
        <p class="warn">
          {rowCount.toLocaleString("en-GB")} rows: a large export. Narrow the filters first if you
          only need a slice.
        </p>
      {/if}
      {#if footnote}
        <p class="foot">{footnote}</p>
      {/if}
      <p class="foot">Saved as <span class="num">{filename}</span></p>
    </div>
  {/if}
</div>

<style>
  .export {
    position: relative;
  }

  .trigger {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    background: var(--c-blue);
    color: var(--c-cream);
    border: 0;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    cursor: pointer;
  }

  .trigger:hover {
    background: var(--c-ink);
  }

  .caret {
    font-size: 0.75em;
  }

  /* The menu floats on the same flat elevation as everything else. */
  .menu {
    position: absolute;
    inset-inline-end: 0;
    inset-block-start: calc(100% + var(--space-2));
    z-index: 30;
    min-inline-size: 17rem;
    padding: var(--space-2);
    background: var(--c-surface);
    box-shadow: var(--ring), var(--offset-card);
    display: grid;
    gap: var(--space-2);
  }

  .scope {
    margin: 0;
    padding: var(--space-2) var(--space-3) 0;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .menu button {
    display: grid;
    gap: var(--space-1);
    text-align: start;
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-3);
    background: transparent;
    border: 0;
    cursor: pointer;
  }

  .menu button:hover,
  .menu button:focus-visible {
    background: var(--tint-accent-strong);
  }

  .label {
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
  }

  .note {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .warn,
  .foot {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    font-size: var(--step--2);
    color: var(--c-text-muted);
    border-block-start: 2px solid var(--c-rule);
  }

  .warn {
    background: var(--sig-down-tint);
  }

  .num {
    font-family: var(--font-mono);
    font-feature-settings: var(--num-features);
    color: var(--c-text-strong);
  }
</style>
