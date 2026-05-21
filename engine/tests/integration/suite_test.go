//go:build integration

// Package integration contains CE-side HTTP integration tests for the engine.
// Tests run against a real CE server wired to a real PostgreSQL container with
// Liquibase-applied schema. Each file uses //go:build integration so the suite
// is opt-in (go test -tags integration).
//
// Run with:
//
//	go test -tags integration ./tests/integration/... -v -timeout 180s
//
// Requires a running Docker daemon. When Docker is unavailable the suite
// auto-skips so the build stays green.
package integration

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	ceserver "github.com/syntheticinc/syntheticbrew/pkg/server"
)

const (
	// ceTenantID is the single tenant row CE seeds for local-mode
	// deployments. All CE rows default to this uuid via GORM defaults.
	ceTenantID = "00000000-0000-0000-0000-000000000001"
)

var (
	baseURL    string
	adminToken string
	testDB     *gorm.DB

	// localSessionPrivKey is the Ed25519 private key loaded after the engine
	// boots (it writes the keypair on first start). Used by tokenFor/tokenForRole
	// in helpers_test.go to sign test JWTs with the same key the verifier trusts.
	localSessionPrivKey ed25519.PrivateKey

	// suiteSkipReason — non-empty means setup bailed (no Docker, etc.) and
	// each test file's requireSuite(t) will call t.Skip instead of fail.
	suiteSkipReason atomic.Value // string
)

func skipReason() string {
	v := suiteSkipReason.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// TestMain is the suite entry point. It runs once per process.
//
// Existing non-CE tests (production_harness_test.go, streaming_api_test.go,
// ws_api_test.go, v2_test.go) live in this same package and do NOT call
// requireSuite — they must keep running even when Docker is missing. So we
// never hard-fail here: setup errors flip suiteSkipReason and only CE suite
// tests skip.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cleanup, err := setupSuite(ctx)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		suiteSkipReason.Store(fmt.Sprintf("ce integration suite setup failed: %v", err))
		os.Exit(m.Run())
	}
	os.Exit(m.Run())
}

func setupSuite(ctx context.Context) (func(), error) {
	cleanups := &cleanupStack{}
	cleanup := func() { cleanups.run() }

	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("syntheticbrew_ce_test"),
		tcpostgres.WithUsername("syntheticbrew"),
		tcpostgres.WithPassword("syntheticbrew_ce_test_pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return cleanup, fmt.Errorf("start postgres: %w", err)
	}
	cleanups.push(func() { _ = pg.Terminate(context.Background()) })

	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return cleanup, fmt.Errorf("postgres connection string: %w", err)
	}

	// Liquibase migrations live at ../../migrations relative to this file
	// (engine/tests/integration → engine/migrations).
	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		return cleanup, fmt.Errorf("resolve migrations dir: %w", err)
	}
	if _, statErr := os.Stat(migrationsDir); statErr != nil {
		return cleanup, fmt.Errorf("migrations dir not found: %w", statErr)
	}
	if err := applyLiquibaseMigrations(ctx, pg, migrationsDir); err != nil {
		return cleanup, fmt.Errorf("apply liquibase migrations: %w", err)
	}

	httpPort, err := pickFreePort()
	if err != nil {
		return cleanup, fmt.Errorf("pick free port: %w", err)
	}

	dataDir, err := os.MkdirTemp("", "syntheticbrew-ce-it-")
	if err != nil {
		return cleanup, fmt.Errorf("mkdir data: %w", err)
	}
	cleanups.push(func() { _ = os.RemoveAll(dataDir) })

	configPath := filepath.Join(dataDir, "config.yaml")
	if err := writeBootstrapConfig(configPath, connStr, httpPort); err != nil {
		return cleanup, fmt.Errorf("write bootstrap config: %w", err)
	}

	// Isolate the engine's portfile / logs inside dataDir so we don't collide
	// with a developer's running engine under their real profile dir.
	restoreEnv := setEnvIsolated(dataDir)
	cleanups.push(restoreEnv)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	cleanups.push(serverCancel)

	go func() {
		_ = ceserver.Run(ceserver.Config{
			ConfigPath:     configPath,
			ConfigExplicit: true,
			RequireTenant:  false,
			Version:        "ce-integration-test",
			Commit:         "none",
			Date:           "none",
		})
		_ = serverCtx
	}()

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	if err := waitForHealthy(ctx, baseURL, 60*time.Second); err != nil {
		return cleanup, fmt.Errorf("wait for engine healthy: %w", err)
	}

	// Load the Ed25519 keypair that the engine generated on first boot.
	// The engine writes <jwt_keys_dir>/jwt_ed25519.priv on startup; we read
	// the same files so tokenFor() signs with exactly the key the verifier trusts.
	keysDir := filepath.Join(dataDir, "keys")
	kp, err := auth.LoadOrGenerateKeypair(keysDir)
	if err != nil {
		return cleanup, fmt.Errorf("load engine keypair: %w", err)
	}
	localSessionPrivKey = kp.Private

	// Open a direct GORM connection for test-side seeding + assertions.
	// This is intentionally separate from the engine's pool — truncation
	// must work regardless of engine state.
	db, err := gorm.Open(gormpostgres.Open(connStr), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		return cleanup, fmt.Errorf("open test gorm: %w", err)
	}
	testDB = db

	// Cache an always-admin token for test helpers that need one without
	// going through /auth/login. Signed with the engine's Ed25519 private key.
	adminToken = tokenFor("local-admin")

	// Seed a tenant-default chat model. Engine refuses to create agents
	// without one (resolveAgentModel returns 400 — see C1 fix). This model
	// survives truncateTables (llm_provider_models is intentionally not in
	// the tenant-truncate list), so every test downstream of suite setup
	// can call createAgentForTest without seeding its own model.
	if err := seedDefaultChatModel(db); err != nil {
		return cleanup, fmt.Errorf("seed default chat model: %w", err)
	}

	return cleanup, nil
}

