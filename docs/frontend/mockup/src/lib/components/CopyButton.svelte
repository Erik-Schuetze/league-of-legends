<script lang="ts">
  interface Props {
    /** The text placed on the clipboard. */
    value: string;
    /** What is being copied, for the accessible name and the confirmation. */
    label?: string;
    /** "icon" for table rows and code blocks, "button" where there is room. */
    variant?: "button" | "icon";
  }

  let { value, label = "value", variant = "button" }: Props = $props();

  let copied = $state(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      /* Clipboard access can be denied. Fall back to selecting the text so the
         visitor can still copy it by hand. */
      const area = document.createElement("textarea");
      area.value = value;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      document.execCommand("copy");
      document.body.removeChild(area);
    }
    copied = true;
    setTimeout(() => (copied = false), 2000);
  }
</script>

{#if variant === "icon"}
  <button type="button" class="copy icon" onclick={copy} title={`Copy ${label}`}>
    <span aria-hidden="true">{copied ? "✓" : "⧉"}</span>
    <span class="visually-hidden">{copied ? `${label} copied` : `Copy ${label}`}</span>
  </button>
{:else}
  <button type="button" class="copy" onclick={copy} aria-live="polite">
    <span aria-hidden="true">{copied ? "✓" : "⧉"}</span>
    {copied ? "Copied" : `Copy ${label}`}
  </button>
{/if}

<style>
  .copy {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    min-block-size: var(--touch-target);
    padding: var(--space-2) var(--space-3);
    background: var(--c-surface);
    box-shadow: var(--ring);
    border: 0;
    font-family: var(--font-mono);
    font-size: var(--step--2);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    text-transform: uppercase;
    color: var(--c-text-accent-strong);
    cursor: pointer;
    white-space: nowrap;
  }

  .copy:hover {
    background: var(--tint-accent-strong);
  }

  .copy.icon {
    inline-size: var(--touch-target);
    padding: 0;
    font-size: var(--step-0);
  }

  /* Confirmation is a permanent state on the control itself, not a toast that
     disappears while the visitor is elsewhere. */
  .copy[aria-live] span[aria-hidden] {
    font-size: var(--step-0);
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
