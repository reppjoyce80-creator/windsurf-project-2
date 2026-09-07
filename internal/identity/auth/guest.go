package auth

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

// GuestSessionLoader reads back what EnsureGuestSession needs to judge a presented
// cookie: the account's current token generation (the same revocation check every
// other auth path makes) plus whether the account is a guest and, if so, the unix
// time it expires. Return values are plain primitives rather than a database row
// type, the same reasoning TokenVersionLoader and RoleLoader give -- this package
// needs no database import. guestExpiresUnix is meaningless (and ignored) when
// isGuest is false.
type GuestSessionLoader interface {
	GetGuestSession(ctx context.Context, id int64) (tokenVersion int32, isGuest bool, guestExpiresUnix int64, err error)
}

// GuestProvisioner mints a brand-new disposable account and returns its id.
// CreateGuestUser is expected to fail only on genuine infrastructure errors -- a
// synthetic email is generated fresh per call, so a collision is not a case this
// interface needs to describe.
type GuestProvisioner interface {
	CreateGuestUser(ctx context.Context, expiresAt time.Time) (int64, error)
}

// EnsureGuestSession returns middleware that guarantees every request carries a
// valid session, minting a disposable guest account the instant one is not already
// present. Mounted ahead of every route (before RequireAuth/RequireAuthOrKey/
// OptionalAuth ever run for that request), it is what makes "every visitor is
// already signed in, to an account nobody had to create" true without touching any
// of the ~90 existing route registrations: those middlewares gained a one-line fast
// path (see RequireAuth et al.) that trusts an identity already sitting in locals,
// which is exactly what this handler leaves behind.
//
// Minting, not just validating, is why this cannot simply call resolveSession and
// stop: a freshly-minted guest's Set-Cookie header only reaches the browser in the
// response to THIS request -- the request already in flight still carries no cookie
// at all, or an expired/foreign one. c.Locals is this request's only channel to tell
// the middlewares still ahead of it in the chain "trust this id", which is why this
// handler both sets the cookie for next time and stores the id in locals for right
// now.
//
// ttl is the guest session's lifetime -- both the cookie's own expiry and, via the
// expiresAt passed to the provisioner, the instant the account itself becomes
// eligible for the cleanup sweep (see cmd/server's guest-reaper goroutine). It is
// deliberately NOT the Issuer's own JWT ttl: a guest's session is meant to be short
// (the whole point is that it visibly disappears), while the JWT's own exp claim can
// safely stay at the Issuer's normal, much longer TTL -- the account row being gone
// is what actually ends the session (GetGuestSession returns pgx.ErrNoRows, exactly
// like an ordinary revoked account), so a stale exp claim never matters in practice.
func EnsureGuestSession(iss *Issuer, sessions GuestSessionLoader, guests GuestProvisioner, ttl time.Duration, secure bool, domains []string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if token := c.Cookies(CookieName); token != "" {
			if id, version, perr := iss.Parse(token); perr == nil {
				tv, isGuest, guestExpiresUnix, lerr := sessions.GetGuestSession(c.Context(), id)
				if lerr != nil && !errors.Is(lerr, pgx.ErrNoRows) {
					return fiber.NewError(fiber.StatusServiceUnavailable, "service temporarily unavailable")
				}
				stillLive := lerr == nil && tv == version &&
					(!isGuest || time.Now().Unix() < guestExpiresUnix)
				if stillLive {
					c.Locals(LocalsUserID, id)
					c.Locals(localsViaCookie, true)
					return c.Next()
				}
			}
		}

		id, err := guests.CreateGuestUser(c.Context(), time.Now().Add(ttl))
		if err != nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "service temporarily unavailable")
		}
		// A brand-new row's token_version starts at its column default (1) -- see
		// CreateGuestUser -- so the token is issued directly against that known value
		// rather than spending a second round trip reading it back.
		token, err := iss.Issue(id, 1)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to start session")
		}
		SetTokenCookie(c, token, ttl, secure, CookieDomainForHost(c.Hostname(), domains))
		c.Locals(LocalsUserID, id)
		c.Locals(localsViaCookie, true)
		return c.Next()
	}
}
