# Hosting & migration runbook

Handoff notes for whichever Claude session (this one, a future one here, or a
fresh Claude Code CLI session on a VPS) picks this project up next. Written
after a working session covering guest sessions, the Apply flow, and planning
a move off `localhost`. Nothing below is speculative — every claim was
verified against the actual code or a live test during that session.

## What's already shipped and verified

- **True guest sessions.** Every visitor gets a disposable account the instant
  they arrive (`auth.EnsureGuestSession`, mounted ahead of all `/api/v1`
  routes), ~5-minute TTL, no sign-in screen shown. Migration `0122` adds
  `is_guest`/`guest_expires_at` to `users`; a cleanup goroutine in
  `cmd/server/main.go` reaps expired guests (existing `ON DELETE CASCADE` FKs
  wipe everything they touched in one `DELETE`). Confirmed live: mint on first
  request, persists across requests, doesn't disturb a real logged-in session.
- **Apply flow.** `ApplyFormDialog.svelte`'s confirmation screen shows a
  corporate-style "Thank You for Applying" message (job title/company filled
  in, a "What happens next?" section, no mention that it's a personal
  tracking tool) and now has two exits: **Browse more jobs** (`goto('/')`,
  fixes the "confirmation screen is a dead end" complaint) and **Done**
  (closes the dialog, stays on the job page).
- **Apply button hardened.** The CTA in `JobView.svelte` no longer carries
  `href`/`target`/`rel` — the shared `Button` component
  (`design-system/src/button.svelte`) renders it as a plain
  `<button type="button">` instead of a real `<a href={job.url}>` when no
  `href` is passed, same CSS either way. Previously a click landing before
  Svelte finished hydrating (e.g. right after a rebuild) could fall through to
  the real link and leave the page for the original posting (remoteOK, in the
  case we reproduced); now the worst case is a dead click, never a leak to the
  employer's site. Confirmed live, including catching the exact race mid-test.
- **Resend email fixed.** `onboarding@resend.dev`'s sandbox sender can only
  send to the Resend account's own registered address. `APPLY_FORWARD_EMAILS`
  in `.env` now holds only that address; delivery confirmed working.
  Delivering to a second, non-Resend-verified address requires verifying a
  real domain in Resend — deliberately deferred, not started.
- **Telegram-as-backup notification channel** — discussed as priority 2, not
  started. Existing `internal/api/handler/telegram.go` /
  `telegramnotify.Client` infra is reusable; design question (one fixed chat
  ID vs. per-account linking) was never answered.

## Investigated and ruled out (don't re-investigate from scratch)

A bug report claimed (a) applying in one browser then opening the site in a
different browser/device shows the same page/state, and (b) concurrent users'
data collides. Checked all three places this class of bug usually lives and
found nothing:

- `web/nginx.conf` proxies straight through with zero caching directives — no
  shared cache exists that could hand one visitor's response to another.
- `web/src/hooks.server.ts` already has a `cacheControl` hook built
  specifically to prevent this: authenticated responses are marked
  `private, no-store` with `Vary: Cookie` (see `$lib/httpCache.ts`).
- No global/package-level mutable state in `internal/api/handler` that isn't
  scoped to the caller's own resolved user ID.

Most likely explanation for what was observed: browser/OS-level sync (Chrome
signed into the same account across devices syncs open tabs/history), not an
app bug. Worth confirming with the user whether the two browsers/devices
shared a sign-in before spending more time here.

## Ingestion

`cmd/ingest` reads `sources/*.yml` (207 files, one per ATS platform) and
pulls each listed company's public job-board feed; `search-drain` pushes the
result into Meilisearch. `ingest-batch.ps1` currently only runs ~24 of the
207 files. As of this session the DB held only 20 jobs — the fuller run
hasn't happened yet.

Real numbers, counted directly rather than guessed:

- **97,053 total company entries** across all 207 `sources/*.yml` files.
- Fetched 8 at a time (`defaultConcurrency = 8` in
  `internal/ingest/pipeline/pipeline.go`); a couple of specific ATS platforms
  (careers-page.com, vagas.com.br) have their own extra-conservative pacers
  on top (`internal/ingest/sources/pacer.go`), e.g. careers-page.com capped
  around 1.25 req/s.
- Estimated wall-clock for the full 207-file set: **several hours to a full
  day**, not minutes — plan to run it unattended (overnight), not watched.
