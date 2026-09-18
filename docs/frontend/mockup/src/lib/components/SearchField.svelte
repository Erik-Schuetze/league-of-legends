<script lang="ts">
  interface Props {
    label: string;
    value?: string;
    placeholder?: string;
    id?: string;
    /** Debounce in ms before `onsearch` fires. 0 disables debouncing. */
    debounce?: number;
    onsearch?: (value: string) => void;
  }

  let {
    label,
    value = $bindable(""),
    placeholder = "Search",
    id = undefined,
    debounce = 200,
    onsearch,
  }: Props = $props();

  const inputId = $derived(id ?? `search-${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`);
  let timer: ReturnType<typeof setTimeout>;

  function oninput() {
    if (!onsearch) return;
    clearTimeout(timer);
    if (debounce === 0) {
      onsearch(value);
      return;
    }
    timer = setTimeout(() => onsearch(value), debounce);
  }
</script>

<div class="search">
  <label for={inputId}>{label}</label>
  <div class="control">
    <span class="glyph" aria-hidden="true">⌕</span>
    <input
      id={inputId}
      type="search"
      autocomplete="off"
      {placeholder}
      bind:value
      {oninput}
    />
    {#if value}
      <button
        type="button"
        class="clear"
        aria-label="Clear search"
        onclick={() => {
          value = "";
          onsearch?.("");
        }}
      ><span aria-hidden="true">×</span></button>
    {/if}
  </div>
</div>

<style>
  .search {
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
    display: flex;
    align-items: center;
    gap: var(--space-2);
    background: var(--c-surface);
    box-shadow: var(--ring);
    padding-inline-start: var(--space-3);
  }

  .control:focus-within {
    box-shadow: var(--focus-shadow);
  }

  .glyph {
    color: var(--c-text-muted);
    font-size: var(--step-0);
  }

  input {
    flex: 1 1 auto;
    min-inline-size: 0;
    min-block-size: var(--touch-target);
    padding: var(--space-2) 0;
    background: none;
    border: 0;
    font-size: var(--step--1);
  }

  input:focus-visible { outline: none; }

  /* Remove the platform magnifier: we draw our own. */
  input::-webkit-search-decoration,
  input::-webkit-search-cancel-button {
    appearance: none;
  }

  .clear {
    inline-size: var(--touch-target);
    block-size: var(--touch-target);
    display: grid;
    place-items: center;
    background: none;
    border: 0;
    cursor: pointer;
    color: var(--c-text-muted);
    font-size: var(--step-1);
    line-height: 1;
  }
</style>
