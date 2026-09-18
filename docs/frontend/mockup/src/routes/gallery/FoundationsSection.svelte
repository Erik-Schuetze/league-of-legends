<script lang="ts">
  import FloatingTile from "$lib/components/FloatingTile.svelte";

  interface Swatch {
    token: string;
    value: string;
    use: string;
    /** Set when the swatch is a text/fill colour with a measured ratio. */
    ratio?: string;
  }

  const palette: { group: string; caption: string; swatches: Swatch[] }[] = [
    {
      group: "Core",
      caption: "The five brand values. Every other colour is one of these or a composite of them.",
      swatches: [
        { token: "--c-ink", value: "#0b162a", use: "Deepest navy. Strong headings, inverse surfaces." },
        { token: "--c-blue", value: "#1b4bc6", use: "Retro blue. Interaction, rings, shadows." },
        { token: "--c-sand", value: "#efdbbf", use: "Page background." },
        { token: "--c-cream", value: "#f1eae0", use: "Raised surface: cards, tiles, tables." },
        { token: "--c-graphite", value: "#242a2b", use: "Body text." }
      ]
    },
    {
      group: "Text",
      caption: "Ratios are against both page surfaces and are enforced by tools/contrast-audit.mjs.",
      swatches: [
        { token: "--c-text", value: "#242a2b", use: "Body.", ratio: "12.20:1 cream · 10.79:1 sand" },
        { token: "--c-text-strong", value: "#0b162a", use: "Headings, key figures.", ratio: "15.13:1 cream" },
        { token: "--c-text-muted", value: "#55524b", use: "Meta, captions, axis labels.", ratio: "6.53:1 cream · 5.77:1 sand" },
        { token: "--c-text-accent", value: "#1b4bc6", use: "Links, active control.", ratio: "6.13:1 cream · 5.42:1 sand" },
        { token: "--c-text-accent-strong", value: "#163f9f", use: "Link on sand, hover.", ratio: "7.83:1 cream · 6.93:1 sand" },
        { token: "--c-text-disabled", value: "#8b877e", use: "Disabled only. Never labels live data.", ratio: "3.00:1 · exempt" }
      ]
    },
    {
      group: "Semantics",
      caption: "Two derived hues in the same flat language. Separation between them is only 1.03–1.24:1, so colour is never the only signal.",
      swatches: [
        { token: "--sig-up", value: "#0f5f52", use: "Win, above baseline. Always with a sign or glyph.", ratio: "6.33:1 cream · 5.60:1 sand" },
        { token: "--sig-down", value: "#9e4a22", use: "Loss, below baseline. Always with a sign or glyph.", ratio: "5.09:1 cream · 4.50:1 sand" },
        { token: "--sig-flat", value: "#242a2b", use: "At baseline. No direction implied.", ratio: "12.20:1 cream" },
        { token: "--sig-unknown", value: "#6f6a60", use: "Withheld, not computed, not comparable.", ratio: "4.50:1 cream" }
      ]
    },
    {
      group: "Rules, tints, focus",
      caption: "Decorative rules are exempt from contrast only while nothing depends on seeing them.",
      swatches: [
        { token: "--c-rule", value: "#cbbba2", use: "Decorative hairline only.", ratio: "1.57:1 · decorative" },
        { token: "--c-control-border", value: "#6f6a60", use: "Every input border.", ratio: "4.50:1 cream · 3.98:1 sand" },
        { token: "--c-rule-strong", value: "#0b162a", use: "2px structural rules, table head." },
        { token: "--tint-accent", value: "7% blue", use: "Inert rows, inset wells." },
        { token: "--tint-accent-strong", value: "14% blue", use: "Selected rows, active tab." },
        { token: "--grid-line", value: "6% blue", use: "The 27px backdrop." },
        { token: "--focus-ring-color", value: "#1b4bc6", use: "2px ring, plus a 2px cream inner ring." },
        { token: "--c-scrim", value: "45% ink", use: "Behind dialogs and drawers." }
      ]
    }
  ];

  const typeScale: { token: string; role: string; sample: string }[] = [
    { token: "--step-5", role: "Hero. Landing headline only.", sample: "Patch 16.18" },
    { token: "--step-4", role: "h1, one per page.", sample: "Data explorer" },
    { token: "--step-3", role: "h2, section titles.", sample: "Win rates by role" },
    { token: "--step-2", role: "h3, stat value.", sample: "52.4%" },
    { token: "--step-1", role: "Lede paragraph.", sample: "141 published champion-role cells." },
    { token: "--step-0", role: "Body copy.", sample: "Every figure carries the sample it was computed from." },
    { token: "--step--1", role: "Dense table body.", sample: "Aatrox · TOP · 5.1% pick" },
    { token: "--step--2", role: "Meta, n, eyebrows.", sample: "n = 12,480 · 95% CI ±0.9" }
  ];

  const spacing: { token: string; px: string }[] = [
    { token: "--space-1", px: "4" },
    { token: "--space-2", px: "8" },
    { token: "--space-3", px: "12" },
    { token: "--space-4", px: "16" },
    { token: "--space-5", px: "24" },
    { token: "--space-6", px: "32" },
    { token: "--space-7", px: "48" },
    { token: "--space-8", px: "64" },
    { token: "--space-9", px: "96" }
  ];

  const ratios: { label: string; on: string; value: string }[] = [
    { label: "Body", on: "cream", value: "12.20:1" },
    { label: "Body", on: "sand", value: "10.79:1" },
    { label: "Strong", on: "cream", value: "15.13:1" },
    { label: "Muted", on: "cream", value: "6.53:1" },
    { label: "Muted", on: "sand", value: "5.77:1" },
    { label: "Accent", on: "cream", value: "6.13:1" },
    { label: "Accent", on: "sand", value: "5.42:1" },
    { label: "Win", on: "cream", value: "6.33:1" },
    { label: "Loss", on: "cream", value: "5.09:1" },
    { label: "Loss", on: "sand", value: "4.50:1" }
  ];

  let hoverTile = $state(false);
  let pressedTile = $state(false);
