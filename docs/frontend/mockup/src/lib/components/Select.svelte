<script lang="ts">
  interface Option {
    value: string;
    label: string;
    /** Dimmed and unselectable. Use for partitions with no published data,
     *  never for a value the user may want to reach. */
    disabled?: boolean;
    group?: string;
  }

  interface Props {
    /** Visible label. A select without a visible label is a defect. */
    label: string;
    options: Option[];
    value: string;
    onchange?: (value: string) => void;
    id?: string;
    hint?: string;
    /** Rendered as a note under the control when the current choice has a
     *  caveat, e.g. a partition with withheld cells. */
    note?: string;
  }

  let {
    label,
    options,
    value = $bindable(),
    onchange,
    id = undefined,
    hint = undefined,
    note = undefined,
  }: Props = $props();

  /* Two selects can share a label ("Region" appears in the filter bar and in the
     table controls), so the id needs a per-instance suffix to stay unique. */
  const uid = $props.id();
  const inputId = $derived(id ?? `select-${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}-${uid}`);

  const groups = $derived(
    options.reduce<Record<string, Option[]>>((acc, option) => {
      const key = option.group ?? "";
      (acc[key] ??= []).push(option);
      return acc;
    }, {}),
  );
</script>

<div class="field">
  <label for={inputId}>{label}</label>
  <div class="control">
    <select
      id={inputId}
      bind:value
      onchange={() => onchange?.(value)}
      aria-describedby={hint || note ? `${inputId}-hint` : undefined}
    >
      {#each Object.entries(groups) as [group, opts] (group)}
        {#if group}
          <optgroup label={group}>
            {#each opts as option (option.value)}
              <option value={option.value} disabled={option.disabled}>{option.label}</option>
            {/each}
          </optgroup>
        {:else}
          {#each opts as option (option.value)}
            <option value={option.value} disabled={option.disabled}>{option.label}</option>
          {/each}
        {/if}
      {/each}
    </select>
    <span class="caret" aria-hidden="true">▾</span>
  </div>
  {#if hint || note}
    <p class="hint" id={`${inputId}-hint`}>{note ?? hint}</p>
  {/if}
</div>

<style>
  .field {
    display: block;
  }

  label {
    display: block;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    margin-block-end: var(--space-2);
  }

  .control {
    position: relative;
    display: flex;
    align-items: center;
    background: var(--c-surface);
    box-shadow: var(--ring);
  }

  select {
    appearance: none;
    inline-size: 100%;
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-7) var(--space-2) var(--space-3);
    background: none;
    border: 0;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
    color: var(--c-text-strong);
    cursor: pointer;
  }

  select:focus-visible {
    /* The ring belongs to the wrapper so it wraps the caret too. */
    outline: none;
  }

  .control:focus-within {
    box-shadow: var(--focus-shadow);
  }

  .caret {
    position: absolute;
    inset-inline-end: var(--space-3);
    color: var(--c-blue);
    pointer-events: none;
    font-size: 0.75em;
  }

  .hint {
    margin-block-start: var(--space-2);
    font-size: var(--step--2);
    color: var(--c-text-muted);
    max-width: var(--measure-narrow);
  }
</style>
