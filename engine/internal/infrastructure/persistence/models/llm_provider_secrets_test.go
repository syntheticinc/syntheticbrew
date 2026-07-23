package models

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/secrets"
)

func newSecretsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// AutoMigrate chokes on the postgres-only gen_random_uuid() default in
	// sqlite; create the table shape manually instead.
	if err := db.Exec(`CREATE TABLE models (
		id TEXT PRIMARY KEY, name TEXT, type TEXT, kind TEXT, is_default BOOLEAN DEFAULT FALSE,
		base_url TEXT, model_name TEXT, api_key_encrypted TEXT, api_version TEXT,
		config TEXT DEFAULT '{}', tenant_id TEXT, created_at DATETIME, updated_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func initCipher(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(100 + i)
	}
	if err := secrets.Init(key); err != nil {
		t.Fatalf("init secrets: %v", err)
	}
}

// TestAPIKeySealedAtRest proves the model hooks encrypt on save and decrypt
// on read: the raw column carries ciphertext while every GORM read returns
// plaintext to consumers (model cache, adapters, LLM clients).
func TestAPIKeySealedAtRest(t *testing.T) {
	initCipher(t)
	db := newSecretsTestDB(t)

	m := &LLMProviderModel{
		ID: "9f7c0e7e-2b3a-4d3c-9a51-1f2e3d4c5b6a", Name: "openai", Type: "openai_compatible",
		Kind: "chat", ModelName: "gpt-4o", APIKeyEncrypted: "sk-live-secret",
		TenantID: "00000000-0000-0000-0000-000000000001",
	}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	// Raw column: sealed.
	var raw string
	if err := db.Raw(`SELECT api_key_encrypted FROM models WHERE id = ?`, m.ID).Scan(&raw).Error; err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "sk-live-secret") {
		t.Fatalf("api key not sealed at rest: %q", raw)
	}

	// GORM read: plaintext.
	var loaded LLMProviderModel
	if err := db.First(&loaded, "id = ?", m.ID).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if loaded.APIKeyEncrypted != "sk-live-secret" {
		t.Fatalf("AfterFind must return plaintext, got %q", loaded.APIKeyEncrypted)
	}

	// Re-save of a loaded row must not double-encrypt.
	if err := db.Save(&loaded).Error; err != nil {
		t.Fatalf("re-save: %v", err)
	}
	var again LLMProviderModel
	if err := db.First(&again, "id = ?", m.ID).Error; err != nil {
		t.Fatalf("re-find: %v", err)
	}
	if again.APIKeyEncrypted != "sk-live-secret" {
		t.Fatalf("round trip after re-save broken: %q", again.APIKeyEncrypted)
	}
}

// TestLegacyPlaintextRowStillReadable covers the upgrade path: rows written
// before encryption remain readable and get sealed on their next save.
func TestLegacyPlaintextRowStillReadable(t *testing.T) {
	initCipher(t)
	db := newSecretsTestDB(t)

	if err := db.Exec(`INSERT INTO models (id, name, type, kind, model_name, api_key_encrypted, tenant_id, config)
		VALUES ('8e6b1d2c-1a29-4c2b-8940-0e1d2c3b4a59', 'legacy', 'openai_compatible', 'chat', 'gpt-4', 'sk-legacy', '00000000-0000-0000-0000-000000000001', '{}')`).Error; err != nil {
		t.Fatalf("insert legacy: %v", err)
	}

	var loaded LLMProviderModel
	if err := db.First(&loaded, "name = ?", "legacy").Error; err != nil {
		t.Fatalf("find legacy: %v", err)
	}
	if loaded.APIKeyEncrypted != "sk-legacy" {
		t.Fatalf("legacy plaintext must pass through: %q", loaded.APIKeyEncrypted)
	}

	if err := db.Save(&loaded).Error; err != nil {
		t.Fatalf("save: %v", err)
	}
	var raw string
	if err := db.Raw(`SELECT api_key_encrypted FROM models WHERE name = 'legacy'`).Scan(&raw).Error; err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("legacy row must be sealed after save: %q", raw)
	}
}
