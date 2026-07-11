package domain

// Active-users gate modes. In "enforce" mode a user beyond the configured
// limit is rejected; in "monitor" mode the excess is only observed and
// reported, never blocked.
const (
	ActiveUsersModeEnforce = "enforce"
	ActiveUsersModeMonitor = "monitor"
)

// ActiveUsersWindowSeconds is the rolling window for counting active users:
// a user counts as active when their last_active_at falls within the window.
const ActiveUsersWindowSeconds int64 = 2592000

// ActiveUsersDecision is the result of the active-users gate. When Allowed is
// false the remaining fields describe the configured limit and the count of
// users already active in the window.
type ActiveUsersDecision struct {
	Allowed bool
	Limit   int64
	Used    int64
}
