<script lang="ts">
  import Popover from "./Popover.svelte";
  import Checkbox from "./Checkbox.svelte";

  interface Option {
    value: string;
    label: string;
    hint?: string;
    count?: number;
  }

  interface Props {
    label: string;
    options: Option[];
    value: string[];
    onchange?: (value: string[]) => void;
    /** Shown when nothing is selected. */
    placeholder?: string;
  }

  let {
    label,
    options,
    value = $bindable([]),
    onchange,
    placeholder = "Any",
  }: Props = $props();

  const toggle = (option: string, checked: boolean) => {
    value = checked ? [...value, option] : value.filter((v) => v !== option);
    onchange?.(value);
  };

  const summary = $derived(
    value.length ? `${value.length} selected` : placeholder,
  );

  const uid = $props.id();
  const groupId = $derived(`multiselect-${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}-${uid}`);
  const clear = () => {
    value = [];
    onchange?.(value);
  };

  const selectedLabels = $derived(
    options.filter((o) => value.includes(o.value)).map((o) => o.label),
  );
</script>

<div class="field">
  <p class="label" id={groupId}>{label}</p>
  <Popover label={summary} align="start">
    <div class="panel-body" role="group" aria-labelledby={groupId}>
      {#each options as option (option.value)}
        <Checkbox
          label={option.label}
          hint={option.hint}
          count={option.count}
          checked={value.includes(option.value)}
          onchange={(checked) => toggle(option.value, checked)}
        />
      {/each}
      <div class="footer">
        <button type="button" class="clear" onclick={clear}>
          Clear selection
        </button>
      </div>
    </div>
  </Popover>
  {#if selectedLabels.length}
    <p class="chosen">{selectedLabels.join(", ")}</p>
  {/if}
</div>

<style>
  .label {
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-muted);
    margin-block-end: var(--space-2);
  }

  .panel-body {
    min-inline-size: 16rem;
    max-block-size: 22rem;
    overflow: auto;
  }

  .footer {
    border-block-start: 2px solid var(--c-rule);
    margin-block-start: var(--space-2);
    padding-block-start: var(--space-2);
  }

  .clear {
    min-block-size: var(--touch-target);
    background: none;
    border: 0;
    padding: 0;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    color: var(--c-text-accent);
    text-decoration: underline;
    cursor: pointer;
  }

  /* The chosen values stay visible outside the panel: a closed multi-select
     must still say what it is filtering on. */
  .chosen {
    margin-block-start: var(--space-2);
    font-size: var(--step--2);
    color: var(--c-text-muted);
    max-width: var(--measure-narrow);
  }
</style>
