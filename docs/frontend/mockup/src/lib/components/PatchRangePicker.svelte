<script lang="ts">
  interface Props {
    /** Patches the build actually published, newest first. */
    available: string[];
    /** Inclusive selection. A single patch is a range of one. */
    from: string;
    to: string;
    onchange?: (from: string, to: string) => void;
  }

  let { available, from = $bindable(), to = $bindable(), onchange }: Props = $props();

  const oldest = $derived(available[available.length - 1]);
  const count = $derived(available.filter((p) => p >= from && p <= to).length);

  /* Reverse-chronological string compare is safe for patch labels like
     16.18 / 16.17 while they stay zero-padded two-part versions. */
  const bounds = $derived(
    available.length
      ? {
          min: available[available.length - 1],
          max: available[0],
          indexOf: (p: string) => available.indexOf(p),
        }
      : undefined,
  );

  function set(fromValue: string, toValue: string) {
    if (bounds && bounds.indexOf(fromValue) > bounds.indexOf(toValue)) {
      [fromValue, toValue] = [toValue, fromValue];
    }
    from = fromValue;
    to = toValue;
    onchange?.(from, to);
  }
</script>

<fieldset class="range">
  <legend>Patch range</legend>
  <div class="row">
    <label>
      <span>From</span>
      <select value={from} onchange={(e) => set(e.currentTarget.value, to)}>
        {#each available as patch (patch)}
          <option value={patch}>{patch}</option>
        {/each}
      </select>
    </label>
    <span class="dash" aria-hidden="true">→</span>
    <label>
      <span>To</span>
      <select value={to} onchange={(e) => set(from, e.currentTarget.value)}>
        {#each [...available].reverse() as patch (patch)}
          <option value={patch}>{patch}</option>
        {/each}
      </select>
    </label>
  </div>
  <p class="note num">
    {count} of {available.length} patches published
    {#if oldest && from === oldest && to === available[0]}· full history{/if}
  </p>
</fieldset>

<style>
  .range {
    margin: 0;
    padding: 0;
    border: 0;
  }

  legend {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    padding: 0;
    margin-block-end: var(--space-2);
  }

  .row {
    display: flex;
    align-items: end;
    gap: var(--space-2);
  }

  label {
    display: block;
  }

  label > span {
    display: block;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    color: var(--c-text-muted);
    margin-block-end: var(--space-1);
  }

  select {
    appearance: none;
    min-block-size: var(--touch-target);
    min-inline-size: 5.5rem;
    padding: var(--space-2) var(--space-5) var(--space-2) var(--space-2);
    background:
      linear-gradient(45deg, transparent 50%, var(--c-blue) 50%) calc(100% - 1rem) 55% / 6px 6px no-repeat,
      linear-gradient(135deg, var(--c-blue) 50%, transparent 50%) calc(100% - 0.625rem) 55% / 6px 6px no-repeat,
      var(--c-surface);
    border: 0;
    box-shadow: var(--ring);
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    cursor: pointer;
  }

  .dash {
    padding-block-end: var(--space-3);
    color: var(--c-text-muted);
  }

  .note {
    margin-block-start: var(--space-2);
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }
</style>
