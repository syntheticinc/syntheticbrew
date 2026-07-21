package domain

import "time"

// OAuthRefreshToken is a persisted OAuth 2.1 refresh token record.
//
// The token itself is opaque and never stored — only its SHA-256 hash. Each
// record belongs to a rotation family (FamilyID): rotating a refresh token
// mints a successor in the same family and revokes the predecessor, so
// detecting a revoked-token reuse lets the whole family be revoked at once.
//
// CodeJTI links the very first refresh token of a family to the authorization
// code it was issued from, making authorization-code replay detectable
// (a second exchange of the same code hits the unique CodeJTI constraint).
type OAuthRefreshToken struct {
	ID        string
	TenantID  string
	UserSub   string
	CidHash   string
	Scope     string
	Resource  string
	FamilyID  string
	TokenHash string
	CodeJTI   string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}
