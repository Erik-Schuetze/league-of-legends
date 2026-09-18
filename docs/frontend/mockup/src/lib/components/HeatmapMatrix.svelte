<script lang="ts">
  import type { Role } from "./RoleBadge.svelte";

  export interface HeatCell {
    /** Win rate as a 0-1 fraction. Absent when not published. */
    value?: number;
    n?: number;
    /** Withheld by the suppression floor. */
    suppressed?: boolean;
    /** No cell exists for this pair in this slice at all. */
    absent?: boolean;
  }

  export interface HeatRow {
    name: string;
    slug: string;
    cells: Record<string, HeatCell>;
  }

  interface Props {
    rows: HeatRow[];
    columns: Role[];
    /** The pivot the tint is measured against, 0-1. Default is the coin flip. */
    pivot?: number;
    /** Distance from the pivot that saturates the scale. */
    spread?: number;
    caption: string;
    /** Explains what the numbers mean and what a missing cell means. */
    legend?: string;
    /** Formats the cell value for display and for speech. */
    format?: (value: number) => string;
  }

  let {
    rows,
    columns,
    pivot = 0.5,
    spread = 0.05,
    caption,
    legend = undefined,
    format = (v: number) => `${(v * 100).toFixed(1)}%`,
  }: Props = $props();

  /* Three symmetric steps each side. The number is always printed, so the
     tint is a scanning aid and never the value. */
  function step(cell: HeatCell | undefined): number {
    if (!cell || cell.absent || cell.suppressed || cell.value === undefined) return 0;
    const distance = (cell.value - pivot) / spread;
    const magnitude = Math.min(3, Math.ceil(Math.abs(distance)));
    return Math.sign(distance) * magnitude;
  }

  const tone = (s: number) => (s === 0 ? "flat" : s > 0 ? "up" : "down");
</script>

<div class="heat">
  <table>
    <caption>
      <span class="cap">{caption}</span>
      {#if legend}<span class="cap-note">{legend}</span>{/if}
    </caption>
    <thead>
      <tr>
        <th scope="col" class="corner">Champion</th>
        {#each columns as column (column)}
          <th scope="col" class="col">{column}</th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each rows as row (row.slug)}
        <tr>
          <th scope="row">
            <span class="name">{row.name}</span>
            <span class="slug num">{row.slug}</span>
          </th>
          {#each columns as column (column)}
            {@const cell = row.cells[column]}
            {@const s = step(cell)}
            <td
              class="cell"
              data-tone={tone(s)}
              data-magnitude={Math.abs(s)}
              data-missing={!cell || cell.absent ? "absent" : cell.suppressed ? "suppressed" : "no"}
            >
              {#if !cell || cell.absent}
                <span class="spoken">{column} not in this slice</span>
                <span class="glyph" aria-hidden="true">·</span>
              {:else if cell.suppressed || cell.value === undefined}
                <span class="spoken">{column} withheld: fewer games than the minimum</span>
                <span class="glyph" aria-hidden="true">n&lt;</span>
              {:else}
                <span class="spoken">{column} {format(cell.value)}{#if cell.n}, {cell.n} games{/if}</span>
                <span class="value num" aria-hidden="true">{format(cell.value)}</span>
                {#if cell.n !== undefined}
                  <span class="n num" aria-hidden="true">n {cell.n.toLocaleString("en-GB")}</span>
                {/if}
              {/if}
            </td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
  <div class="key">
    <span class="key-item"><span class="swatch" data-tone="down" data-magnitude="3"></span>well below {format(pivot)}</span>
    <span class="key-item"><span class="swatch" data-tone="flat"></span>near {format(pivot)}</span>
    <span class="key-item"><span class="swatch" data-tone="up" data-magnitude="3"></span>well above {format(pivot)}</span>
    <span class="key-item"><span class="swatch" data-missing="suppressed"></span>withheld (under the minimum)</span>
    <span class="key-item"><span class="swatch" data-missing="absent"></span>no cell in this slice</span>
  </div>
</div>

<style>
  .heat {
    overflow-x: auto;
  }

  table {
    border-collapse: collapse;
    font-size: var(--step--1);
  }

  caption {
    display: grid;
    gap: var(--space-1);
    text-align: start;
    padding-block-end: var(--space-3);
  }

  .cap {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  .cap-note {
    font-size: var(--step--1);
    color: var(--c-text-muted);
    max-inline-size: 60ch;
  }

  th {
    padding: var(--space-2) var(--cell-x);
    border-block-end: 2px solid var(--c-ink);
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    text-align: start;
    white-space: nowrap;
    background: var(--c-surface);
  }

  .col,
  .corner {
    text-align: center;
  }

  th[scope="row"] {
    position: sticky;
    inset-inline-start: 0;
    z-index: 1;
    display: grid;
    gap: 0;
  }

  .name {
    font-family: var(--font-heading);
    font-size: var(--step--1);
    letter-spacing: var(--ls-normal);
    text-transform: none;
    color: var(--c-text-strong);
  }

  .slug {
    font-size: var(--step--2);
    font-weight: var(--fw-regular);
    text-transform: none;
    letter-spacing: var(--ls-normal);
    color: var(--c-text-muted);
  }

  td.cell {
    min-inline-size: 5.5rem;
    padding: var(--space-2);
    text-align: center;
    border-block-end: 2px solid var(--c-rule);
    border-inline-start: 2px solid var(--c-rule);
  }

  .value {
    display: block;
    font-weight: var(--fw-bold);
    color: var(--c-ink);
  }

  .n {
    display: block;
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .glyph {
    color: var(--sig-unknown);
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
  }

  .spoken {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }

  /* Tint plate under the number. Ink text stays ink, so every cell clears AA
     whatever the plate does. */
  td[data-tone="up"][data-magnitude="1"] { background: color-mix(in srgb, var(--sig-up) 8%, var(--c-cream)); }
  td[data-tone="up"][data-magnitude="2"] { background: color-mix(in srgb, var(--sig-up) 16%, var(--c-cream)); }
  td[data-tone="up"][data-magnitude="3"] { background: color-mix(in srgb, var(--sig-up) 26%, var(--c-cream)); }
  td[data-tone="down"][data-magnitude="1"] { background: color-mix(in srgb, var(--sig-down) 8%, var(--c-cream)); }
  td[data-tone="down"][data-magnitude="2"] { background: color-mix(in srgb, var(--sig-down) 16%, var(--c-cream)); }
  td[data-tone="down"][data-magnitude="3"] { background: color-mix(in srgb, var(--sig-down) 26%, var(--c-cream)); }
  td[data-tone="flat"] { background: var(--c-surface); }

  td[data-missing="suppressed"],
  td[data-missing="absent"] {
    background: repeating-linear-gradient(-45deg, transparent 0 4px, var(--c-rule) 4px 6px);
  }

  td[data-missing="absent"] {
    background: none;
  }

  .key {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    padding-block-start: var(--space-3);
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .key-item {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
  }

  .swatch {
    inline-size: 1rem;
    block-size: 1rem;
    box-shadow: inset 0 0 0 2px var(--c-rule);
    background: var(--c-surface);
  }

  .swatch[data-tone="up"][data-magnitude="3"] { background: color-mix(in srgb, var(--sig-up) 26%, var(--c-cream)); }
  .swatch[data-tone="down"][data-magnitude="3"] { background: color-mix(in srgb, var(--sig-down) 26%, var(--c-cream)); }
  .swatch[data-missing="suppressed"] { background: repeating-linear-gradient(-45deg, transparent 0 4px, var(--c-rule) 4px 6px); }
</style>
