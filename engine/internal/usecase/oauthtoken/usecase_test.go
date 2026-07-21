package oauthtoken

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
)

const (
	testClientID = "test-client-id"
	testResource = "https://engine.example/api/v1/mcp/rpc"
	testIssuer   = "https://engine.example"
)

// fakeRepo is an in-memory RefreshTokenRepository honouring the Stage-1
// semantics: unique code_jti (replay → domain.ErrOAuthCodeReplayed) and atomic
// single-row RotateRevoke.
type fakeRepo struct {
	mu       sync.Mutex
	byHash   map[string]*domain.OAuthRefreshToken
	byJTI    map[string]string // code_jti -> family_id
	storeErr error
	// forceRotateZero makes the next RotateRevoke report 0 rows affected,
	// simulating a lost atomic-rotation race.
	forceRotateZero bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byHash: make(map[string]*domain.OAuthRefreshToken),
		byJTI:  make(map[string]string),
	}
}

func (f *fakeRepo) Store(_ context.Context, t domain.OAuthRefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.storeErr != nil {
		return f.storeErr
	}
	if t.CodeJTI != "" {
		if _, dup := f.byJTI[t.CodeJTI]; dup {
			return domain.ErrOAuthCodeReplayed
		}
		f.byJTI[t.CodeJTI] = t.FamilyID
	}
	rec := t
	f.byHash[t.TokenHash] = &rec
	return nil
}

func (f *fakeRepo) GetByHash(_ context.Context, hash string) (domain.OAuthRefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.byHash[hash]
	if !ok {
		return domain.OAuthRefreshToken{}, assertNotFound
	}
	return *rec, nil
}

func (f *fakeRepo) RotateRevoke(_ context.Context, hash string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forceRotateZero {
		f.forceRotateZero = false
		return 0, nil
	}
	rec, ok := f.byHash[hash]
	if !ok || rec.RevokedAt != nil {
		return 0, nil
	}
	now := time.Now()
	rec.RevokedAt = &now
	return 1, nil
}

