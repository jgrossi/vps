package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig("test/apps.yml")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(cfg.Apps))
	}
	if cfg.Apps[0].Name != "myapp" {
		t.Errorf("expected first app 'myapp', got '%s'", cfg.Apps[0].Name)
	}
	if cfg.Apps[1].Database != "mysql" {
		t.Errorf("expected second app database 'mysql', got '%s'", cfg.Apps[1].Database)
	}
}

func TestValidateConfig(t *testing.T) {
	cfg, _ := loadConfig("test/apps.yml")
	errs := validateConfig(cfg)
	if len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
}

func TestValidateDuplicateName(t *testing.T) {
	cfg := Config{Apps: []App{
		{Name: "app", Domain: "a.com", PHP: "8.4", Database: "sqlite"},
		{Name: "app", Domain: "b.com", PHP: "8.4", Database: "sqlite"},
	}}
	if errs := validateConfig(cfg); len(errs) == 0 {
		t.Fatal("expected error for duplicate name")
	}
}

func TestValidateDuplicateDomain(t *testing.T) {
	cfg := Config{Apps: []App{
		{Name: "app1", Domain: "same.com", PHP: "8.4", Database: "sqlite"},
		{Name: "app2", Domain: "same.com", PHP: "8.4", Database: "sqlite"},
	}}
	if errs := validateConfig(cfg); len(errs) == 0 {
		t.Fatal("expected error for duplicate domain")
	}
}

func TestValidateUnsupportedPHP(t *testing.T) {
	cfg := Config{Apps: []App{
		{Name: "app", Domain: "app.com", PHP: "7.4", Database: "sqlite"},
	}}
	if errs := validateConfig(cfg); len(errs) == 0 {
		t.Fatal("expected error for unsupported PHP version")
	}
}

func TestValidateMySQLMissingFields(t *testing.T) {
	cfg := Config{Apps: []App{
		{Name: "app", Domain: "app.com", PHP: "8.4", Database: "mysql"},
	}}
	if errs := validateConfig(cfg); len(errs) == 0 {
		t.Fatal("expected error for mysql missing db_name/db_user")
	}
}

func TestGenerateFiles(t *testing.T) {
	cfg, err := loadConfig("test/apps.yml")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	base := t.TempDir()

	if err := ensureDirs(base); err != nil {
		t.Fatalf("ensureDirs: %v", err)
	}

	rootPass := "testrootpassword"
	dbPasswords := map[string]string{"myapp2": "testdbpassword"}

	if err := generateFiles(cfg, base, rootPass, dbPasswords); err != nil {
		t.Fatalf("generateFiles: %v", err)
	}

	files := []string{
		"generated/docker-compose.yml",
		"generated/caddy/Caddyfile",
		"generated/caddy/sites/myapp.caddy",
		"generated/caddy/sites/myapp2.caddy",
		"generated/env/infrastructure.env",
		"generated/env/myapp.env",
		"generated/env/myapp2.env",
		"Dockerfile.php84",
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Errorf("missing expected file: %s", f)
		}
	}

	// Spot-check compose content
	compose, _ := os.ReadFile(filepath.Join(base, "generated/docker-compose.yml"))
	for _, want := range []string{"myapp-php84", "myapp2-php84", "myapp2-scheduler", "myapp2-worker", "mariadb", "caddy"} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("docker-compose.yml missing: %s", want)
		}
	}

	// mysql app must have depends_on mariadb
	if !strings.Contains(string(compose), "condition: service_healthy") {
		t.Error("docker-compose.yml: mysql app missing mariadb healthcheck dependency")
	}

	// Spot-check Caddyfile
	caddyfile, _ := os.ReadFile(filepath.Join(base, "generated/caddy/Caddyfile"))
	if !strings.Contains(string(caddyfile), "test@example.com") {
		t.Error("Caddyfile missing admin_email")
	}

	// Spot-check env file
	env, _ := os.ReadFile(filepath.Join(base, "generated/env/myapp2.env"))
	if !strings.Contains(string(env), "DB_HOST=mariadb") {
		t.Error("myapp2.env missing DB_HOST")
	}
	if !strings.Contains(string(env), "testdbpassword") {
		t.Error("myapp2.env missing db password")
	}

	// sqlite app must NOT have mysql vars
	envSqlite, _ := os.ReadFile(filepath.Join(base, "generated/env/myapp.env"))
	if strings.Contains(string(envSqlite), "DB_HOST") {
		t.Error("myapp.env (sqlite) should not contain DB_HOST")
	}

	// Idempotency: run again, env files must not be overwritten
	firstEnv := string(env)
	generateFiles(cfg, base, rootPass, dbPasswords) //nolint
	env2, _ := os.ReadFile(filepath.Join(base, "generated/env/myapp2.env"))
	if string(env2) != firstEnv {
		t.Error("env file was overwritten on second generateFiles call")
	}
}

func TestPhpTag(t *testing.T) {
	app := App{PHP: "8.4"}
	if app.PhpTag() != "84" {
		t.Errorf("expected '84', got '%s'", app.PhpTag())
	}
}

func TestPhpService(t *testing.T) {
	app := App{Name: "gallery", PHP: "8.5"}
	if app.PhpService() != "gallery-php85" {
		t.Errorf("expected 'gallery-php85', got '%s'", app.PhpService())
	}
}
