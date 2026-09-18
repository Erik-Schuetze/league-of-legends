<script lang="ts">
  interface Props {
    /** Patches the site has data for, newest first. Empty means nothing at all. */
    availablePatches: string[];
    /** The patch or bracket the visitor asked for. */
    requested?: string;
    /** Region, queue and bracket, so the message names the exact slice. */
    region?: string;
    queue?: string;
    bracket?: string;
    /** Where a working slice can be found. */
    suggestedHref?: string;
    suggestedLabel?: string;
  }

  let {
    availablePatches,
    requested = undefined,
    region = undefined,
    queue = undefined,
    bracket = undefined,
    suggestedHref = undefined,
    suggestedLabel = undefined,
  }: Props = $props();

  const uid = $props.id();
  const headingId = `no-data-heading-${uid}`;

  /* Naming the slice in words is the whole point of this component: "no data"
     without a scope is the message this site exists to avoid. */
  const scope = $derived(
    [region, queue, bracket].filter((part) => part !== undefined).join(" · ") || "this selection",
  );
</script>

<section class="no-data" aria-labelledby={headingId}>
  <p class="stamp">No published data</p>
  <h3 id={headingId} class="heading">
    {#if availablePatches.length === 0}
      Nothing has been published yet
    {:else}
      No published data for {requested ? `${requested}, ` : ""}{scope}
    {/if}
  </h3>
  <p class="body">
    {#if availablePatches.length === 0}
      No build artifacts exist at this address. This is a publisher-side condition, not a filter that
      matched nothing.
    {:else}
      This slice is not suppressed: it was never built. The published patches are
      <span class="num">{availablePatches.join(", ")}</span>.
    {/if}
  </p>
  <div class="foot">
    <span class="hint">Absence is shown rather than hidden</span>
    {#if suggestedHref && suggestedLabel}
      <a class="suggest" href={suggestedHref}>{suggestedLabel}</a>
    {/if}
  </div>
</section>

<style>
  .no-data {
    display: grid;
    gap: var(--space-3);
    padding: var(--space-5);
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    max-inline-size: var(--measure);
  }

  .stamp {
    margin: 0;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--sig-unknown);
  }

  .heading {
    margin: 0;
    font-family: var(--font-heading);
    font-size: var(--step-1);
    color: var(--c-text-strong);
  }

  .body {
    margin: 0;
    color: var(--c-text);
    line-height: var(--lh-body);
  }

  .num {
    font-family: var(--font-mono);
    font-feature-settings: var(--num-features);
    font-weight: var(--fw-bold);
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding-block-start: var(--space-2);
    border-block-start: 2px solid var(--c-rule);
  }

  .hint {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .suggest {
    font-weight: var(--fw-semibold);
    color: var(--c-text-accent-strong);
    text-decoration: underline;
    text-underline-offset: 0.2em;
    min-block-size: var(--touch-target);
    display: inline-flex;
    align-items: center;
  }
</style>
