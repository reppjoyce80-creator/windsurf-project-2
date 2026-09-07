<script lang="ts">
  import type { FacetStore } from '$lib/facets';
  import { EMPLOYER_CREDENTIALS, FACETS, JOB_COLLECTION } from '$lib/facets';
  import type { ClearanceFilter, JobFilters } from '$lib/filters';
  import type { RailEntry } from '$lib/filterSections';
  import type { FacetCounts } from '$lib/types';
  import {
    EXPERIENCE_PRESETS,
    FRESHNESS_PRESETS,
    SALARY_MAX,
    SALARY_STEP,
    experienceLabel,
    freshnessLabel,
  } from '$lib/filterControls';
  import FacetSection from '../facets/FacetSection.svelte';
  import ChipFacet from './ChipFacet.svelte';
  import CategoryPane from './CategoryPane.svelte';
  import LocationPane from './LocationPane.svelte';

  // The one place a rail entry's controls render, shared by FilterModal (against a
  // deferred `StagedFilters` copy) and FilterBar (against the live `FilterStore`
  // directly, applying immediately). Both stores satisfy the same shape — `facet`/
  // `cycle`/`pick`/… plus the non-facet setters below — so this component neither
  // knows nor cares which one it's driving. Extracted from FilterModal's `pane`
  // snippet: this IS that logic, not a second copy of it.
  interface PaneStore extends FacetStore {
    readonly value: JobFilters;
    setSalaryMin(n: number | null): void;
    setVisa(on: boolean): void;
    setClearance(v: ClearanceFilter): void;
    setPostedWithinDays(n: number | null): void;
    setExperienceYearsMax(n: number | null): void;
  }

  let {
    entry,
    store,
    counts = null,
    exclude = [],
    plain = false,
    matchAvailable = false,
    minMatch = null,
    onMinMatchChange,
  }: {
    entry: RailEntry;
    store: PaneStore;
    counts?: FacetCounts | null;
    exclude?: string[];
    plain?: boolean;
    matchAvailable?: boolean;
    minMatch?: number | null;
    onMinMatchChange?: (value: number | null) => void;
  } = $props();

  const CLEARANCE_OPTIONS: { value: ClearanceFilter; label: string }[] = [
    { value: 'any', label: 'Any' },
    { value: 'hide', label: 'Hide' },
    { value: 'only', label: 'Only' },
  ];

  // In plain mode strip the search-only exclude/match toggles from a facet control.
  function facetDefFor(param: string | undefined) {
    const d = FACETS.find((x) => x.param === param);
    return d && plain ? { ...d, excludable: false, hasAndOr: false } : d;
  }

  const englishDef = FACETS.find((d) => d.param === 'english_level');
  const postingDef = FACETS.find((d) => d.param === 'posting_language');

  // A non-preset value (hand-edited URL) has no exact stop, so it reads as "Any"
  // (the rightmost stop) rather than snapping to "Today".
  const freshnessIndex = $derived.by(() => {
    const i = FRESHNESS_PRESETS.findIndex((p) => p.days === store.value.postedWithinDays);
    return i < 0 ? FRESHNESS_PRESETS.length - 1 : i;
  });

  // Same snap-to-Any rule as freshness above.
  const experienceIndex = $derived.by(() => {
    const i = EXPERIENCE_PRESETS.findIndex((p) => p.years === store.value.experienceYearsMax);
    return i < 0 ? EXPERIENCE_PRESETS.length - 1 : i;
  });
</script>