func (f *fakeRepo) RevokeFamily(_ context.Context, familyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, rec := range f.byHash {
		if rec.FamilyID == familyID && rec.RevokedAt == nil {
			rec.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeRepo) FindFamilyByCodeJTI(_ context.Context, codeJTI string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fam, ok := f.byJTI[codeJTI]
	if !ok {
		return "", assertNotFound
	}
	return fam, nil
}

// familyRevoked reports whether every token in the family is revoked.
func (f *fakeRepo) familyRevoked(familyID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := false
	for _, rec := range f.byHash {
		if rec.FamilyID != familyID {
			continue
		}
		seen = true
		if rec.RevokedAt == nil {
			return false
		}
	}
	return seen
}

var assertNotFound = &notFoundError{}

type notFoundError struct{}

func (*notFoundError) Error() string { return "not found" }

// newSigner builds a real Stage-1 signer with a throwaway keypair.
func newSigner(t *testing.T) *auth.OAuthTokenSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return auth.NewOAuthTokenSigner(priv, testIssuer)
}

// mintCode signs an authorization-code blob with the given tenant and a fresh
// PKCE pair. It returns the code and the matching verifier.
func mintCode(t *testing.T, s *auth.OAuthTokenSigner, tenantID, redirectURI string) (code, verifier string) {
	t.Helper()
	verifier = "verifier-" + uuid.New().String()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	cidHash := sha256.Sum256([]byte(testClientID))
	code, err := s.SignOAuthBlob("oauth_code", map[string]any{
		"cid_hash":              hex.EncodeToString(cidHash[:]),
		"redirect_uri":          redirectURI,
		"scope":                 domain.OAuthScopeProvision + " " + domain.OAuthScopeManage,
		"resource":              testResource,
		"sub":                   "user-1",
		"tenant_id":             tenantID,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"jti":                   uuid.New().String(),
	}, 60*time.Second)
	require.NoError(t, err)
	return code, verifier
}

func newUsecase(s *auth.OAuthTokenSigner, repo RefreshTokenRepository) *Usecase {
	return New(s, s, s, repo, nil, testResource)
}

const loopbackRedirect = "http://127.0.0.1:1455/callback"

func exchangeInput(code, verifier string) Input {
	return Input{
		GrantType:    "authorization_code",
		Code:         code,
		RedirectURI:  loopbackRedirect,
		ClientID:     testClientID,
		CodeVerifier: verifier,
	}
}

// decodeAccessToken parses (without verifying) the access-token claims.
func decodeAccessToken(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(token, claims)
	require.NoError(t, err)
	return claims
}

// TestExchangeCode_ReplayRevokesFamily is the load-bearing (a) guard: a second
// exchange of the same code returns invalid_grant AND revokes the family the
// first exchange spawned. RED without DB-backed single-use code JTIs.
func TestExchangeCode_ReplayRevokesFamily(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, verifier := mintCode(t, s, "t1", loopbackRedirect)

	first, err := uc.Execute(context.Background(), exchangeInput(code, verifier))
	require.NoError(t, err)
	require.NotEmpty(t, first.RefreshToken)

	// The first refresh token must be live before the replay.
	firstStored, err := repo.GetByHash(context.Background(), authprim.Hash(first.RefreshToken))
	require.NoError(t, err)
	require.Nil(t, firstStored.RevokedAt)
	familyID := firstStored.FamilyID

	// Replay the same code.
	_, err = uc.Execute(context.Background(), exchangeInput(code, verifier))
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_grant", oerr.Code)

	assert.True(t, repo.familyRevoked(familyID), "family must be revoked after code replay")
}

// TestRotate_LostRaceRevokesFamily is the load-bearing (b) guard: when the
// atomic RotateRevoke reports 0 rows (a concurrent rotation already won) the
// grant is rejected and the family revoked. RED without the rows==0 check.
func TestRotate_LostRaceRevokesFamily(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, verifier := mintCode(t, s, "t1", loopbackRedirect)

	first, err := uc.Execute(context.Background(), exchangeInput(code, verifier))
	require.NoError(t, err)

	stored, err := repo.GetByHash(context.Background(), authprim.Hash(first.RefreshToken))
	require.NoError(t, err)
	familyID := stored.FamilyID

	repo.forceRotateZero = true
	_, err = uc.Execute(context.Background(), Input{
		GrantType:    "refresh_token",
		ClientID:     testClientID,
		RefreshToken: first.RefreshToken,
	})
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_grant", oerr.Code)
	assert.True(t, repo.familyRevoked(familyID), "family must be revoked when rotation race is lost")
}

// TestRotate_PreservesTenant is the load-bearing (c) guard: rotation signs the
// access token with the stored tenant_id and user sub, NOT tenant=user. RED
// without the T9 fix.
func TestRotate_PreservesTenant(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, verifier := mintCode(t, s, "t1", loopbackRedirect)

	first, err := uc.Execute(context.Background(), exchangeInput(code, verifier))
	require.NoError(t, err)

	rotated, err := uc.Execute(context.Background(), Input{
		GrantType:    "refresh_token",
		ClientID:     testClientID,
		RefreshToken: first.RefreshToken,
	})
	require.NoError(t, err)

	claims := decodeAccessToken(t, rotated.AccessToken)
	assert.Equal(t, "t1", claims["tenant_id"], "rotation must preserve the tenant binding")
	assert.Equal(t, "user-1", claims["sub"])
	assert.NotEqual(t, claims["sub"], claims["tenant_id"], "tenant must not be set to the user sub")
}

// TestExchangeCode_EmptyTenant covers the CE single-tenant path: a code with an
// empty tenant_id exchanges successfully and the access token carries "".
func TestExchangeCode_EmptyTenant(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, verifier := mintCode(t, s, "", loopbackRedirect)

	out, err := uc.Execute(context.Background(), exchangeInput(code, verifier))
	require.NoError(t, err)
	require.NotEmpty(t, out.AccessToken)

	claims := decodeAccessToken(t, out.AccessToken)
	assert.Equal(t, "", claims["tenant_id"])
	assert.Equal(t, "user-1", claims["sub"])
}

func TestExchangeCode_WrongVerifier(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, _ := mintCode(t, s, "t1", loopbackRedirect)

	_, err := uc.Execute(context.Background(), exchangeInput(code, "not-the-verifier"))
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_grant", oerr.Code)
}

func TestExchangeCode_RedirectMismatch(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, verifier := mintCode(t, s, "t1", loopbackRedirect)

	in := exchangeInput(code, verifier)
	in.RedirectURI = "https://evil.example/callback"
	_, err := uc.Execute(context.Background(), in)
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_grant", oerr.Code)
}

func TestExecute_UnsupportedGrant(t *testing.T) {
	s := newSigner(t)
	uc := newUsecase(s, newFakeRepo())
	_, err := uc.Execute(context.Background(), Input{GrantType: "password", ClientID: testClientID})
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "unsupported_grant_type", oerr.Code)
}

func TestExecute_MissingClientID(t *testing.T) {
	s := newSigner(t)
	uc := newUsecase(s, newFakeRepo())
	_, err := uc.Execute(context.Background(), Input{GrantType: "authorization_code"})
	var oerr *domain.OAuthError
	require.ErrorAs(t, err, &oerr)
	assert.Equal(t, "invalid_request", oerr.Code)
}

// TestNormalRotation confirms a happy rotation issues a new distinct pair and
// revokes the predecessor (baseline for the reuse guards).
func TestNormalRotation(t *testing.T) {
	s := newSigner(t)
	repo := newFakeRepo()
	uc := newUsecase(s, repo)
	code, verifier := mintCode(t, s, "t1", loopbackRedirect)

	first, err := uc.Execute(context.Background(), exchangeInput(code, verifier))
	require.NoError(t, err)

	rotated, err := uc.Execute(context.Background(), Input{
		GrantType:    "refresh_token",
		ClientID:     testClientID,
		RefreshToken: first.RefreshToken,
	})
	require.NoError(t, err)
	assert.NotEqual(t, first.RefreshToken, rotated.RefreshToken)

	// Old token now revoked → reuse is theft → invalid_grant.
	_, err = uc.Execute(context.Background(), Input{
		GrantType:    "refresh_token",
		ClientID:     testClientID,
		RefreshToken: first.RefreshToken,
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "invalid_grant"))
}
