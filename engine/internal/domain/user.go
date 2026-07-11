package domain

import "context"

// Identity context helpers.
//
// The engine identifies callers by their JWT `sub` claim (varchar, not uuid).
// There is no users table — admin/system identity is external (a JWT
// issued by landing) or synthetic (CE local admin = "local-admin"). End-user
// identity on sessions/memories is likewise user_sub.
//
// These helpers let services read the authenticated sub from ctx without
// knowing how it was set (HTTP middleware, gRPC interceptor, test harness).

type userSubCtxKey struct{}

// WithUserSub returns a context with the authenticated user's `sub` claim set.
func WithUserSub(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, userSubCtxKey{}, sub)
}

// UserSubFromContext extracts the authenticated `sub` from context.
// Returns empty string if not set.
func UserSubFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userSubCtxKey{}).(string)
	return v
}
