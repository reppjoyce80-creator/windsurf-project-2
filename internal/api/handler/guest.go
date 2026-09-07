package handler

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/strelov1/freehire/internal/platform/db"
)

// guestSessions adapts *db.Queries to auth.GuestSessionLoader and
// auth.GuestProvisioner, the same pattern apiKeys (above, in auth.go) uses to hand
// the auth package a database-free interface: the auth package must not import
// internal/platform/db (see the doc comments on TokenVersionLoader / RoleLoader), so
// the two rows GetUserSessionState and CreateGuestUser actually return are unpacked
// into the plain primitives auth.EnsureGuestSession asks for here, at the boundary.
type guestSessions struct{ q *db.Queries }

// GetGuestSession reports an account's current token generation, whether it is a
// guest, and (only meaningful when it is) the unix instant it expires.
func (g guestSessions) GetGuestSession(ctx context.Context, id int64) (tokenVersion int32, isGuest bool, guestExpiresUnix int64, err error) {
	row, err := g.q.GetUserSessionState(ctx, id)
	if err != nil {
		return 0, false, 0, err
	}
	if row.GuestExpiresAt.Valid {
		guestExpiresUnix = row.GuestExpiresAt.Time.Unix()
	}
	return row.TokenVersion, row.IsGuest, guestExpiresUnix, nil
}

// CreateGuestUser provisions a disposable account under a synthetic, guaranteed-unique
// address -- guests never sign in by password or email, so the address exists only to
// satisfy the NOT NULL UNIQUE constraint every account row carries. uuid.NewString
// (already a dependency throughout this package -- see assistant.go, cv.go, and
// others) is what guarantees the "guaranteed-unique" half without a round trip to
// check.
func (g guestSessions) CreateGuestUser(ctx context.Context, expiresAt time.Time) (int64, error) {
	return g.q.CreateGuestUser(ctx, db.CreateGuestUserParams{
		Email:          "guest-" + uuid.NewString() + "@guest.invalid",
		GuestExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
}
