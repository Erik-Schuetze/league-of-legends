<script lang="ts">
  interface Props {
    /** Signed change, in the same unit as the value it accompanies. */
    value: number;
    /** Unit suffix, e.g. "pp" for percentage points or "%". */
    unit?: string;
    /** What the change is measured against, e.g. "vs 16.17". Required: a delta
     *  with no baseline is a meaningless number. */
    baseline: string;
    /** Lower is better here, so a fall is the good outcome. */
    inverted?: boolean;
    decimals?: number;
    size?: "sm" | "md";
  }

  let {
    value,
    unit = "",
    baseline,
    inverted = false,
    decimals = 2,
    size = "md",
  }: Props = $props();

  const direction = $derived(value > 0 ? "up" : value < 0 ? "down" : "flat");
  const favourable = $derived(
    direction !== "flat" && (inverted ? direction === "down" : direction === "up"),
  );
  const spoken = $derived(
    direction === "flat"
      ? `No change ${baseline}`
      : `${direction === "up" ? "Up" : "Down"} ${Math.abs(value).toFixed(decimals)}${unit} ${baseline}`,
  );

  /* The sign is written out, not implied by the arrow: colour alone is never
     the message, and the glyph is decorative. */
  const text = $derived(
    `${value > 0 ? "+" : value < 0 ? "\u2212" : "\u00b1"}${Math.abs(value).toFixed(decimals)}${unit}`,
  );
</script>

<span class="delta" data-dir={direction} data-good={favourable} data-size={size} title={`${spoken}.${direction === "flat" ? "" : favourable ? " This is the better direction." : " This is the worse direction."}`}>
  <span class="visually-hidden">{spoken}</span>
  <span class="glyph" aria-hidden="true">{direction === "up" ? "▲" : direction === "down" ? "▼" : "—"}</span>
  <span class="value num" aria-hidden="true">{text}</span>
  <span class="baseline" aria-hidden="true">{baseline}</span>
</span>

<style>
  .delta {
    display: inline-flex;
    align-items: baseline;
    gap: var(--space-2);
    font-size: var(--step--1);
    white-space: nowrap;
  }

  .delta[data-size="sm"] {
    font-size: var(--step--2);
  }

  .value {
    font-weight: var(--fw-bold);
  }

  .glyph {
    font-size: 0.75em;
  }

  .baseline {
    color: var(--c-text-muted);
    font-size: var(--step--2);
  }

  /* Colour carries sign; the glyph and the explicit +/− carry it too, so the
     component still reads in greyscale. */
  .delta[data-dir="up"] .value,
  .delta[data-dir="up"] .glyph {
    color: var(--sig-up);
  }

  .delta[data-dir="down"] .value,
  .delta[data-dir="down"] .glyph {
    color: var(--sig-down);
  }

  .delta[data-dir="flat"] {
    color: var(--sig-flat);
  }

  /* When the move is the good direction, the value gets a tinted plate: the
     reading is "this improved", not merely "this went up". */
  .delta[data-good="true"] .value {
    background: var(--sig-up-tint);
    padding-inline: 0.25em;
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
