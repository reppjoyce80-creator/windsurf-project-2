<script lang="ts">
  import type { Snippet } from 'svelte';
  import { resolve } from '$app/paths';
  import { Bell, Info, UserRound } from '@lucide/svelte';
  import { Tooltip } from '$lib/ui';
  import { EMPLOYER_CREDENTIALS, JOB_COLLECTION } from '$lib/facets';
  import { isAuthenticated } from '$lib/auth.svelte';
  import { openAuthDialog } from '$lib/auth-dialog.svelte';
  import { profileStore } from '$lib/profile.svelte';
  import { notifications } from '$lib/notifications.svelte';
  import { emptyFilters, type ClearanceFilter, type FilterStore, type JobFilters } from '$lib/filters';
  import { StagedFilters } from '$lib/stagedFilters.svelte';
  import { RAIL, RAIL_SECTIONS, type RailEntry, type RailSection } from '$lib/filterSections';
  import type { FacetCounts } from '$lib/types';
  import FilterModalShell from './FilterModalShell.svelte';
  import FilterPaneContent from './FilterPaneContent.svelte';
  import SavedSearches from '../SavedSearches.svelte';

  // The job-search filter modal: a thin wrapper over FilterModalShell that supplies the
  // job rail, the staged job filters, and the pane controls. The shell owns the chrome
  // (rail, footer, deferred apply); this file owns only what's job-specific.
  //
  // Reusable beyond the standalone list: `railKeys` restricts which rail panes show
  // (e.g. Specialization + Skills for a profile); `applyLabel`/`onApply` give the footer
  // a custom label and action (e.g. "Save" → persist a profile); `canApply` gates it.
  let {
    store,
    seed,
    counts = null,
    exclude = [],
    railKeys,
    title = 'All filters',
    applyLabel,
    onApply,
    canApply,
    plain = false,
    savedSearches = false,
    open = false,
    onClose,
    previewCount,
    stagedCounts,
    extra,
    matchAvailable = false,
    minMatch = null,
    onMinMatchChange,
  }: {
    store?: FilterStore;
    seed?: JobFilters;
    counts?: FacetCounts | null;
    exclude?: string[];
    railKeys?: string[];
    // Show the "My filters" (saved searches) tab. Opt-in: the standalone job list enables
    // it; reuse like the profile comparison modal leaves it off.
    savedSearches?: boolean;
    title?: string;
    applyLabel?: string;
    onApply?: (staged: StagedFilters) => void | Promise<void>;
    canApply?: (f: JobFilters) => boolean;
    // Plain-select reuse (e.g. the profile editor): drop the search-only exclude/match
    // toggles so a facet value reads as a plain choice, not a filter.
    plain?: boolean;
    open?: boolean;
    onClose: () => void;
    previewCount?: (params: URLSearchParams) => Promise<number>;
    // Live disjunctive facet counts for the staged selection — when supplied, every
    // control shows counts that recompute as you pick (the job list). Absent for the
    // analytics/swipe reuse, which keep the applied `counts` + total-only preview.
    stagedCounts?: (params: URLSearchParams) => Promise<FacetCounts>;
    // Extra content rendered above the pane, handed the staged store so it can edit it
    // (e.g. the profile editor's "import skills from CV").
    extra?: Snippet<[StagedFilters]>;
    // The "Minimum skill match" slider atop the Skills pane: a client-only threshold
    // over the viewer's own profile skills, not a JobFilters facet (the match percent
    // depends on who's looking, so there's nothing to put in JobFilters/the URL — see
    // JobsView's `minMatch`). Hidden unless the caller has a real percent to filter on.
    matchAvailable?: boolean;
    minMatch?: number | null;
    onMinMatchChange?: (value: number | null) => void;
  } = $props();

  const staged = new StagedFilters();

  // The "My filters" tab is present only on the full job modal — the caller enables saved
  // searches and doesn't restrict the rail to a facet subset (as the profile modal does).
  // Gates the tab itself (visibleRail), its data warm-up, and the footer nudge that jumps
  // to it, so the jump never lands on a missing tab.
  const hasSavedTab = $derived(savedSearches && !railKeys);

  // Warm the Telegram feature flag and the user's profile when the modal opens for a
  // signed-in user on the full job modal: the footer "save for TG alerts" nudge gates on
  // the flag, and the header "Apply my profile" action gates on the profile. Both are
  // no-ops off the browser / once loaded.
  $effect(() => {
    if (open && hasSavedTab && isAuthenticated()) {
      void notifications.ensureLoaded();
      void profileStore.ensureLoaded();
    }
  });

  // The header "Apply my profile" affordance shows on the full job modal (same scope as
  // the My-filters tab). Signed-out: the button still shows and its click opens the
  // sign-in dialog (apply after auth). Signed-in: gated on the profile load having
  // settled (so a user who has a profile never flashes the "create" link while it
  // loads) — the profile-derived Apply button when a profile exists, a create-profile
  // link when it doesn't.
  const showProfileAction = $derived(hasSavedTab && (!isAuthenticated() || profileStore.loaded));
  const profile = $derived(profileStore.profile);

  // The footer nudge shows only when the My-filters tab exists (so the jump lands
  // somewhere), Telegram alerts are available, and there's a search worth saving.
  const showSaveNudge = $derived(hasSavedTab && notifications.telegram.enabled && staged.active > 0);

  // The "My filters" (saved searches) tab. It heads the rail on the full job modal, but
  // not when the caller restricts the rail to a facet subset (e.g. the profile modal),
  // which has no saved-search context.
  const SAVED_ENTRY: RailEntry = { key: 'saved', label: 'My filters', section: 'SAVED', kind: 'saved' };
  const SECTIONS: RailSection[] = ['SAVED', ...RAIL_SECTIONS];

  // Rail entries visible under the current scope: restricted to `railKeys` when given,
  // and a 'facet' entry is hidden when its param is excluded (e.g. Company on a company page).
  const visibleRail = $derived([
    ...(hasSavedTab ? [SAVED_ENTRY] : []),
    ...RAIL.filter(
      (e) => (!railKeys || railKeys.includes(e.key)) && !(e.facetParam && exclude.includes(e.facetParam)),
    ),
  ]);

  const jobCollectionValues = JOB_COLLECTION.map((o) => o.value);
  const employerCredentialValues = EMPLOYER_CREDENTIALS.map((o) => o.value);

  // Values selected for one facet — included plus excluded — so the rail count reflects
  // any staged selection regardless of sign. `values`, when passed, scopes the count to
  // just that subset — for a param split across two panes (see ChipFacet's `options`
  // override), so a badge doesn't count values shown under a different tab.
  function selCount(f: JobFilters, param: string, values?: string[]): number {
    const st = f.facets[param];
    if (!st) return 0;
    if (!values) return st.include.length + st.exclude.length;
    const allowed = new Set(values);
    return st.include.filter((v) => allowed.has(v)).length + st.exclude.filter((v) => allowed.has(v)).length;
  }

  function entryCount(e: RailEntry): number {
    const f = staged.value;
    if (e.kind === 'category') return selCount(f, 'role') + selCount(f, 'category') + selCount(f, 'ai_archetype');
    if (e.kind === 'experience')
      return selCount(f, 'seniority') + selCount(f, 'role_type') + (f.experienceYearsMax != null ? 1 : 0);
    if (e.kind === 'location') return selCount(f, 'regions') + selCount(f, 'countries') + selCount(f, 'cities');
    if (e.kind === 'salary') return selCount(f, 'salary_currency') + (f.salaryMin != null ? 1 : 0);
    if (e.kind === 'work') return selCount(f, 'work_mode') + selCount(f, 'employment_type');
    if (e.kind === 'industry')
      return selCount(f, 'domains') + selCount(f, 'company_type') + selCount(f, 'collections', jobCollectionValues);
    if (e.kind === 'language') return selCount(f, 'english_level') + selCount(f, 'posting_language');
    if (e.kind === 'relocation')
      return (
        selCount(f, 'relocation') +
        (f.visa ? 1 : 0) +
        (f.clearance !== 'any' ? 1 : 0) +
        selCount(f, 'collections', employerCredentialValues)
      );
    if (e.kind === 'posted') return f.postedWithinDays != null ? 1 : 0;
    // The Minimum skill match threshold lives at the top of the Skills pane, so it
    // counts toward that tab's badge alongside the skills facet selections.
    if (e.key === 'skills') return selCount(f, 'skills') + (minMatch != null ? 1 : 0);
    return selCount(f, e.facetParam ?? e.key);
  }

  function seedStaged() {
    staged.seed(seed ?? store?.value ?? emptyFilters());
  }

  async function apply() {
    if (onApply) {
      await onApply(staged);
      return;
    }
    if (store) staged.commit(store);
  }

  const applyDisabled = $derived(canApply ? !canApply(staged.value) : false);
