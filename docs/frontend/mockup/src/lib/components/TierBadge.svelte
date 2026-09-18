<script lang="ts">
  /** The frozen Tier enum from docs/contracts.md. */
  export type Tier = "S+" | "S" | "A" | "B" | "C" | "D";

  interface Props {
    tier: Tier;
    /** Optional qualifier shown after the glyph, e.g. "top 4%". */
    note?: string;
    size?: "sm" | "md";
  }

  let { tier, note = undefined, size = "md" }: Props = $props();

  /* Six tiers, two visual decisions each. No rainbow: the encoding is
     weight plus one accent tint, so the badge survives greyscale printing
     and any colour-vision deficiency. */
  const rank = $derived(tier === "S+" ? 0 : tier === "S" ? 1 : tier === "A" ? 2 : tier === "B" ? 3 : tier === "C" ? 4 : 5);

  const spoken = $derived(tier === "S+" ? "Tier S plus" : `Tier ${tier}`);
</script>

<span class="tier" data-rank={rank} data-size={size}>
  <span class="visually-hidden">{spoken}{note ? `, ${note}` : ""}</span>
  <span aria-hidden="true">{tier}</span>
  {#if note}
    <span class="note" aria-hidden="true">{note}</span>
  {/if}
</span>

<style>
  .tier {
    display: inline-flex;
    align-items: baseline;
    gap: var(--space-2);
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
    font-feature-settings: var(--num-features);
    line-height: 1;
    white-space: nowrap;
  }

  .tier[data-size="sm"] {
    font-size: var(--step--2);
  }

  .tier[data-size="md"] {
    font-size: var(--step-0);
  }

  .note {
    font-size: var(--step--2);
    font-weight: var(--fw-regular);
    color: var(--c-text-muted);
  }

  /* Rank 0 and 1: the top of the scale carries the accent. */
  .tier[data-rank="0"] > span:not(.visually-hidden):not(.note) {
    color: var(--c-cream);
    background: var(--c-blue);
    padding: 0.1em 0.35em;
  }

  .tier[data-rank="1"] > span:not(.visually-hidden):not(.note) {
    color: var(--c-text-accent-strong);
    box-shadow: var(--ring);
    padding: 0.1em 0.35em;
  }

  .tier[data-rank="2"] > span:not(.visually-hidden):not(.note) {
    color: var(--c-text-strong);
    box-shadow: var(--ring);
    padding: 0.1em 0.35em;
  }

  /* Ranks 3-5: mid and low tiers are plain ink so they recede. */
  .tier[data-rank="3"] > span:not(.visually-hidden):not(.note) {
    color: var(--c-text-strong);
  }

  .tier[data-rank="4"] > span:not(.visually-hidden):not(.note),
  .tier[data-rank="5"] > span:not(.visually-hidden):not(.note) {
    color: var(--c-text-muted);
  }

  .visually-hidden {
    position: absolute;
    inline-size: 1px;
    block-size: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
