<script lang="ts">
  interface Props {
    patch: string;
    region: string;
    queue: string;
    bracket: string;
    /** ISO timestamp of the build. */
    generatedAt: string;
    /** Inclusive date range the games were played in. */
    sourceWindow?: { from: string; to: string };
    minCellN?: number;
    suppressedCells?: number;
    buildRunId?: string | number;
    /** "stamp" is the one-line strip that sits above every table. */
    variant?: "stamp" | "card";
  }

  let {
    patch,
    region,
    queue,
    bracket,
    generatedAt,
    sourceWindow = undefined,
    minCellN = undefined,
    suppressedCells = undefined,
    buildRunId = undefined,
    variant = "stamp",
  }: Props = $props();

  const generated = $derived(new Date(generatedAt));

  /* Fixed locale and UTC: a stamp that renders differently per machine is not
     a stamp. */
  const generatedLabel = $derived(
    new Intl.DateTimeFormat("en-GB", {
      day: "2-digit",
      month: "short",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      timeZone: "UTC",
      timeZoneName: "short",
    }).format(generated),
  );

  const windowLabel = $derived(
    sourceWindow ? `${sourceWindow.from} → ${sourceWindow.to}` : undefined,
  );
</script>

<div class="stamp" data-variant={variant}>
  <span class="spoken">Coverage: </span>
  <dl>
    <div class="pair">
      <dt>Patch</dt>
      <dd class="num">{patch}</dd>
    </div>
    <div class="pair">
      <dt>Region</dt>
      <dd class="num">{region}</dd>
    </div>
    <div class="pair">
      <dt>Queue</dt>
      <dd class="num">{queue}</dd>
    </div>
    <div class="pair">
      <dt>Bracket</dt>
      <dd class="num">{bracket}</dd>
    </div>
    {#if windowLabel}
      <div class="pair">
        <dt>Games played</dt>
        <dd class="num">{windowLabel}</dd>
      </div>
    {/if}
    <div class="pair">
      <dt>Built</dt>
      <dd class="num">{generatedLabel}</dd>
    </div>
    {#if minCellN !== undefined}
      <div class="pair">
        <dt>Cell floor</dt>
        <dd class="num">{minCellN.toLocaleString("en-GB")} games</dd>
      </div>
    {/if}
    {#if suppressedCells !== undefined}
      <div class="pair">
        <dt>Withheld</dt>
        <dd class="num">{suppressedCells.toLocaleString("en-GB")} cells</dd>
      </div>
    {/if}
    {#if buildRunId !== undefined}
      <div class="pair">
        <dt>Build</dt>
        <dd class="num">run {buildRunId}</dd>
      </div>
    {/if}
  </dl>
</div>

<style>
  .stamp {
    background: var(--c-surface);
  }

  .stamp[data-variant="stamp"] {
    box-shadow: var(--shadow-inset);
    padding: var(--space-3) var(--space-4);
  }

  .stamp[data-variant="card"] {
    box-shadow: var(--shadow-card);
    padding: var(--space-4);
  }

  dl {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-5);
    margin: 0;
  }

  .pair {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
  }

  dt {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
  }

  dd {
    margin: 0;
    font-family: var(--font-mono);
    font-feature-settings: var(--num-features);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
  }

  .spoken {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
