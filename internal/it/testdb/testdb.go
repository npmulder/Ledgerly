package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

const (
	databaseURLEnv = "LEDGERLY_TEST_DB"

	postgresImage = "postgres:16-alpine"
	adminDatabase = "ledgerly_admin"
	adminUser     = "postgres"
	adminPassword = "postgres"

	containerStartupTimeout = 2 * time.Minute
	provisionTimeout        = 60 * time.Second
	cleanupTimeout          = 20 * time.Second

	cloneBudget = 500 * time.Millisecond
)

var defaultManager = newManager()

// Main runs m and then tears down the process-wide test database runtime.
func Main(m *testing.M) int {
	code := m.Run()
	if err := defaultManager.shutdown(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "testdb cleanup: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// New returns the current suite's superuser pool plus an idempotent cleanup
// function. Cleanup is also registered with t.Cleanup.
func New(t testing.TB) (*pgxpool.Pool, func()) {
	t.Helper()

	s := defaultManager.suiteFor(t, defaultSuiteConfig(t))
	return s.rawPool, func() {
		t.Helper()
		if err := s.cleanup(); err != nil {
			t.Fatalf("cleanup test database %s: %v", s.databaseName, err)
		}
	}
}

// Raw returns the current suite's superuser pool.
func Raw(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return defaultManager.suiteFor(t, defaultSuiteConfig(t)).rawPool
}

// AsModule returns a pool connected to the current suite database with SET ROLE
// and search_path pinned to module.
func AsModule(t testing.TB, module string) *pgxpool.Pool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	defer cancel()

	s := defaultManager.suiteFor(t, defaultSuiteConfig(t))
	pool, err := s.modulePool(ctx, module)
	if err != nil {
		t.Fatalf("open %s module pool for %s: %v", module, s.databaseName, err)
	}
	return pool
}

type suiteConfig struct {
	key           string
	migrationsDir string
}

func defaultSuiteConfig(t testing.TB) suiteConfig {
	t.Helper()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repository root: %v", err)
	}
	return suiteConfig{
		key:           t.Name(),
		migrationsDir: filepath.Join(root, "db", "migrations"),
	}
}

type manager struct {
	initOnce sync.Once
	initErr  error
	env      *environment

	mu                 sync.Mutex
	prefix             string
	sequence           atomic.Uint64
	suites             map[string]*suite
	templateName       string
	templateHash       string
	templateBuilds     int
	lastCloneDuration  time.Duration
	templateMigrations string
}

type environment struct {
	adminURL  string
	adminPool *pgxpool.Pool
	container *postgres.PostgresContainer
	source    string
}

func newManager() *manager {
	prefix, err := randomPrefix()
	if err != nil {
		prefix = fmt.Sprintf("ledgerly_it_%d_%d", os.Getpid(), time.Now().UnixNano())
	}
	return &manager{
		prefix: prefix,
		suites: make(map[string]*suite),
	}
}

func (m *manager) suiteFor(t testing.TB, cfg suiteConfig) *suite {
	t.Helper()

	key := cfg.key + "\x00" + cfg.migrationsDir
	m.mu.Lock()
	if s, ok := m.suites[key]; ok {
		m.mu.Unlock()
		return s
	}
	m.mu.Unlock()

	s, err := m.createSuite(t, cfg)
	if err != nil {
		t.Fatalf("provision test database: %v", err)
	}

	m.mu.Lock()
	if existing, ok := m.suites[key]; ok {
		m.mu.Unlock()
		if err := s.close(); err != nil {
			t.Fatalf("cleanup duplicate test database %s: %v", s.databaseName, err)
		}
		return existing
	}
	m.suites[key] = s
	m.mu.Unlock()

	s.cleanup = func() error {
		return m.cleanupSuite(key, s)
	}
	t.Cleanup(func() {
		if err := s.cleanup(); err != nil {
			t.Fatalf("cleanup test database %s: %v", s.databaseName, err)
		}
	})

	return s
}

