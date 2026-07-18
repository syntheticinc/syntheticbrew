package plugin

import "context"

// ActiveUsersFloor supplies a fallback active-users limit for the engine's
// distinct-user gate when no per-tenant active_users_limit policy resolves one
// (a missing or malformed policy row). It lets a plugin close the gate's
// fail-open gap without the engine learning any plan/tier vocabulary: the
// engine treats the returned pair as opaque.
//
// Floor returns enforce=false to leave the gate unlimited — the CE default and
// any non-enforcing deployment. When enforce=true the engine applies limit
// through the SAME unlimited guard as a normal policy: a limit <= 0 still means
// unlimited, so an unlimited plan never rejects every user.
//
// Floor must do NO I/O: it resolves purely from ctx and a static default, so it
// has no error path that could itself fail open.
type ActiveUsersFloor interface {
	Floor(ctx context.Context) (limit int64, enforce bool)
}