// seedDefaultChatModel inserts a single chat model with is_default=true so
// that POST /api/v1/agents (which calls resolveAgentModel) can resolve a
// tenant default without a per-test setup step.
func seedDefaultChatModel(db *gorm.DB) error {
	m := models.LLMProviderModel{
		Name:      "integration-default-chat",
		Type:      "openai_compatible",
		Kind:      "chat",
		IsDefault: true,
		ModelName: "test-chat-model",
		BaseURL:   "https://api.test.example",
		Config:    "{}",
	}
	return db.Create(&m).Error
}

// cleanupStack is a tiny LIFO teardown stack. Panics in one cleanup don't
// abort later ones.
type cleanupStack struct{ fns []func() }

func (c *cleanupStack) push(f func()) { c.fns = append(c.fns, f) }
func (c *cleanupStack) run() {
	for i := len(c.fns) - 1; i >= 0; i-- {
		func() {
			defer func() { _ = recover() }()
			c.fns[i]()
		}()
	}
}

// applyLiquibaseMigrations runs the official liquibase image against the
// testcontainers postgres instance using its docker-network IP (not the
// host-mapped port, which the liquibase container can't reach).
func applyLiquibaseMigrations(ctx context.Context, pg *tcpostgres.PostgresContainer, migrationsDir string) error {
	pgHost, err := pg.ContainerIP(ctx)
	if err != nil {
		return fmt.Errorf("postgres container ip: %w", err)
	}
	jdbcURL := fmt.Sprintf("jdbc:postgresql://%s:5432/syntheticbrew_ce_test", pgHost)

	req := testcontainers.ContainerRequest{
		Image: "liquibase/liquibase:4.30",
		Mounts: testcontainers.ContainerMounts{
			{
				Source: testcontainers.GenericBindMountSource{HostPath: migrationsDir},
				Target: "/liquibase/changelog",
			},
		},
		Cmd: []string{
			"--url=" + jdbcURL,
			"--username=syntheticbrew",
			"--password=syntheticbrew_ce_test_pass",
			"--changeLogFile=db.changelog-master.yaml",
			"--searchPath=/liquibase/changelog",
			"update",
		},
		WaitingFor: wait.ForExit().WithExitTimeout(120 * time.Second),
	}

	liq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("start liquibase container: %w", err)
	}
	defer func() { _ = liq.Terminate(context.Background()) }()

	state, err := liq.State(ctx)
	if err != nil {
		return fmt.Errorf("liquibase state: %w", err)
	}
	if state.ExitCode != 0 {
		logs, _ := liq.Logs(ctx)
		body := ""
		if logs != nil {
			buf := make([]byte, 8192)
			n, _ := logs.Read(buf)
			body = string(buf[:n])
		}
		return fmt.Errorf("liquibase exited %d: %s", state.ExitCode, body)
	}
	return nil
}

// writeBootstrapConfig emits a YAML config that passes both config.Load
// (legacy validation) and config.LoadBootstrap (bootstrap path). Also writes
// prompts.yaml next to config.yaml — config.Load fails without it.
func writeBootstrapConfig(path, dbURL string, port int) error {
	// keysDir sits next to config.yaml so the engine writes its keypair there.
	keysDir := filepath.Join(filepath.Dir(path), "keys")
	content := fmt.Sprintf(`engine:
  host: "127.0.0.1"
  port: %d
database:
  url: %q
  host: "localhost"
security:
  auth_mode: "local"
  jwt_keys_dir: %q
logging:
  level: "warn"
llm:
  default_provider: "ollama"
`, port, dbURL, keysDir)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	promptsPath := filepath.Join(filepath.Dir(path), "prompts.yaml")
	promptsContent := "prompts:\n  system_prompt: \"integration-test assistant\"\n"
	return os.WriteFile(promptsPath, []byte(promptsContent), 0644)
}

// pickFreePort grabs a free TCP port on 127.0.0.1 and closes the listener.
// Tiny TOCTOU window between close and server bind — acceptable for a
// one-shot harness.
func pickFreePort() (int, error) {
	lst, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = lst.Close() }()
	addr, ok := lst.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type")
	}
	return addr.Port, nil
}

// setEnvIsolated points platform-specific data-dir env vars inside dataDir so
// the engine's portfile/logs land in our temp dir, not the user's profile.
func setEnvIsolated(dataDir string) func() {
	type kv struct {
		key string
		old string
		had bool
	}
	keys := []string{"APPDATA", "XDG_DATA_HOME", "HOME"}
	saved := make([]kv, 0, len(keys))
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		saved = append(saved, kv{key: k, old: old, had: had})
	}
	_ = os.Setenv("APPDATA", dataDir)
	_ = os.Setenv("XDG_DATA_HOME", dataDir)
	_ = os.Setenv("HOME", dataDir)
	return func() {
		for _, s := range saved {
			if s.had {
				_ = os.Setenv(s.key, s.old)
			} else {
				_ = os.Unsetenv(s.key)
			}
		}
	}
}

// waitForHealthy polls GET /api/v1/health until 200 or timeout.
func waitForHealthy(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := client.Get(base + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("health status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("engine did not become healthy within %s: %w", timeout, lastErr)
}

