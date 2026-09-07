<script lang="ts">
  import type { Snippet } from 'svelte';
  import { ChevronDown } from '@lucide/svelte';
  import { focusTrap } from '$lib/actions/focusTrap';
  import { portal } from '$lib/actions/portal';

  // One category button in FilterBar: a pill trigger that opens a positioned panel
  // below it holding that facet's controls (via FilterPaneContent). Closes on
  // outside click, Escape, or Tab-out (focusTrap keeps focus inside while open, so
  // the browser's own Tab-out never needs separate handling). Each dropdown owns
  // its own open state — the bar just renders one of these per rail entry — so two
  // can never fight over which is "active" the way the modal's single-pane rail did.
  //
  // The panel is portaled to <body> and positioned with `fixed` + coordinates read
  // off the trigger, rather than `absolute` inside the trigger's own `relative`
  // wrapper: that wrapper lives inside FilterBar's `overflow-x-auto` pill row, and
  // an `overflow-x` other than `visible` forces `overflow-y` to behave as `auto`
  // too (the CSS spec's visible/non-visible pairing rule) -- so the row clips any
  // absolutely-positioned child to its own ~40px height instead of letting the
  // panel hang below it. Same failure mode `portal.ts` already documents for the
  // AI-filter dialog, one layer of positioning down: escaping the row is the only
  // way out, not a bigger z-index.
  let {
    label,
    count = 0,
    panel,
  }: {
    label: string;
    count?: number;
    panel: Snippet;
  } = $props();

  let open = $state(false);
  let root: HTMLDivElement | undefined;
  let panelEl: HTMLDivElement | undefined;
  let coords = $state({ top: 0, left: 0 });

  function place() {
    if (!root) return;
    const r = root.getBoundingClientRect();
    coords = { top: r.bottom + 8, left: r.left };
  }

  // root only wraps the trigger button now -- the panel itself is portaled out to
  // <body> (see the note above), so a click landing inside the panel is a click
  // outside `root` too. Both have to say "not mine" before this counts as an
  // outside click, or every click on a region/checkbox in the panel closed it
  // before the selection could register.
  function onDocClick(e: MouseEvent) {
    const target = e.target as Node;
    if (root?.contains(target)) return;
    if (panelEl?.contains(target)) return;
    open = false;
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') open = false;
  }

  function toggle() {
    if (!open) place();
    open = !open;
  }
</script>

<svelte:window
  onclick={open ? onDocClick : undefined}
  onkeydown={open ? onKeydown : undefined}
  onresize={open ? place : undefined}
  onscroll={open ? () => (open = false) : undefined}
/>

<div class="relative shrink-0" bind:this={root}>
  <button
    type="button"
    onclick={toggle}
    aria-expanded={open}
    class={[
      'inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-3 py-1.5 text-sm font-medium transition-colors',
      count > 0 || open
        ? 'border-brand bg-brand-muted text-brand-strong'
        : 'border-border bg-background text-foreground hover:bg-accent',
    ]}
  >
    {label}
    {#if count > 0}
      <span class="flex h-4 min-w-4 items-center justify-center rounded-full bg-brand px-1 text-[10px] font-semibold text-brand-foreground"
        >{count}</span
      >
    {/if}
    <ChevronDown class={['size-3.5 shrink-0 transition-transform', open && 'rotate-180']} />
  </button>

  {#if open}
    <div
      class="fixed z-dropdown max-h-[70vh] w-80 overflow-y-auto rounded-xl border border-border bg-popover p-4 text-popover-foreground shadow-lg"
      style="top: {coords.top}px; left: {coords.left}px;"
      role="dialog"
      aria-label={label}
      bind:this={panelEl}
      {@attach portal()}
      {@attach focusTrap()}
    >
      {@render panel()}
    </div>
  {/if}
</div>