- Running it does **not** consume any LLM/Claude usage — it's a plain Go
  binary making HTTP calls from the user's own machine to public ATS APIs,
  independent of any Claude session being open.

## Hosting

Goal: get this off `localhost` so it's reachable without the user's PC being
on. Nothing has been provisioned yet — still at the planning stage.

Tried and hit real friction:

- **Oracle Cloud "Always Free"** (Ampere A1, 2 OCPU/12GB RAM, genuinely
  permanent, not a trial) — the best fit for this stack since it's a real VM
  and the existing `docker-compose.yml` would run on it unmodified. User hit
  repeated signup/identity-verification failures — a known common friction
  point with Oracle's free-tier signup, not something wrong with the account
  or the plan. Troubleshooting tips given (different card, no VPN/incognito,
  try a different region, Oracle support chat) but not resolved as of this
  session.
- **Google Cloud Free Tier** (`e2-micro`, permanent, `us-west1`/`us-central1`/
  `us-east1` only) — signup asked for a $50 prepayment, which happens on
  accounts where Google only offers prepaid self-serve billing rather than
  the standard $0-hold flow (country-dependent). Even if resolved, `e2-micro`
  is 1 shared vCPU / 1GB RAM — likely too small for the full stack (Postgres +
  Meilisearch + Redis + MinIO + app + web) without trimming something.
- **Azure / AWS free VMs** — only free for the first 12 months as part of an
  intro credit, not an ongoing free tier like Oracle/Google. Not pursued.
- **Fly.io** — confirmed (checked their pricing page directly) they no longer
  have any free tier at all, pure pay-as-you-go.
- **Render / Railway** — already ruled out earlier: Render's free Postgres
  expires after 30 days + a 14-day grace period and free tier has no
  background workers at all; Railway's "free" is a 30-day/$5-credit trial,
  not ongoing.

Current default fallback if the free options keep not panning out: **a small
paid VPS** (Hetzner, DigitalOcean, ~$4-6/mo) — no prepayment games, no
identity-verification runaround, runs the existing `docker compose up`
unmodified.

If the stack needs to shrink to fit a smaller box, checked what's actually
safe to drop:

- **MinIO + minio-init** — safe to drop, zero risk. The code already
  degrades gracefully when `S3_*` is unset (résumé upload just doesn't
  persist); nothing built/tested this session touches it.
- **Redis** — technically fail-open in the app code, but it's already one of
  the lightest containers in the stack, so dropping it barely helps RAM and
  currently requires a compose change (`app` hard-depends on
  `redis: condition: service_healthy` for startup ordering). Not worth
  targeting.
- **Meilisearch** — the actual RAM hog (runs an embedding model in-container),
  but **not safe to drop as-is**: `internal/api/handler/search.go` confirms
  `/jobs/search` 503s when Meilisearch is unconfigured, and that endpoint is
  what powers the entire job-listing homepage. Cutting it cleanly would need
  real engineering (a Postgres-only fallback listing path), not a config flip.

### Migration mechanism (decided, not yet executed)

However hosting ends up resolved, the plan is: run the big ingest locally
first, independent of hosting. Then move the *database*, not redo the work:

1. `docker compose exec db pg_dump -U hire -d hire -F c -f /tmp/hire.dump`,
   copy it out with `docker cp`.
2. On the new host, run the same `pgvector/pgvector:pg18` image (the DB
   depends on the `pgvector` extension via migration `0092`), copy the dump
   over, `pg_restore -U hire -d hire --clean --if-exists`.
3. Point `DATABASE_URL` at the restored DB.
4. Run `search-drain` once there to rebuild the Meilisearch index from the
   restored data — reads local Postgres, doesn't re-hit any external API, so
   it's fast.

This means ingestion and hosting are independent problems — solving one
doesn't block the other, and neither has to be redone if the other changes.

## Open items

- Oracle/Google/VPS decision not finalized.
- Full 207-file ingest not yet run (DB still has ~20 jobs as of this
  writing).
- Telegram backup notifier: not started, design question unanswered.
- Resend second-recipient (domain verification): deferred by explicit user
  request, not started.
- `redis` startup dependency (`condition: service_healthy`) would need a
  compose change if the memory footprint ever needs it dropped — not needed
  yet since Redis isn't the RAM problem.
