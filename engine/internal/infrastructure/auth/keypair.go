package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Keypair is a loaded or freshly-generated Ed25519 keypair used by local-mode
// admin session issuance. In external mode only the public half is present
// (provided via config); the private half lives in the landing service.
type Keypair struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey // nil when only the public key was loaded (external mode)
}

// LoadOrGenerateKeypair returns an Ed25519 keypair for signing local-admin
// sessions. On first run it writes both keys to `dir` with mode 0600 so the
// signing half is protected but stable across restarts (otherwise all
// existing admin sessions would be invalidated on every boot).
//
// Layout:
//
//	<dir>/jwt_ed25519.priv  (32 bytes hex, mode 0600)
//	<dir>/jwt_ed25519.pub   (32 bytes hex, mode 0644)
//
// Caller must ensure `dir` is outside the repo (add to .gitignore) and
// persisted across container restarts (mount a volume).
func LoadOrGenerateKeypair(dir string) (*Keypair, error) {
	if dir == "" {
		return nil, errors.New("keypair dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	privPath := filepath.Join(dir, "jwt_ed25519.priv")
	pubPath := filepath.Join(dir, "jwt_ed25519.pub")

	priv, err := readHexFile(privPath, ed25519.PrivateKeySize)
	if err == nil {
		pub, err := readHexFile(pubPath, ed25519.PublicKeySize)
		if err != nil {
			return nil, fmt.Errorf("read public key %s: %w", pubPath, err)
		}
		return &Keypair{Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read private key %s: %w", privPath, err)
	}

	pub, privNew, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 keypair: %w", err)
	}
	// Race guard: O_EXCL+O_CREATE makes the create atomic. If another
	// concurrent boot beat us to it (k8s rolling restart, multi-replica),
	// we discard our just-generated key and re-read theirs from disk.
	if err := writeHexFileExcl(privPath, privNew, 0o600); err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("write private key: %w", err)
		}
		// Lost the race — read the winner's keypair instead.
		priv, err := readHexFile(privPath, ed25519.PrivateKeySize)
		if err != nil {
			return nil, fmt.Errorf("re-read private key after race: %w", err)
		}
		pub, err := readHexFile(pubPath, ed25519.PublicKeySize)
		if err != nil {
			return nil, fmt.Errorf("re-read public key after race: %w", err)
		}
		return &Keypair{Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}, nil
	}
	if err := writeHexFile(pubPath, pub, 0o644); err != nil {
		return nil, fmt.Errorf("write public key: %w", err)
	}
	return &Keypair{Public: pub, Private: privNew}, nil
}

// writeHexFileExcl writes the hex-encoded data atomically — fails with
// os.ErrExist if the file already exists. Used for the private-key write to
// detect concurrent-boot races.
func writeHexFileExcl(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(hex.EncodeToString(data) + "\n")
	return err
}

// LoadPublicKey reads an Ed25519 public key from a hex-encoded file.
// Used in external mode where only the public half is configured.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := readHexFile(path, ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	return ed25519.PublicKey(raw), nil
}

func readHexFile(path string, wantLen int) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(string(trimTrailingNewlines(data)))
	if err != nil {
		return nil, fmt.Errorf("decode hex %s: %w", path, err)
	}
	if len(decoded) != wantLen {
		return nil, fmt.Errorf("%s: want %d bytes, got %d", path, wantLen, len(decoded))
	}
	return decoded, nil
}

func writeHexFile(path string, data []byte, mode os.FileMode) error {
	encoded := hex.EncodeToString(data)
	return os.WriteFile(path, []byte(encoded+"\n"), mode)
}

func trimTrailingNewlines(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}
