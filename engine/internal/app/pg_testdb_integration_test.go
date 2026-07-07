//go:build integration

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// appTestDB lazily provisions a single PostgreSQL container with the engine's
// Liquibase schema applied, shared across every integration test in package
// app that needs a real DB (the mcp_servers CHECK constraints only fire in
// Postgres — a mock repo cannot reproduce them). Docker-gated: when Docker is
// unavailable the caller skips instead of failing.
var (
	appTestDBOnce sync.Once
	appTestDB     *gorm.DB
	appTestDBErr  error
)

func requireAppTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	appTestDBOnce.Do(func() {
		appTestDB, appTestDBErr = setupAppTestDB()
	})
	if appTestDBErr != nil {
		t.Skipf("app integration DB unavailable: %v", appTestDBErr)
	}
	return appTestDB
}

func setupAppTestDB() (*gorm.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("syntheticbrew_app_test"),
		tcpostgres.WithUsername("syntheticbrew"),
		tcpostgres.WithPassword("syntheticbrew_app_test_pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres: %w", err)
	}

	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("postgres connection string: %w", err)
	}

	// migrations live at ../../migrations relative to this file
	// (engine/internal/app → engine/migrations).
	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		return nil, fmt.Errorf("resolve migrations dir: %w", err)
	}
	if _, statErr := os.Stat(migrationsDir); statErr != nil {
		return nil, fmt.Errorf("migrations dir not found: %w", statErr)
	}
	if err := applyAppLiquibaseMigrations(ctx, pg, migrationsDir); err != nil {
		return nil, fmt.Errorf("apply liquibase migrations: %w", err)
	}

	db, err := gorm.Open(gormpostgres.Open(connStr), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("open test gorm: %w", err)
	}
	return db, nil
}

// applyAppLiquibaseMigrations runs the official liquibase image against the
// testcontainers postgres instance using its docker-network IP (the
// host-mapped port is unreachable from inside the liquibase container).
func applyAppLiquibaseMigrations(ctx context.Context, pg *tcpostgres.PostgresContainer, migrationsDir string) error {
	pgHost, err := pg.ContainerIP(ctx)
	if err != nil {
		return fmt.Errorf("postgres container ip: %w", err)
	}
	jdbcURL := fmt.Sprintf("jdbc:postgresql://%s:5432/syntheticbrew_app_test", pgHost)

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
			"--password=syntheticbrew_app_test_pass",
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
