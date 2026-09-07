<script lang="ts">
  import { SlidersHorizontal, X } from '@lucide/svelte';
  import type { FilterStore } from '$lib/filters';
  import { RAIL } from '$lib/filterSections';
  import type { FacetCounts } from '$lib/types';
  import FilterDropdown from './FilterDropdown.svelte';
  import FilterPaneContent from './FilterPaneContent.svelte';

  // The horizontal, HiringCafe-style filter bar: one pill button per RAIL entry,
  // each opening a small dropdown with that facet's controls — bound directly to
  // the LIVE store, so a change applies immediately (no staged/Apply step, same as
  // the old sidebar's chip removal). `onMore` opens the full modal for anything
  // outside the primary rail (e.g. a facet excluded from this scope, or — on a
  // narrow viewport where a row of dropdowns doesn't work — the whole filter set).
  let {
    store,
    counts = null,
    exclude = [],
    onMore,
  }: {
    store: FilterStore;
    counts?: FacetCounts | null;
    exclude?: string[];
    onMore: () => void;
  } = $props();

  const visibleRail = $derived(
    RAIL.filter((e) => !(e.facetParam && exclude.includes(e.facetParam))),
  );

  function selCount(param: string): number {
    const st = store.value.facets[param];
    return st ? st.include.length + st.exclude.length : 0;
  }

  // Mirrors FilterModal's entryCount, minus the modal-only "My filters" / minMatch
  // concerns the bar doesn't carry.
  function entryCount(e: (typeof RAIL)[number]): number {
    const f = store.value;
    if (e.kind === 'category') return selCount('role') + selCount('category') + selCount('ai_archetype');
    if (e.kind === 'experience')
      return selCount('seniority') + selCount('role_type') + (f.experienceYearsMax != null ? 1 : 0);
    if (e.kind === 'location') return selCount('regions') + selCount('countries') + selCount('cities');
    if (e.kind === 'salary') return selCount('salary_currency') + (f.salaryMin != null ? 1 : 0);
    if (e.kind === 'work') return selCount('work_mode') + selCount('employment_type');
    if (e.kind === 'industry') return selCount('domains') + selCount('company_type') + selCount('collections');
    if (e.kind === 'language') return selCount('english_level') + selCount('posting_language');
    if (e.kind === 'relocation')
      return selCount('relocation') + (f.visa ? 1 : 0) + (f.clearance !== 'any' ? 1 : 0);
    if (e.kind === 'posted') return f.postedWithinDays != null ? 1 : 0;
    return selCount(e.facetParam ?? e.key);
  }

  const totalActive = $derived(store.active);
</script>

<div class="flex items-center gap-2 overflow-x-auto pb-1 pl-0.5 pr-1" style="scrollbar-width: thin;">
  <button
    type="button"
    onclick={onMore}
    class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border border-border bg-card px-3 py-1.5 text-sm font-semibold transition-colors hover:bg-accent"
  >
    <SlidersHorizontal class="size-3.5 shrink-0" />
    Filters
  </button>

  {#each visibleRail as entry (entry.key)}
    <FilterDropdown label={entry.label} count={entryCount(entry)}>
      {#snippet panel()}
        <FilterPaneContent {entry} {store} {counts} {exclude} />
      {/snippet}
    </FilterDropdown>
  {/each}

  {#if totalActive > 0}
    <button
      type="button"
      onclick={() => store.clear()}
      class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2.5 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground"
    >
      <X class="size-3.5" />
      Reset all
    </button>
  {/if}
</div>
