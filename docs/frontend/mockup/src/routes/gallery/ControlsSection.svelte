<script lang="ts">
  import Specimen from "./Specimen.svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Chip from "$lib/components/Chip.svelte";
  import Combobox from "$lib/components/Combobox.svelte";
  import CopyButton from "$lib/components/CopyButton.svelte";
  import ExportMenu from "$lib/components/ExportMenu.svelte";
  import FilterBar from "$lib/components/FilterBar.svelte";
  import FilterChip from "$lib/components/FilterChip.svelte";
  import IconButton from "$lib/components/IconButton.svelte";
  import MultiSelect from "$lib/components/MultiSelect.svelte";
  import PatchRangePicker from "$lib/components/PatchRangePicker.svelte";
  import RadioGroup from "$lib/components/RadioGroup.svelte";
  import RangeSlider from "$lib/components/RangeSlider.svelte";
  import SearchField from "$lib/components/SearchField.svelte";
  import SegmentedControl from "$lib/components/SegmentedControl.svelte";
  import Select from "$lib/components/Select.svelte";
  import Switch from "$lib/components/Switch.svelte";

  let role = $state("MID");
  let region = $state("EUW");
  let queue = $state("420");
  let bracket = $state("all");
  let roles = $state<string[]>(["MID", "SUPPORT"]);
  let champion = $state("Ahri");
  let query = $state("");
  let minGames = $state(500);
  let includeWithheld = $state(false);
  let density = $state("dense");
  let checkedRole = $state(true);
  let patchFrom = $state("16.18");
  let patchTo = $state("16.17");
  let exportTone = $state("");

  const regionOptions = [
    { value: "EUW", label: "EU West" },
    { value: "EUNE", label: "EU Nordic & East" },
    { value: "NA", label: "North America" },
    { value: "KR", label: "Korea" },
    { value: "BR", label: "Brazil" },
    { value: "OCE", label: "Oceania", disabled: true }
  ];

  const bracketOptions = [
    { value: "all", label: "All ranks" },
    { value: "gold_plus", label: "Gold and above" },
    { value: "diamond_plus", label: "Diamond and above" }
  ];

  const championOptions = [
    { value: "Ahri", label: "Ahri", note: "MID" },
    { value: "Aatrox", label: "Aatrox", note: "TOP" },
    { value: "Alistar", label: "Alistar", note: "SUPPORT" },
    { value: "Amumu", label: "Amumu", note: "JUNGLE" },
    { value: "Ashe", label: "Ashe", note: "BOTTOM" },
    { value: "Aurelion Sol", label: "Aurelion Sol", note: "MID" }
  ];

  const roleOptions = [
    { value: "TOP", label: "Top", hint: "141 champion-role cells" },
    { value: "JUNGLE", label: "Jungle", hint: "141 champion-role cells" },
    { value: "MID", label: "Mid", hint: "141 champion-role cells" },
    { value: "BOTTOM", label: "Bottom", hint: "141 champion-role cells" },
    { value: "SUPPORT", label: "Support", hint: "141 champion-role cells" }
  ];
</script>

