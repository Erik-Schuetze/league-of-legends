<script lang="ts">
  interface Option {
    value: string;
    label: string;
    /** Right-aligned note: role, region, row count. */
    note?: string;
  }

  interface Props {
    label: string;
    options: Option[];
    value: string;
    onchange?: (value: string) => void;
    placeholder?: string;
    /** Shown when the query matches nothing. */
    emptyMessage?: string;
    id?: string;
  }

  let {
    label,
    options,
    value = $bindable(),
    onchange,
    placeholder = "Type to search",
    emptyMessage = "No match",
    id = undefined,
  }: Props = $props();

  const inputId = $derived(id ?? `combo-${label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`);
  const listId = $derived(`${inputId}-listbox`);

  let query = $state("");
  let open = $state(false);
  let active = $state(0);

  const selected = $derived(options.find((o) => o.value === value));
  const filtered = $derived(
    query.trim()
      ? options.filter((o) =>
          o.label.toLowerCase().includes(query.trim().toLowerCase()),
        )
      : options,
  );

  function choose(option: Option) {
    value = option.value;
    query = "";
    open = false;
    onchange?.(option.value);
  }

  function onkeydown(event: KeyboardEvent) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      open = true;
      const step = event.key === "ArrowDown" ? 1 : -1;
      active = (active + step + filtered.length) % Math.max(filtered.length, 1);
      return;
    }
    if (event.key === "Enter" && open && filtered[active]) {
      event.preventDefault();
      choose(filtered[active]);
      return;
    }
    if (event.key === "Escape") {
      open = false;
      return;
    }
    if (event.key === "Home") {
      active = 0;
    }
  }
</script>

<div class="field">
  <label for={inputId}>{label}</label>
  <div class="control">
    <input
      id={inputId}
      type="text"
      role="combobox"
      autocomplete="off"
      aria-expanded={open}
      aria-controls={listId}
      aria-autocomplete="list"
      aria-activedescendant={open && filtered[active] ? `${listId}-${active}` : undefined}
      placeholder={selected ? selected.label : placeholder}
      bind:value={query}
      onfocus={() => (open = true)}
      oninput={() => {
        open = true;
        active = 0;
      }}
      onkeydown={onkeydown}
      onblur={() => setTimeout(() => (open = false), 120)}
    />
    <span class="mark" aria-hidden="true">▾</span>
  </div>

  {#if open}
    <ul class="listbox" id={listId} role="listbox" aria-label={label}>
      {#each filtered as option, i (option.value)}
        <li
          id={`${listId}-${i}`}
          role="option"
          aria-selected={option.value === value}
          data-active={i === active}
          onmousedown={(e) => {
            e.preventDefault();
            choose(option);
          }}
          onmouseenter={() => (active = i)}
        >
          <span class="grow">{option.label}</span>
          {#if option.note}
            <span class="note num">{option.note}</span>
          {/if}
        </li>
      {:else}
        <li class="empty">{emptyMessage}</li>
      {/each}
    </ul>
  {/if}

  {#if selected}
    <p class="choice">
      <span class="choice-label">Selected</span>
      <span class="mono">{selected.label}</span>
    </p>
  {/if}
</div>

<style>
  .field {
    position: relative;
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
    background: var(--c-surface);
    box-shadow: var(--ring);
  }

  input {
    inline-size: 100%;
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-7) var(--space-2) var(--space-3);
    background: none;
    border: 0;
    font-family: var(--font-mono);
    font-size: var(--step--1);
    font-weight: var(--fw-bold);
  }

  input:focus-visible { outline: none; }
  .control:focus-within { box-shadow: var(--focus-shadow); }

  .mark {
    position: absolute;
    inset-inline-end: var(--space-3);
    color: var(--c-blue);
    pointer-events: none;
    font-size: 0.75em;
  }

  .listbox {
    position: absolute;
    z-index: 25;
    inset-inline: 0;
    margin-block-start: var(--space-1);
    max-block-size: 18rem;
    overflow: auto;
    background: var(--c-surface);
    box-shadow: var(--shadow-card);
    padding: 0;
    list-style: none;
  }

  .listbox li {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-3);
    font-size: var(--step--1);
    cursor: pointer;
  }

  .listbox li[data-active="true"] {
    background: var(--tint-accent-strong);
  }

  .listbox li[aria-selected="true"] {
    font-weight: var(--fw-semibold);
    box-shadow: inset 4px 0 0 var(--c-blue);
  }

  .note {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .empty {
    color: var(--c-text-muted);
    cursor: default;
  }

  .choice {
    margin-block-start: var(--space-2);
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  .choice-label {
    font-family: var(--font-mono);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    margin-inline-end: var(--space-2);
  }
</style>