</script>

<div class="stack stack-6">
  <section class="block">
    <h2>Colour</h2>
    {#each palette as group (group.group)}
      <div class="stack stack-3">
        <h3>{group.group}</h3>
        <p class="caption">{group.caption}</p>
        <ul class="swatches">
          {#each group.swatches as swatch (swatch.token)}
            <li class="swatch">
              <span class="chip-face" style:background={`var(${swatch.token})`} aria-hidden="true"></span>
              <span class="swatch-body">
                <code class="num">{swatch.token}</code>
                <span class="value num">{swatch.value}</span>
                <span class="use">{swatch.use}</span>
                {#if swatch.ratio}<span class="ratio num">{swatch.ratio}</span>{/if}
              </span>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  </section>

  <section class="block">
    <h2>Measured contrast</h2>
    <p class="caption">
      Recomputed from tokens.css by <code>tools/contrast-audit.mjs</code>. AA needs 4.5:1 for text and
      3:1 for control borders; the audit fails the build otherwise.
    </p>
    <table class="plain">
      <caption class="visually-hidden">Contrast ratios for text colour on page surfaces</caption>
      <thead>
        <tr><th scope="col">Colour</th><th scope="col">Surface</th><th scope="col">Ratio</th><th scope="col">Verdict</th></tr>
      </thead>
      <tbody>
        {#each ratios as row (`${row.label}-${row.on}`)}
          <tr>
            <th scope="row">{row.label}</th>
            <td>{row.on}</td>
            <td class="num">{row.value}</td>
            <td>AA+</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </section>

  <section class="block">
    <h2>Type</h2>
    <ul class="families">
      <li><span class="fam" style:font-family="var(--font-brand)">Montserrat</span><span class="use">Wordmark only, 700.</span></li>
      <li><span class="fam" style:font-family="var(--font-heading)">JetBrains Mono</span><span class="use">Headings, numerals, controls, table chrome.</span></li>
      <li><span class="fam" style:font-family="var(--font-body)">Inter</span><span class="use">Prose, long labels, help text.</span></li>
    </ul>
    <ul class="scale">
      {#each typeScale as row (row.token)}
        <li>
          <p class="sample" style:font-size={`var(${row.token})`}>{row.sample}</p>
          <p class="scale-meta"><code class="num">{row.token}</code><span class="use">{row.role}</span></p>
        </li>
      {/each}
    </ul>
  </section>

  <section class="block">
    <h2>Elevation and radius</h2>
    <p class="caption">
      Two elevations, no blur, no rounded corners: a 2px accent ring plus a hard accent block offset
      down-right. The ring and the offset use the same colour as the surface's accent.
    </p>
    <div class="elev">
      <figure class="elev-item">
        <span class="elev-face card">8px</span>
        <figcaption><code class="num">--shadow-card</code> raised content</figcaption>
      </figure>
      <figure class="elev-item">
        <span class="elev-face hero">12px</span>
        <figcaption><code class="num">--shadow-hero</code> hero, one per page</figcaption>
      </figure>
      <figure class="elev-item">
        <span class="elev-face inset">inset</span>
        <figcaption><code class="num">--shadow-inset</code> sunken well</figcaption>
      </figure>
      <figure class="elev-item">
        <span class="elev-face" data-state={pressedTile ? "pressed" : "rest"}>6px</span>
        <figcaption><code class="num">--offset-hover</code> while hovered or pressed</figcaption>
      </figure>
    </div>
    <div class="cluster">
      <button
        type="button"
        class="demo-btn"
        onclick={() => (pressedTile = !pressedTile)}
        aria-pressed={pressedTile}
      >Toggle the pressed elevation</button>
      <span class="use">On hover the box moves 3px toward its shadow, so the shadow shrinks 8px → 6px. The box never grows.</span>
    </div>
  </section>

  <section class="block">
    <h2>Spacing and grid</h2>
    <ul class="spacing">
      {#each spacing as step (step.token)}
        <li><code class="num">{step.token}</code><span class="bar" style:inline-size={`var(${step.token})`}></span><span class="num px">{step.px}px</span></li>
      {/each}
    </ul>
    <p class="caption">
      Layout sits on a 27px backdrop grid (<code class="num">--grid-cell</code>) that fades out down the
      page. It is decoration: nothing is aligned to it because nothing may depend on a background image.
    </p>
    <div class="grid-demo" aria-hidden="true"></div>
  </section>

  <section class="block">
    <h2>Motion</h2>
    <p class="caption">
      One idle float for decorative tiles, three phases so tiles never move in lockstep, and one hover
      lift. Both are removed under <code>prefers-reduced-motion: reduce</code>; the gallery's motion
      assertions cover this.
    </p>
    <div class="cluster cluster-5">
      <FloatingTile phase="a">float a · 2.8s</FloatingTile>
      <FloatingTile phase="b">float b · 3.2s</FloatingTile>
      <FloatingTile phase="c">float c · 3.6s</FloatingTile>
      <FloatingTile>static · no float</FloatingTile>
    </div>
    <button
      type="button"
      class="demo-btn lift"
      ondblclick={() => (hoverTile = !hoverTile)}
      data-lifted={hoverTile}
    >Hover me for the 3px lift</button>
  </section>

  <section class="block">
    <h2>Focus</h2>
    <p class="caption">
      Keyboard focus draws a 2px accent ring 2px away from the control, with a cream inner ring so it
      stays visible over both surfaces. The ring is never removed and never replaced by colour change
      alone. Press <kbd>Tab</kbd> through this section to see it.
    </p>
    <div class="focus-row">
      <button type="button" class="demo-btn">A button</button>
      <a href="#gallery" class="focus-link">A link</a>
      <input class="focus-input" type="text" value="An input" aria-label="Focus demonstration input" />
    </div>
  </section>

  <section class="block">
    <h2>Backdrop</h2>
    <p class="caption">
      The page is sand with the 27px blue grid faded in at the top; raised content sits on cream. There
      is no second background colour and no gradient behind text.
    </p>
    <div class="backdrop-note">
      <p class="num">--bg-fade-0 → --bg-fade-50</p>
    </div>
  </section>
</div>

<style>
  .block {
    display: grid;
    gap: var(--space-4);
  }

  h2 {
    font-family: var(--font-heading);
    font-size: var(--step-3);
    line-height: var(--lh-tight);
    color: var(--c-text-strong);
    margin: 0;
  }

  h3 {
    font-family: var(--font-heading);
    font-size: var(--step-1);
    color: var(--c-text-strong);
    margin: 0;
  }

  .caption {
    margin: 0;
    max-inline-size: 76ch;
    color: var(--c-text-muted);
    line-height: var(--lh-body);
  }

  .caption code {
    font-family: var(--font-mono);
    font-size: 0.92em;
    background: var(--tint-accent);
    padding: 0 var(--space-1);
    border-radius: var(--radius-code);
  }

  .swatches {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-3);
    grid-template-columns: repeat(auto-fit, minmax(19rem, 1fr));
  }

  .swatch {
    display: grid;
    grid-template-columns: 4.5rem 1fr;
    gap: var(--space-3);
    align-items: start;
    padding: var(--space-3);
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
  }

  .chip-face {
    display: block;
    block-size: 4.5rem;
    box-shadow: var(--ring);
  }

  .swatch-body {
    display: grid;
    gap: var(--space-1);
    min-inline-size: 0;
  }

  .swatch-body code {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    color: var(--c-text-accent-strong);
  }

  .value {
    font-size: var(--step--1);
    color: var(--c-text);
  }

  .use {
    font-size: var(--step--1);
    color: var(--c-text-muted);
    line-height: var(--lh-body);
  }

  .ratio {
    font-size: var(--step--2);
    color: var(--c-text-muted);
    font-weight: var(--fw-semibold);
  }

  table.plain {
    border-collapse: collapse;
    inline-size: 100%;
    max-inline-size: 34rem;
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
  }

  table.plain th,
  table.plain td {
    text-align: start;
    padding: var(--space-2) var(--space-3);
    border-block-end: 1px solid var(--c-rule);
    font-size: var(--step--1);
  }

  table.plain thead th {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    border-block-end: 2px solid var(--c-rule-strong);
  }

  .families {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-3);
  }

  .families li {
    display: grid;
    gap: var(--space-1);
    padding-block-end: var(--space-3);
    border-block-end: 1px solid var(--c-rule);
  }

  .fam {
    font-size: var(--step-2);
    color: var(--c-text-strong);
    line-height: var(--lh-tight);
  }

  .scale {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-4);
  }

  .scale li {
    display: grid;
    gap: var(--space-1);
    border-block-end: 1px solid var(--c-rule);
    padding-block-end: var(--space-3);
  }

  .sample {
    margin: 0;
    font-family: var(--font-heading);
    font-weight: var(--fw-semibold);
    line-height: var(--lh-tight);
    color: var(--c-text-strong);
    letter-spacing: var(--ls-tight);
    overflow-wrap: anywhere;
  }

  .scale-meta {
    margin: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    align-items: baseline;
  }

  .scale-meta code {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    color: var(--c-text-accent-strong);
    font-weight: var(--fw-bold);
  }

  .elev {
    display: grid;
    gap: var(--space-6);
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    padding-block-end: var(--space-2);
  }

  .elev-item {
    margin: 0;
    display: grid;
    gap: var(--space-3);
    justify-items: center;
    text-align: center;
  }

  .elev-face {
    display: grid;
    place-items: center;
    inline-size: 8rem;
    block-size: 6rem;
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    transition: box-shadow var(--dur-base) var(--ease);
  }

  .elev-face.hero {
    box-shadow: var(--shadow-hero);
  }

  .elev-face.inset {
    background: var(--tint-accent);
    box-shadow: var(--shadow-inset);
  }

  .elev-face[data-state="pressed"] {
    box-shadow: var(--ring), var(--offset-hover);
    translate: var(--lift);
  }

  .elev-item figcaption {
    font-size: var(--step--2);
    color: var(--c-text-muted);
    line-height: var(--lh-body);
  }

  .elev-item figcaption code {
    display: block;
    font-family: var(--font-mono);
    color: var(--c-text-accent-strong);
    font-weight: var(--fw-bold);
  }

  .demo-btn {
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-4);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
    background: var(--c-surface);
    border: 2px solid var(--c-control-border);
    box-shadow: var(--shadow-card);
    cursor: pointer;
    transition:
      box-shadow var(--dur-base) var(--ease),
      translate var(--dur-base) var(--ease);
  }

  .demo-btn:hover,
  .demo-btn[data-lifted="true"] {
    box-shadow: var(--ring), var(--offset-hover);
    translate: var(--lift);
  }

  .demo-btn:focus-visible {
    outline: none;
    box-shadow: var(--focus-shadow);
  }

  .lift {
    justify-self: start;
  }

  .spacing {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--space-2);
  }

  .spacing li {
    display: grid;
    grid-template-columns: 7rem 1fr 3rem;
    gap: var(--space-3);
    align-items: center;
  }

  .spacing code {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    color: var(--c-text-accent-strong);
    font-weight: var(--fw-bold);
  }

  .bar {
    display: block;
    block-size: 0.75rem;
    background: var(--c-blue);
  }

  .px {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .grid-demo {
    block-size: 8rem;
    background-image:
      linear-gradient(to right, var(--grid-line) 1px, transparent 1px),
      linear-gradient(to bottom, var(--grid-line) 1px, transparent 1px);
    background-size: var(--grid-cell) var(--grid-cell);
    background-color: var(--c-sand);
    box-shadow: var(--shadow-inset);
  }

  .focus-row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-5);
    align-items: center;
  }

  .focus-link {
    color: var(--c-text-accent);
    font-family: var(--font-body);
    min-block-size: var(--touch-target);
    min-inline-size: var(--touch-target);
    justify-content: center;
    display: inline-flex;
    align-items: center;
    text-decoration: underline;
    text-underline-offset: 3px;
  }

  .focus-input {
    min-block-size: var(--touch-target);
    padding-inline: var(--space-3);
    font-family: var(--font-mono);
    font-size: var(--step-0);
    color: var(--c-text);
    background: var(--c-surface);
    border: 2px solid var(--c-control-border);
  }

  kbd {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    border: 1px solid var(--c-control-border);
    padding: 0 var(--space-1);
  }

  .backdrop-note {
    padding: var(--space-4);
    background: var(--c-surface);
    box-shadow: var(--shadow-inset);
  }

  .backdrop-note p {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
  }
</style>
