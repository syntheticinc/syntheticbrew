package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadOrGenerateKeypair_LoadsPreSeeded proves the pre-provisioned-Secret
// path: when dir already holds a valid keypair, LoadOrGenerateKeypair returns
// those EXACT keys (a freshly generated pair would differ) and does not error —
// i.e. it loads read-only without needing to write.
func TestLoadOrGenerateKeypair_LoadsPreSeeded(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	dir := t.TempDir()
	writeHex := func(name string, data []byte) {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(hex.EncodeToString(data)+"\n"), 0o600))
	}
	writeHex("jwt_ed25519.priv", priv)
	writeHex("jwt_ed25519.pub", pub)

	kp, err := LoadOrGenerateKeypair(dir)
	require.NoError(t, err)
	require.NotNil(t, kp)

	assert.True(t, bytes.Equal(priv, kp.Private), "loaded private key must equal the pre-seeded one")
	assert.True(t, bytes.Equal(pub, kp.Public), "loaded public key must equal the pre-seeded one")
}
