// Package authprim holds token primitives shared by the bootstrap seed, the
// API token handler, and the auth middleware. Lives under internal/ because
// it is not part of the public engine API.
//
// Why a dedicated package: the same algorithm is needed by three layers
// (app seed, delivery handler, delivery middleware). Cross-layer imports
// from app → delivery would invert the dependency arrow, so each layer
// previously kept its own copy of SHA-256 hashing and bb_<hex> format.
// This package is the single source of truth — every layer imports from
// here.
//
// Naming: package is `authprim` (not `auth`) to avoid colliding with
// internal/infrastructure/auth, the EdDSA keypair / JWT verifier package.
//
// Invariant: this package is a LEAF — no internal/ imports. If you find
// yourself wanting to depend on domain or infrastructure here, pull the
// caller up instead. Keeping authprim leaf-only preserves Clean Architecture.
package authprim

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// Prefix marks a string as a SyntheticBrew API token. Stable on-disk format.
	Prefix = "bb_"
	// HexLen is the number of hex characters after Prefix (32 random bytes).
	HexLen = 64
	// Length is the total token length: Prefix + HexLen.
	Length = len(Prefix) + HexLen
)

// ErrInvalidTokenFormat is returned by ValidateFormat for any input that does
// not match `bb_<64 lowercase hex>`. Wrap-friendly via %w so callers can use
// errors.Is to match the class without depending on the message string.
var ErrInvalidTokenFormat = errors.New("invalid token format")

// Generate returns a fresh random API token in `bb_<64-hex>` form.
// 32 random bytes from crypto/rand → 64 hex chars.
func Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return Prefix + hex.EncodeToString(b), nil
}

// Hash returns the lowercase SHA-256 hex digest of plain. The same value is
// stored in api_tokens.token_hash and re-derived on every verify lookup.
//
// Hash makes no assumption about plain — caller must enforce length/prefix
// invariants before calling if those matter (the auth middleware checks the
// `bb_` prefix; ValidateFormat does the strict check).
func Hash(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// ValidateFormat checks that plain matches `bb_<64 lowercase hex>`. Used by
// the bootstrap seed to fail-fast on misconfigured SYNTHETICBREW_BOOTSTRAP_ADMIN_TOKEN.
// The middleware does not call this — it only verifies the prefix and lets
// the DB lookup reject malformed tokens.
//
// Returns errors wrapped around ErrInvalidTokenFormat so callers can do
// errors.Is(err, authprim.ErrInvalidTokenFormat) to match the class.
func ValidateFormat(plain string) error {
	if !strings.HasPrefix(plain, Prefix) || len(plain) != Length {
		return fmt.Errorf("%w: expected %s<%d-hex> (%d chars), got %d chars", ErrInvalidTokenFormat, Prefix, HexLen, Length, len(plain))
	}
	hexPart := plain[len(Prefix):]
	if _, err := hex.DecodeString(hexPart); err != nil {
		return fmt.Errorf("%w: hex part is not valid hex: %v", ErrInvalidTokenFormat, err)
	}
	return nil
}
