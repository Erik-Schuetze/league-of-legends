<script lang="ts">
  /** The frozen Role enum from docs/contracts.md. Shown verbatim: roles are
   *  compared against the data contract, so they are never abbreviated. */
  export type Role = "TOP" | "JUNGLE" | "MID" | "BOTTOM" | "SUPPORT";

  interface Props {
    role: Role;
    /** Use "glyph" in dense tables, where the column header carries the words. */
    variant?: "word" | "glyph";
    size?: "sm" | "md";
  }

  let { role, variant = "word", size = "md" }: Props = $props();

  /* One letter each, as a scanning aid beside the word - never instead of it. */
  const glyph: Record<Role, string> = {
    TOP: "T",
    JUNGLE: "J",
    MID: "M",
    BOTTOM: "B",
    SUPPORT: "S",
  };
</script>

<span class="role" data-size={size} data-variant={variant}>
  {#if variant === "glyph"}
    <span class="visually-hidden">{role}</span>
    <span aria-hidden="true">{glyph[role]}</span>
  {:else}
    {role}
  {/if}
</span>

<style>
  .role {
    display: inline-block;
    font-family: var(--font-mono);
    font-weight: var(--fw-bold);
    letter-spacing: var(--ls-wide);
    color: var(--c-text-strong);
    white-space: nowrap;
  }

  .role[data-size="sm"] {
    font-size: var(--step--2);
  }

  .role[data-size="md"] {
    font-size: var(--step--1);
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