func (m *manager) createSuite(t testing.TB, cfg suiteConfig) (*suite, error) {
	t.Helper()

	env := m.ensureEnvironment(t)

	ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
	defer cancel()

	template, err := m.ensureTemplate(ctx, env, cfg.migrationsDir)
	if err != nil {
		return nil, err
	}

	dbName := m.nextDatabaseName()
	start := time.Now()
	if _, err := env.adminPool.Exec(ctx, "CREATE DATABASE "+identifier(dbName)+" TEMPLATE "+identifier(template.name)); err != nil {
		return nil, fmt.Errorf("create suite database %s from template %s: %w", dbName, template.name, err)
	}
	cloneDuration := time.Since(start)

	rawPool, err := openDatabasePool(ctx, env.adminURL, dbName)
	if err != nil {
		_ = dropDatabase(context.Background(), env.adminPool, dbName)
		return nil, fmt.Errorf("open suite database %s: %w", dbName, err)
	}

	m.mu.Lock()
	m.lastCloneDuration = cloneDuration
	m.mu.Unlock()

	t.Logf("testdb suite database %s provisioned from template %s in %s", dbName, template.name, cloneDuration)

	return &suite{
		adminPool:     env.adminPool,
		adminURL:      env.adminURL,
		databaseName:  dbName,
		rawPool:       rawPool,
		modulePools:   make(map[string]*pgxpool.Pool),
		cloneDuration: cloneDuration,
	}, nil
}

func (m *manager) ensureEnvironment(t testing.TB) *environment {
	t.Helper()

	m.initOnce.Do(func() {
		m.env, m.initErr = startEnvironment()
	})
	if m.initErr == nil {
		return m.env
	}

	var unavailable unavailableError
	if errors.As(m.initErr, &unavailable) {
		t.Skipf("testdb requires Docker or %s: %v", databaseURLEnv, m.initErr)
	}
	t.Fatalf("initialize testdb runtime: %v", m.initErr)
	return nil
}

func startEnvironment() (*environment, error) {
	if databaseURL := strings.TrimSpace(os.Getenv(databaseURLEnv)); databaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
		defer cancel()

		adminPool, err := openDatabasePool(ctx, databaseURL, "")
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", databaseURLEnv, err)
		}
		return &environment{
			adminURL:  databaseURL,
			adminPool: adminPool,
			source:    databaseURLEnv,
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	ctr, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase(adminDatabase),
		postgres.WithUsername(adminUser),
		postgres.WithPassword(adminPassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, unavailableError{err: fmt.Errorf("start %s testcontainer: %w", postgresImage, err)}
	}

	databaseURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return nil, fmt.Errorf("read testcontainer connection string: %w", err)
	}

	adminPool, err := openDatabasePool(ctx, databaseURL, "")
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)
		return nil, fmt.Errorf("connect to testcontainer postgres: %w", err)
	}

	return &environment{
		adminURL:  databaseURL,
		adminPool: adminPool,
		container: ctr,
		source:    "testcontainers",
	}, nil
}

type unavailableError struct {
	err error
}

func (e unavailableError) Error() string {
	return e.err.Error()
}