{#if entry.kind === 'category'}
  {@const roleDef = facetDefFor('role')}
  {#if roleDef && !exclude.includes('role')}
    <div class="mb-6"><FacetSection def={roleDef} {store} {counts} expand /></div>
  {/if}
  <CategoryPane {store} {plain} {counts} />
  {#if !exclude.includes('ai_archetype')}
    {@const aiArchetypeDef = facetDefFor('ai_archetype')}
    {#if aiArchetypeDef}
      <div class="mt-6"><FacetSection def={aiArchetypeDef} {store} {counts} expand /></div>
    {/if}
  {/if}
{:else if entry.kind === 'location'}
  <LocationPane {store} {counts} />
{:else if entry.kind === 'facet'}
  {@const def = facetDefFor(entry.facetParam)}
  {#if entry.key === 'skills' && matchAvailable}
    <!-- Client-only post-filter (see `minMatch`/`onMinMatchChange` props): re-filters
         the already-fetched page in memory, so it applies immediately, no debounce. -->
    <div class="mb-2 flex items-center justify-between">
      <h3 class="text-sm font-semibold tracking-tight">Minimum skill match</h3>
      <span class="text-xs font-medium text-muted-foreground">{minMatch != null ? `${minMatch}%+` : 'Any'}</span>
    </div>
    <input
      type="range"
      min="0"
      max="100"
      step="5"
      value={minMatch ?? 0}
      oninput={(e) => onMinMatchChange?.(Number(e.currentTarget.value) || null)}
      aria-label="Minimum skill match"
      class="mb-6 w-full accent-primary"
    />
  {/if}
  {#if def}<FacetSection {def} {store} {counts} expand />{/if}
{:else if entry.kind === 'salary'}
  <ChipFacet {store} param="salary_currency" label="Currency" {counts} />
  <div class="mb-2 mt-6 flex items-center justify-between">
    <h3 class="text-sm font-semibold tracking-tight">Minimum salary</h3>
    <span class="text-xs font-medium text-muted-foreground"
      >{store.value.salaryMin ? `${store.value.salaryMin.toLocaleString('en-US')}+` : 'Any'}</span
    >
  </div>
  <input
    type="range"
    min="0"
    max={SALARY_MAX}
    step={SALARY_STEP}
    value={store.value.salaryMin ?? 0}
    oninput={(e) => store.setSalaryMin(Number(e.currentTarget.value) || null)}
    aria-label="Minimum salary"
    class="w-full accent-primary"
  />
{:else if entry.kind === 'experience'}
  {@const showSeniority = !exclude.includes('seniority')}
  {@const showRoleType = !exclude.includes('role_type')}
  {#if showSeniority}
    <ChipFacet {store} param="seniority" label="Seniority" {counts} />
  {/if}
  <!-- Directly beneath seniority on purpose: the two are the axes users conflate. -->
  {#if showRoleType}
    <div class:mt-6={showSeniority}>
      <ChipFacet {store} param="role_type" label="Role type" {counts} />
    </div>
  {/if}
  <div class:mt-6={showSeniority || showRoleType}>
    <div class="mb-2 flex items-center justify-between">
      <h3 class="text-sm font-semibold tracking-tight">Years of experience</h3>
      <span class="text-xs font-medium text-muted-foreground">{experienceLabel(store.value.experienceYearsMax)}</span>
    </div>
    <input
      type="range"
      min="0"
      max={EXPERIENCE_PRESETS.length - 1}
      step="1"
      value={experienceIndex}
      oninput={(e) => store.setExperienceYearsMax(EXPERIENCE_PRESETS[Number(e.currentTarget.value)]?.years ?? null)}
      aria-label="Maximum years of experience"
      class="w-full accent-primary"
    />
    <p class="mt-2 text-xs text-muted-foreground">
      Setting a limit matches only postings that state an experience requirement — about half of them.
    </p>
  </div>
{:else if entry.kind === 'work'}
  <ChipFacet {store} param="work_mode" label="Work format" {counts} />
  <div class="mt-6"><ChipFacet {store} param="employment_type" label="Employment type" {counts} /></div>
{:else if entry.kind === 'industry'}
  <ChipFacet {store} param="domains" label="Industry" {counts} />
  <div class="mt-6"><ChipFacet {store} param="company_type" label="Company type" {counts} /></div>
  <div class="mt-6">
    <ChipFacet {store} param="collections" label="Collection" {counts} options={JOB_COLLECTION} />
  </div>
{:else if entry.kind === 'language'}
  {#if englishDef}<FacetSection def={englishDef} {store} {counts} expand />{/if}
  <div class="mt-4">{#if postingDef}<FacetSection def={postingDef} {store} {counts} expand />{/if}</div>
{:else if entry.kind === 'relocation'}
  <ChipFacet {store} param="relocation" label="Relocation" {counts} />
  <div class="mt-6">
    <ChipFacet
      {store}
      param="collections"
      label="Employer credentials"
      {counts}
      options={EMPLOYER_CREDENTIALS}
    />
  </div>
  <h3 class="mb-2 mt-6 text-sm font-semibold tracking-tight">Visa</h3>
  <label class="flex cursor-pointer items-center gap-2 text-sm">
    <input
      type="checkbox"
      class="size-4 rounded border-border"
      checked={store.value.visa}
      onchange={(e) => store.setVisa(e.currentTarget.checked)}
    />
    <span>Offers visa sponsorship</span>
  </label>
  <h3 class="mb-2 mt-6 text-sm font-semibold tracking-tight">Security clearance</h3>
  <div class="inline-flex overflow-hidden rounded-md border border-border" role="group">
    {#each CLEARANCE_OPTIONS as opt (opt.value)}
      <button
        type="button"
        class="px-3 py-1.5 text-sm transition-colors {store.value.clearance === opt.value
          ? 'bg-primary text-primary-foreground'
          : 'bg-background hover:bg-muted'}"
        aria-pressed={store.value.clearance === opt.value}
        onclick={() => store.setClearance(opt.value)}
      >
        {opt.label}
      </button>
    {/each}
  </div>
  <p class="mt-2 text-xs text-muted-foreground">
    Government vetting — UK SC/DV, US Secret/TS-SCI, AU NV1. Only is for candidates who already
    hold one.
  </p>
{:else if entry.kind === 'posted'}
  <div class="mb-2 flex items-center justify-between">
    <h3 class="text-sm font-semibold tracking-tight">Posted within</h3>
    <span class="text-xs font-medium text-muted-foreground">{freshnessLabel(store.value.postedWithinDays)}</span>
  </div>
  <input
    type="range"
    min="0"
    max={FRESHNESS_PRESETS.length - 1}
    step="1"
    value={freshnessIndex}
    oninput={(e) => store.setPostedWithinDays(FRESHNESS_PRESETS[Number(e.currentTarget.value)]?.days ?? null)}
    aria-label="Posted within"
    class="w-full accent-primary"
  />
{/if}
