-- Guest accounts: the whole of the "true guest session" feature's database side.
--
-- Every visitor gets an account the instant they arrive (see
-- auth.EnsureGuestSession, mounted ahead of every /api/v1 route) — there is no
-- anonymous-but-unaccounted browsing left in the app, only a session that happens to
-- be disposable. is_guest marks that account as one of those; guest_expires_at is the
-- instant it stops being valid.
--
-- A timestamp rather than a "created + fixed TTL" computation, for the same reason
-- 0120's pro_until is a timestamp rather than a boolean: the expiry is data, not
-- logic, so the cleanup sweep (DeleteExpiredGuests) is a plain WHERE clause and the
-- TTL can change (or a specific guest's window can be extended) without touching a
-- single row that predates the change.
--
-- No new cleanup machinery beyond a DELETE: every user-owned table already declares
-- ON DELETE CASCADE back to users.id (the same web account deletion relies on — see
-- DeleteUser in queries/users.sql), so reaping an expired guest here removes
-- everything that guest applied to, saved, searched, or tracked in the same
-- statement. Nothing about a guest's data outlives the row.
--
-- Both columns are additive and unread by the previous binary, so deploy order does
-- not matter. Applied to a fresh volume by initdb after 0121; on an existing prod
-- volume this statement must be run manually BEFORE deploying code that reads these
-- columns.
ALTER TABLE public.users
    ADD COLUMN is_guest boolean NOT NULL DEFAULT false;

ALTER TABLE public.users
    ADD COLUMN guest_expires_at timestamp with time zone;

-- Partial index over exactly what the cleanup sweep scans: live guest rows ordered by
-- when they expire. Non-guest accounts (the overwhelming majority once this ships,
-- if a real sign-in is ever re-enabled) never enter the index at all.
CREATE INDEX idx_users_guest_expires_at ON public.users USING btree (guest_expires_at) WHERE (is_guest = true);