func (m *manager) ensureTemplate(ctx context.Context, env *environment, migrationsDir string) (templateInfo, error) {
	hash, err := migrationHash(migrationsDir)
	if err != nil {
		return templateInfo{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.templateName != "" && m.templateHash == hash {
		return templateInfo{name: m.templateName, hash: m.templateHash}, nil
	}

	oldTemplate := m.templateName
	templateName := fmt.Sprintf("%s_template_%s", m.prefix, hash[:12])
	if err := dropDatabase(ctx, env.adminPool, templateName); err != nil {
		return templateInfo{}, err
	}
	if _, err := env.adminPool.Exec(ctx, "CREATE DATABASE "+identifier(templateName)); err != nil {
		return templateInfo{}, fmt.Errorf("create template database %s: %w", templateName, err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = dropDatabase(context.Background(), env.adminPool, templateName)
		}
	}()

	templatePool, err := openDatabasePool(ctx, env.adminURL, templateName)
	if err != nil {
		return templateInfo{}, fmt.Errorf("open template database %s: %w", templateName, err)
	}
	if _, err := db.MigrateDir(ctx, templatePool, migrationsDir); err != nil {
		templatePool.Close()
		return templateInfo{}, fmt.Errorf("migrate template database %s: %w", templateName, err)
	}
	templatePool.Close()

	if _, err := env.adminPool.Exec(ctx, "ALTER DATABASE "+identifier(templateName)+" WITH ALLOW_CONNECTIONS false"); err != nil {
		return templateInfo{}, fmt.Errorf("seal template database %s: %w", templateName, err)
	}

	m.templateName = templateName
	m.templateHash = hash
	m.templateBuilds++
	m.templateMigrations = migrationsDir
	committed = true

	if oldTemplate != "" && oldTemplate != templateName {
		if err := dropDatabase(ctx, env.adminPool, oldTemplate); err != nil {
			return templateInfo{}, err
		}
	}

	return templateInfo{name: templateName, hash: hash}, nil
}

type templateInfo struct {
	name string
	hash string
}

func (m *manager) nextDatabaseName() string {
	return fmt.Sprintf("%s_%d", m.prefix, m.sequence.Add(1))
}

func (m *manager) cleanupSuite(key string, s *suite) error {
	m.mu.Lock()
	if m.suites[key] == s {
		delete(m.suites, key)
	}
	m.mu.Unlock()
	return s.close()
}

func (m *manager) shutdown() error {
	m.mu.Lock()
	suites := make([]*suite, 0, len(m.suites))
	for _, s := range m.suites {
		suites = append(suites, s)
	}
	m.suites = make(map[string]*suite)
	templateName := m.templateName
	m.templateName = ""
	env := m.env
	m.mu.Unlock()

	var errs []error
	for _, s := range suites {
		if err := s.close(); err != nil {
			errs = append(errs, err)
		}
	}

	if env != nil {
		if templateName != "" {
			ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			if err := dropDatabase(ctx, env.adminPool, templateName); err != nil {
				errs = append(errs, err)
			}
			cancel()
		}
		if env.adminPool != nil {
			env.adminPool.Close()
		}
		if env.container != nil {
			if err := testcontainers.TerminateContainer(env.container); err != nil {
				errs = append(errs, fmt.Errorf("terminate postgres testcontainer: %w", err))
			}
		}
	}

	return errors.Join(errs...)
}

type suite struct {
	adminPool    *pgxpool.Pool
	adminURL     string
	databaseName string
	rawPool      *pgxpool.Pool

	mu          sync.Mutex
	modulePools map[string]*pgxpool.Pool

	closeOnce     sync.Once
	closeErr      error
	cleanup       func() error
	cloneDuration time.Duration
}

func (s *suite) modulePool(ctx context.Context, module string) (*pgxpool.Pool, error) {
	if err := db.ValidateModule(module); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if pool, ok := s.modulePools[module]; ok {
		s.mu.Unlock()
		return pool, nil
	}
	s.mu.Unlock()

	pool, err := openDatabasePool(ctx, s.adminURL, s.databaseName, db.WithModule(module))
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.modulePools[module]; ok {
		pool.Close()
		return existing, nil
	}
	s.modulePools[module] = pool
	return pool, nil
}

func (s *suite) close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		for _, pool := range s.modulePools {
			pool.Close()
		}
		s.modulePools = make(map[string]*pgxpool.Pool)
		s.mu.Unlock()

		if s.rawPool != nil {
			s.rawPool.Close()
		}

		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		s.closeErr = dropDatabase(ctx, s.adminPool, s.databaseName)
	})
	return s.closeErr
}

func openDatabasePool(ctx context.Context, databaseURL string, databaseName string, opts ...db.PoolOption) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if databaseName != "" {
		cfg.ConnConfig.Database = databaseName
	}
	cfg.MinConns = 0
	if cfg.MaxConns == 0 || cfg.MaxConns > 4 {
		cfg.MaxConns = 4
	}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}

func dropDatabase(ctx context.Context, pool *pgxpool.Pool, databaseName string) error {
	if _, err := pool.Exec(ctx, "DROP DATABASE IF EXISTS "+identifier(databaseName)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop database %s: %w", databaseName, err)
	}
	return nil
}

func migrationHash(dir string) (string, error) {
	var paths []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", fmt.Errorf("hash migrations: %w", err)
	}
	sort.Strings(paths)

	hash := sha256.New()
	for _, rel := range paths {
		bytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", rel, err)
		}
		_, _ = hash.Write([]byte(rel))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(bytes)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod from %s", dir)
		}
		dir = parent
	}
}

func randomPrefix() (string, error) {
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("ledgerly_it_%d_%s", os.Getpid(), hex.EncodeToString(bytes[:])), nil
}

func identifier(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

type managerStats struct {
	Source             string
	TemplateName       string
	TemplateHash       string
	TemplateBuilds     int
	LastCloneDuration  time.Duration
	TemplateMigrations string
}

func (m *manager) stats() managerStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	source := ""
	if m.env != nil {
		source = m.env.source
	}
	return managerStats{
		Source:             source,
		TemplateName:       m.templateName,
		TemplateHash:       m.templateHash,
		TemplateBuilds:     m.templateBuilds,
		LastCloneDuration:  m.lastCloneDuration,
		TemplateMigrations: m.templateMigrations,
	}
}