</script>

<FilterModalShell
  {open}
  {onClose}
  {title}
  rail={visibleRail}
  sections={SECTIONS}
  {staged}
  {entryCount}
  seed={seedStaged}
  initialKey={RAIL[0]?.key}
  {apply}
  {applyDisabled}
  {applyLabel}
  {previewCount}
  countsFetch={stagedCounts}
  {pane}
  headerAction={showProfileAction ? profileAction : undefined}
  {titleHint}
  extra={extra ? extraStaged : undefined}
  {footerNote}
/>

<!-- Most facets are three-state (off / include / exclude); one quiet hint in the
     header covers all of them rather than repeating the explanation on every
     excludable section. -->
{#snippet titleHint()}
  <!-- side="right", not "bottom": the trigger sits at the modal's left edge, and a
       centered tooltip (left-1/2 -translate-x-1/2) grows equally both ways —
       overflowing the modal to the left no matter how short the content is.
       Growing rightward only stays inside it. -->
  <Tooltip side="right">
    <button
      type="button"
      aria-label="How filters work"
      class="flex size-4 items-center justify-center rounded-full text-muted-foreground transition-colors hover:text-foreground"
    >
      <Info class="size-3.5" aria-hidden="true" />
    </button>
    {#snippet content()}
      <a
        href={resolve('/features/advanced-search')}
        class="block whitespace-nowrap font-medium text-foreground underline-offset-2 hover:underline"
      >
        See how filters work →
      </a>
    {/snippet}
  </Tooltip>
{/snippet}

{#snippet profileAction()}
  {#if !isAuthenticated() || profile}
    <!-- Signed-out: click opens the sign-in dialog; signed-in with a profile: applies it. -->
    <button
      type="button"
      onclick={() => (profile ? staged.applyProfile(profile) : openAuthDialog('login'))}
      class="flex h-9 items-center gap-1.5 rounded-lg bg-brand px-3 text-sm font-medium text-brand-foreground transition-opacity hover:opacity-90"
    >
      <UserRound class="size-4 shrink-0" aria-hidden="true" />
      Apply my profile
    </button>
  {:else}
    <a
      href={resolve('/my/profile')}
      class="flex h-9 items-center gap-1.5 rounded-lg border border-dashed border-border px-3 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
    >
      <UserRound class="size-4 shrink-0" aria-hidden="true" />
      Create a profile
    </a>
  {/if}
{/snippet}

{#snippet extraStaged()}
  {@render extra?.(staged)}
{/snippet}

{#snippet footerNote({ jumpTo, activeKey }: { jumpTo: (key: string) => void; activeKey: string })}
  {#if showSaveNudge && activeKey !== 'saved'}
    <p class="flex items-center justify-end gap-1.5 text-xs text-muted-foreground">
      <Bell class="size-3.5 shrink-0" aria-hidden="true" />
      <span>
        Want new jobs for this search via Email/Telegram?
        <button
          type="button"
          onclick={() => jumpTo('saved')}
          class="font-medium text-foreground underline underline-offset-2 hover:opacity-80"
        >
          Save it to My filters
        </button>.
      </span>
    </p>
  {/if}
{/snippet}

{#snippet pane(entry: RailEntry, live: FacetCounts | null)}
  {#if entry.kind === 'saved'}
    <SavedSearches store={staged} />
  {:else}
    <FilterPaneContent
      {entry}
      store={staged}
      counts={live ?? counts}
      {exclude}
      {plain}
      {matchAvailable}
      {minMatch}
      {onMinMatchChange}
    />
  {/if}
{/snippet}
