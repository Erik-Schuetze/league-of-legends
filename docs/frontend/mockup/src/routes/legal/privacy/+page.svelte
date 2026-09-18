<script lang="ts">
  import Callout from "$lib/components/Callout.svelte";
  import PageHeader from "$lib/components/PageHeader.svelte";
  import ProvenanceLine from "$lib/components/ProvenanceLine.svelte";
  import SectionHeader from "$lib/components/SectionHeader.svelte";
  import Well from "$lib/components/Well.svelte";
  import { contactEmail, contactLine, effectiveLine, operatorLine } from "$lib/legal";
</script>

<svelte:head>
  <title>Privacy · LoL Stats</title>
  <meta name="description" content="What this site does with data about you: no accounts, no cookies, no third-party requests and no analytics." />
</svelte:head>

<div class="stack stack-7">
  <PageHeader
    eyebrow="Legal"
    title="Privacy"
    lede="What this site does with data about you. The short version: it collects none, and it sets no cookies."
  />

  <Callout tone="note" label="No accounts, no tracking">
    <p>
      There is no login, no account and no personalisation. The site serves the same bytes to everyone and
      cannot tell one visitor from another.
    </p>
  </Callout>

  <section class="section">
    <SectionHeader title="What is collected" level={2} />
    <ul class="statements">
      <li>
        <strong>Nothing you type is sent anywhere.</strong> Search fields, filters and sorting happen in your
        browser. Filter state is kept in the address bar so a view can be shared, which means it is part of
        the link you copy - not something the site stores.
      </li>
      <li>
        <strong>No cookies and no local storage identifiers.</strong> The site sets no cookie, and it stores
        no identifier that could recognise you on a return visit.
      </li>
      <li>
        <strong>No third-party scripts, fonts or pixels.</strong> Fonts are served from this origin, and the
        pages load nothing from any other host, so no third party sees a request from your browser because
        of this site.
      </li>
      <li>
        <strong>No analytics and no advertising.</strong> Visiting a page is not counted, profiled or sold.
      </li>
    </ul>
  </section>

  <section class="section">
    <SectionHeader title="What the server necessarily sees" level={2} />
    <div class="prose">
      <p>
        Serving a web page requires the host to receive the request, which includes your IP address and the
        resource you asked for. Those request logs exist for operating and securing the service and are kept
        for a short, fixed period, then deleted. They are not joined to anything else, not used to build a
        profile, and not shared.
      </p>
      <p>
        The same is true of any aggregate data the host may keep about traffic volumes: it is used to know
        whether the service is up, not to know who you are.
      </p>
    </div>
  </section>

  <section class="section">
    <SectionHeader title="Exports" level={2} />
    <div class="prose">
      <p>
        Exports are generated in your browser from data already loaded on the page, so an export never tells
        the server what you were looking at. The files carry the coverage stamp and nothing about you.
      </p>
    </div>
  </section>

  <section class="section">
    <SectionHeader title="Your rights" level={2} />
    <div class="prose">
      <p>
        Because no personal data is collected or stored beyond short-lived request logs, there is no profile
        to access, correct, export or erase. Rights under the GDPR still apply to the request logs: write to
        the mailbox below and the request will be answered, including by deletion where it is appropriate.
      </p>
    </div>
  </section>

  <section class="section">
    <SectionHeader title="Who is responsible" level={2} />
    <ul class="statements">
      <li>{operatorLine}</li>
      <li>No data protection officer is appointed, because no systematic monitoring or large-scale processing takes place.</li>
    </ul>
  </section>

  <Well tone="paper">
    <SectionHeader title="Contact" level={3} />
    <p>{contactLine}</p>
    <p><a href={`mailto:${contactEmail}`}>{contactEmail}</a></p>
    <p class="fine num">{effectiveLine}</p>
    <ProvenanceLine source="demo" detail="This page is part of the design mockup: the wording is approved, the build is not deployed." />
  </Well>
</div>

<style>
  .section {
    display: grid;
    gap: var(--space-4);
  }

  .statements {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: var(--space-4);
  }

  .statements li {
    max-inline-size: var(--measure);
    line-height: var(--lh-body);
    padding-inline-start: var(--space-4);
    border-inline-start: 2px solid var(--c-rule);
  }

  .prose p {
    margin: 0;
    max-inline-size: var(--measure);
    line-height: var(--lh-body);
  }

  .prose p + p {
    margin-block-start: var(--space-4);
  }

  p {
    margin: 0;
    line-height: var(--lh-body);
  }

  .fine {
    font-size: var(--step--2);
    color: var(--c-text-muted);
  }

  a {
    color: var(--c-text-accent);
  }
</style>
