<script lang="ts" generics="T extends Record<string, unknown>">
  import type { Snippet } from "svelte";
  import type { Column, Sort } from "./table";

  interface Props {
    columns: Column[];
    rows: T[];
    /** Rendered per row. Receives the row and its index. */
    row: Snippet<[T, number]>;
    /** Table caption. Always provide one: it is the table's accessible name. */
    caption: string;
    sort?: Sort;
    onsort?: (sort: Sort) => void;
    /** Off by default: loading, empty and error states are separate
     *  components so their wording is written once. */
    state?: "ready" | "loading";
    dense?: boolean;
    /** Keeps the header visible while the body scrolls. */
    sticky?: boolean;
  }

  let {
    columns,
    rows,
    row,
    caption,
    sort = $bindable(),
    onsort,
    state = "ready",
    dense = false,
    sticky = true,
  }: Props = $props();

  function toggle(column: Column) {
    if (!column.sortable || !onsort) return;
    const dir = sort?.key === column.key && sort.dir === "desc" ? "asc" : "desc";
    const next = { key: column.key, dir } satisfies Sort;
    sort = next;
    onsort(next);
  }

  const ariaSort = (column: Column) => {
    if (!column.sortable) return undefined;
    if (sort?.key !== column.key) return "none" as const;
    return sort.dir === "asc" ? ("ascending" as const) : ("descending" as const);
  };
</script>

<div class="table-wrap" class:dense data-sticky={sticky}>
  <table>
    <caption>{caption}</caption>
    <thead>
      <tr>
        {#each columns as column (column.key)}
          <th
            scope="col"
            class:numeric={column.numeric}
            style={column.width ? `inline-size: ${column.width}` : undefined}
            aria-sort={ariaSort(column)}
          >
            {#if column.sortable}
              <button type="button" onclick={() => toggle(column)}>
                <span>{column.label}</span>
                <span class="marker" aria-hidden="true">
                  {sort?.key === column.key ? (sort.dir === "asc" ? "↑" : "↓") : "↕"}
                </span>
                {#if column.hint}
                  <span class="visually-hidden">. {column.hint}</span>
                {/if}
              </button>
            {:else}
              <span class="plain">
                {column.label}
                {#if column.hint}
                  <span class="visually-hidden">. {column.hint}</span>
                {/if}
              </span>
            {/if}
          </th>
        {/each}
      </tr>
    </thead>
    <tbody data-loading={state === "loading"}>
      {#each rows as record, i (i)}
        <tr class:loading={state === "loading"}>
          {@render row(record, i)}
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .table-wrap {
    overflow: auto;
    /* The body never wraps a header onto two lines: a dense table is scanned,
       not read. */
    max-block-size: var(--table-max, 60vh);
  }

  table {
    inline-size: 100%;
    border-collapse: collapse;
    font-size: var(--step--1);
  }

  caption {
    text-align: start;
    padding: var(--space-3) var(--space-4);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    background: var(--c-surface);
  }

  th {
    position: relative;
    text-align: start;
    padding: var(--space-2) var(--cell-x);
    background: var(--c-surface-sunken);
    /* The header is separated by a 2px ink rule, not a 1px grey line. */
    border-block-end: 2px solid var(--c-rule-strong);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-strong);
    white-space: nowrap;
  }

  .table-wrap[data-sticky="true"] thead th {
    position: sticky;
    inset-block-start: 0;
    z-index: 2;
  }

  th.numeric {
    text-align: end;
  }

  th button {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    inline-size: 100%;
    min-block-size: 2rem;
    padding: 0;
    background: none;
    border: 0;
    cursor: pointer;
    font: inherit;
    letter-spacing: inherit;
    text-transform: inherit;
    color: inherit;
  }

  /* A sort control is the only thing in its header cell, so on touch it gets
     the full floor rather than the compact desktop row height. */
  @media (max-width: 767px) {
    th button {
      min-block-size: var(--touch-target);
      min-inline-size: var(--touch-target);
      justify-content: flex-start;
    }

    th.numeric button {
      justify-content: flex-end;
    }
  }

  th.numeric button {
    justify-content: flex-end;
  }

  th button:hover {
    color: var(--c-text-accent-strong);
    text-decoration: underline;
    text-underline-offset: 0.18em;
  }

  .marker {
    color: var(--c-blue);
  }

  /* Cells are authored by the consuming page's row snippet, so they are styled
     through a global selector anchored to this wrapper. */
  .table-wrap :global(td) {
    padding: var(--row-y) var(--cell-x);
    border-block-end: 2px solid var(--c-rule);
    vertical-align: top;
  }

  .table-wrap.dense :global(td) {
    padding-block: var(--space-1);
  }

  .visually-hidden {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  tr.loading {
    color: transparent;
    background: var(--tint-accent);
    animation: throb 1.2s var(--ease) infinite;
  }

  @keyframes throb {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.55; }
  }

  @media (prefers-reduced-motion: reduce) {
    tr.loading { animation: none; }
  }
</style>
