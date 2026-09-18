<script lang="ts">
  import type { Snippet } from "svelte";
  import "../app.css";
  import Banner from "$lib/components/Banner.svelte";
  import ProvenanceLine from "$lib/components/ProvenanceLine.svelte";
  import SiteNav from "$lib/components/SiteNav.svelte";
  import SiteFooter from "$lib/components/SiteFooter.svelte";
  import { coverage } from "$lib/data/demo.generated";

  interface Props {
    children: Snippet;
  }

  let { children }: Props = $props();

  const buildStamp = `${coverage.patch} · ${coverage.region} · ${coverage.queue} · ${coverage.bracket} · generated ${coverage.generated_at}`;
</script>

<a class="skip-link" href="#main">Skip to content</a>

<SiteNav />

<main id="main" tabindex="-1">
  <div class="shell">
    <!-- Two notices sit above every page. The banner says what the build is,
         the provenance line says where its numbers came from: a visitor who
         lands mid-site never has to infer either. -->
    <div class="notices">
      <Banner tone="info">
        This is the design mockup for the rebuilt frontend. It renders committed demo artifacts, not live
        match data, and requests nothing outside this origin.
      </Banner>
      <ProvenanceLine source={coverage.source} detail="committed fixture artifacts · no live ingest" />
    </div>
    {@render children()}
  </div>
</main>

<SiteFooter {buildStamp} />

<style>
  main {
    /* Clears the sticky nav so anchor jumps and focus land below it. */
    scroll-margin-block-start: var(--nav-height);
  }

  .notices {
    display: grid;
    gap: var(--space-3);
    padding-block: var(--space-5) 0;
  }
</style>