<div class="stack stack-8">
  <Specimen name="Button.svelte" state="primary · secondary · ghost · danger - md">
    <div class="cluster">
      <Button variant="primary">Open champion</Button>
      <Button variant="secondary">Compare</Button>
      <Button variant="ghost">Copy link</Button>
      <Button variant="danger">Reset filters</Button>
    </div>
  </Specimen>

  <Specimen name="Button.svelte" state="sm · busy · disabled · as a link">
    <div class="cluster">
      <Button size="sm">Small</Button>
      <Button size="sm" variant="primary">Small primary</Button>
      <Button busy>Exporting…</Button>
      <Button disabled>Disabled</Button>
      <Button href="/explore/" variant="secondary">Navigates (anchor)</Button>
    </div>
    <p class="use">
      One primary per view. <code>busy</code> keeps the label and blocks the click; a disabled button is
      for a prerequisite that is visibly unmet, never for a slow request.
    </p>
  </Specimen>

  <Specimen name="IconButton.svelte" state="md · sm · pressed · with hint · disabled">
    <div class="cluster">
      <IconButton label="Copy view link" hint="Copy">⧉</IconButton>
      <IconButton label="Columns" size="sm">▤</IconButton>
      <IconButton label="Pin comparison" pressed={true}>★</IconButton>
      <IconButton label="Pin comparison" pressed={false}>☆</IconButton>
      <IconButton label="Unavailable action" disabled>⨯</IconButton>
    </div>
    <p class="use">Every instance carries an accessible name; the glyph is decoration.</p>
  </Specimen>

  <Specimen name="SegmentedControl.svelte" state="three options · with counts">
    <div class="stack stack-4">
      <SegmentedControl
        options={[
          { value: "table", label: "Table" },
          { value: "heatmap", label: "Heatmap" },
          { value: "both", label: "Both" }
        ]}
        value={density}
        onchange={(v) => (density = v)}
        name="view-layout"
        label="View layout"
      />
      <SegmentedControl
        options={[
          { value: "dense", label: "Dense", count: 141 },
          { value: "roomy", label: "Roomy", count: 141 },
          { value: "withheld", label: "Withheld", count: 3 }
        ]}
        value={density}
        onchange={(v) => (density = v)}
        name="row-density"
        label="Row density"
      />
    </div>
  </Specimen>

  <Specimen name="Checkbox.svelte" state="unchecked · checked · with hint · with count · disabled">
    <div class="stack stack-3">
      <Checkbox bind:checked={checkedRole} label="Show role split" hint="Adds one row per role a champion is played in." />
      <Checkbox label="Include withheld cells" hint="Adds rows with too few games to publish, labelled as withheld." count={3} />
      <Checkbox label="Pin to comparison tray" checked={false} count={128} />
      <Checkbox label="Bracket filter" hint="Not available while all ranks are selected." disabled />
    </div>
  </Specimen>

  <Specimen name="RadioGroup.svelte" state="inline · stacked · with hints">
    <div class="grid">
      <RadioGroup name="role-inline" label="Primary role" options={roleOptions.slice(0, 3)} bind:value={role} />
      <RadioGroup name="role-stacked" label="Bracket" options={bracketOptions} bind:value={bracket} stacked />
    </div>
  </Specimen>

  <Specimen name="Switch.svelte" state="off · on · with two labels · disabled">
    <div class="stack stack-3">
      <Switch
        bind:checked={includeWithheld}
        label="Show withheld cells"
        hint="Withheld rows are labelled, never rendered as zero."
        onLabel="shown"
        offLabel="hidden"
      />
      <Switch checked={true} label="Tabular numerals" hint="Never disabled in a data view." onLabel="on" offLabel="off" />
      <Switch label="Live updates" hint="The artifact is a published snapshot; there is nothing to stream." disabled />
    </div>
  </Specimen>

  <Specimen name="Select.svelte" state="native · with hint · with note · disabled option">
    <div class="grid">
      <Select label="Region" options={regionOptions} bind:value={region} hint="Partition in the published artifact." />
      <Select label="Queue" options={[{ value: "420", label: "Ranked Solo/Duo" }, { value: "440", label: "Ranked Flex" }]} bind:value={queue} />
      <Select
        label="Bracket"
        options={bracketOptions}
        bind:value={bracket}
        note="3 cells are withheld in this partition."
      />
    </div>
  </Specimen>

  <Specimen name="MultiSelect.svelte" state="two selected · empty · with hints and counts">
    <div class="grid">
      <MultiSelect label="Roles" options={roleOptions} bind:value={roles} />
      <MultiSelect
        label="Regions"
        options={[
          { value: "EUW", label: "EU West", count: 141 },
          { value: "NA", label: "North America", count: 0, hint: "No published partition" },
          { value: "KR", label: "Korea", count: 141 }
        ]}
        value={[]}
      />
    </div>
  </Specimen>

  <Specimen name="Combobox.svelte" state="closed · typing · no match">
    <div class="grid">
      <Combobox label="Champion" options={championOptions} bind:value={champion} placeholder="Type a champion name" />
      <Combobox
        label="Champion (empty result)"
        options={championOptions}
        value=""
        placeholder="Try zzz"
        emptyMessage="No champion matches that name"
      />
    </div>
  </Specimen>

  <Specimen name="SearchField.svelte" state="empty · with value · debounced">
    <div class="grid">
      <SearchField label="Search cells" bind:value={query} placeholder="Champion, role or tier" />
      <SearchField label="Search (no debounce)" value="Aatrox" debounce={0} />
    </div>
    <p class="use">Announces the result count on change; it does not steal focus or reload the view.</p>
  </Specimen>

  <Specimen name="RangeSlider.svelte" state="minimum sample · win-rate band · stepped">
    <div class="grid">
      <RangeSlider
        label="Minimum games per cell"
        bind:value={minGames}
        min={0}
        max={5000}
        step={100}
        format={(v) => `${v.toLocaleString("en-US")} games`}
        hint="The artifact's own minimum is 500. Raising it narrows the table."
      />
      <RangeSlider
        label="Recommended pick rate"
        value={5}
        min={0}
        max={20}
        step={0.5}
        format={(v) => `${v.toFixed(1)}%`}
      />
    </div>
  </Specimen>

  <Specimen name="Chip.svelte" state="neutral · accent · up · down · unknown · sm">
    <div class="cluster cluster-2">
      <Chip label="Patch" value="16.18" />
      <Chip label="EUW" tone="accent" value="141" />
      <Chip label="Win +1.2" tone="up" value="53.1%" />
      <Chip label="Win −0.8" tone="down" value="47.9%" />
      <Chip label="Withheld" tone="unknown" value="3" />
      <Chip label="Min games" tone="neutral" size="sm" value="500" />
    </div>
    <p class="use">
      Tone never travels alone: the label says win, loss or withheld, and the value carries the sign.
    </p>
  </Specimen>

  <Specimen name="FilterChip.svelte" state="removable · non-removable">
    <div class="cluster cluster-2">
      <FilterChip label="Role" value="MID" onremove={() => {}} />
      <FilterChip label="Region" value="EU West" onremove={() => {}} />
      <FilterChip label="Patch" value="16.18 → 16.17" onremove={() => {}} />
      <FilterChip label="Queue" value="Ranked Solo" />
    </div>
  </Specimen>

  <Specimen
    name="FilterBar.svelte"
    state="controls · chips · summary · sticky"
    note="On small viewports the control row moves into a drawer and the chips stay on the page."
  >
    <FilterBar sticky={false}>
      <SearchField label="Search champion" placeholder="Champion" />
      <Select label="Region" options={regionOptions} bind:value={region} />
      <MultiSelect label="Roles" options={roleOptions} bind:value={roles} />
      {#snippet chips()}
        <FilterChip label="Region" value="EU West" onremove={() => {}} />
        <FilterChip label="Role" value="MID" onremove={() => {}} />
        <FilterChip label="Role" value="SUPPORT" onremove={() => {}} />
        <Button variant="ghost" size="sm">Clear all</Button>
      {/snippet}
      {#snippet summary()}
        <p class="num">128 of 141 cells · patch 16.18 · n ≥ 500</p>
      {/snippet}
    </FilterBar>
  </Specimen>

  <Specimen name="PatchRangePicker.svelte" state="two-patch range · single patch">
    <div class="stack stack-4">
      <PatchRangePicker available={["16.18", "16.17", "16.16", "16.15"]} bind:from={patchFrom} bind:to={patchTo} />
      <PatchRangePicker available={["16.18", "16.17"]} from="16.18" to="16.18" />
    </div>
  </Specimen>

  <Specimen name="CopyButton.svelte" state="icon · button · confirmed">
    <div class="cluster">
      <CopyButton value="https://example.invalid/explore/?patch=16.18&region=EUW&queue=420" label="view link" variant="icon" />
      <CopyButton value="Aatrox,TOP,53.1,12.4,2.1,S+" label="row" variant="icon" />
      <CopyButton value="https://example.invalid/explore/" label="view link" />
    </div>
  </Specimen>

  <Specimen name="ExportMenu.svelte" state="trigger · menu · with footnote">
    <div class="stack stack-3">
      <ExportMenu
        scope="Champion cells · patch 16.18 · EUW · Ranked Solo · all ranks"
        filename="lolstats-cells-16.18-EUW-420-all"
        rowCount={141}
        onexport={(id) => (exportTone = id)}
        footnote="Exports carry the patch, region, queue, bracket, source window and the minimum sample."
      />
      {#if exportTone}<p class="use num">onexport("&#123;{exportTone}&#125;")</p>{/if}
    </div>
  </Specimen>
</div>

<style>
  .grid {
    display: grid;
    gap: var(--space-5);
    grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
  }

  .use {
    margin: 0;
    font-size: var(--step--1);
    color: var(--c-text-muted);
    max-inline-size: 76ch;
  }

  .use code {
    font-family: var(--font-mono);
    background: var(--tint-accent);
    padding: 0 var(--space-1);
  }
</style>
