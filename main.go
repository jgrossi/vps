package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

var supportedPHP = []string{"8.4", "8.5"}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

const (
	clrGreen = "\033[32m"
	clrRed   = "\033[31m"
	clrBlue  = "\033[34m"
	clrBold  = "\033[1m"
	clrDim   = "\033[2m"
	clrReset = "\033[0m"
)

func printOk(msg string)   { fmt.Printf("  %s✓%s %s\n", clrGreen, clrReset, msg) }
func printFail(msg string) { fmt.Fprintf(os.Stderr, "  %s✗%s %s\n", clrRed, clrReset, msg) }
func printInfo(msg string) { fmt.Printf("  %s→%s %s\n", clrBlue, clrReset, msg) }
func printSkip(msg string) { fmt.Printf("  %s· %s%s\n", clrDim, msg, clrReset) }
func printStep(msg string) { fmt.Printf("\n%s%s%s\n", clrBold, msg, clrReset) }

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

type App struct {
	Name      string `yaml:"name"`
	Domain    string `yaml:"domain"`
	PHP       string `yaml:"php"`
	Database  string `yaml:"database"`
	Scheduler bool   `yaml:"scheduler"`
	Queue     bool   `yaml:"queue"`
	DBName    string `yaml:"db_name"`
	DBUser    string `yaml:"db_user"`
}

func (a App) PhpTag() string     { return strings.ReplaceAll(a.PHP, ".", "") }
func (a App) PhpService() string { return fmt.Sprintf("%s-php%s", a.Name, a.PhpTag()) }

type Config struct {
	Apps       []App  `yaml:"apps"`
	AdminEmail string `yaml:"admin_email"`
}

func (c Config) MySQLApps() []App {
	var out []App
	for _, a := range c.Apps {
		if a.Database == "mysql" {
			out = append(out, a)
		}
	}
	return out
}

func (c Config) PHPVersions() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range c.Apps {
		if !seen[a.PHP] {
			seen[a.PHP] = true
			out = append(out, a.PHP)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Config loading + validation
// ---------------------------------------------------------------------------

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("YAML parse error: %w", err)
	}
	if cfg.AdminEmail == "" {
		cfg.AdminEmail = "admin@example.com"
	}
	return cfg, nil
}

func validateConfig(cfg Config) []string {
	nameRe := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	phpOk := map[string]bool{}
	for _, v := range supportedPHP {
		phpOk[v] = true
	}

	var errs []string
	seenNames, seenDomains, seenDBNames := map[string]bool{}, map[string]bool{}, map[string]bool{}

	for _, app := range cfg.Apps {
		if !nameRe.MatchString(app.Name) {
			errs = append(errs, fmt.Sprintf("invalid app name '%s' — use lowercase letters, numbers, hyphens, underscores", app.Name))
		}
		if !phpOk[app.PHP] {
			errs = append(errs, fmt.Sprintf("app '%s': PHP %s not supported (supported: %s)", app.Name, app.PHP, strings.Join(supportedPHP, ", ")))
		}
		if app.Database != "mysql" && app.Database != "sqlite" {
			errs = append(errs, fmt.Sprintf("app '%s': database must be 'mysql' or 'sqlite'", app.Name))
		}
		if app.Database == "mysql" && (app.DBName == "" || app.DBUser == "") {
			errs = append(errs, fmt.Sprintf("app '%s': mysql requires db_name and db_user", app.Name))
		}
		if seenNames[app.Name] {
			errs = append(errs, fmt.Sprintf("duplicate app name: '%s'", app.Name))
		}
		if seenDomains[app.Domain] {
			errs = append(errs, fmt.Sprintf("duplicate domain: '%s'", app.Domain))
		}
		if app.DBName != "" && seenDBNames[app.DBName] {
			errs = append(errs, fmt.Sprintf("duplicate db_name: '%s'", app.DBName))
		}
		seenNames[app.Name] = true
		seenDomains[app.Domain] = true
		if app.DBName != "" {
			seenDBNames[app.DBName] = true
		}
	}
	return errs
}

// ---------------------------------------------------------------------------
// Secrets
// ---------------------------------------------------------------------------

func generatePassword(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[idx.Int64()]
	}
	return string(b)
}

func ensureSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	pw := generatePassword(32)
	return pw, os.WriteFile(path, []byte(pw), 0600)
}

// ---------------------------------------------------------------------------
// File helpers
// ---------------------------------------------------------------------------

var requiredDirs = []string{
	"generated/caddy/sites",
	"generated/env",
	"secrets",
	"backups",
}

func ensureDirs(base string) error {
	for _, d := range requiredDirs {
		if err := os.MkdirAll(filepath.Join(base, d), 0755); err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes content to path. If skipIfExists is true and the file
// already exists, it does nothing and returns (false, nil).
func writeFile(path, content string, skipIfExists bool) (bool, error) {
	if skipIfExists {
		if _, err := os.Stat(path); err == nil {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(content), 0644)
}

func renderTmpl(src string, data any) (string, error) {
	t, err := template.New("").Parse(src)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	return sb.String(), t.Execute(&sb, data)
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

const caddyfileTmpl = `{
    email {{.AdminEmail}}
}

import /etc/caddy/sites/*
`

const siteTmpl = `{{.Domain}} {
    root * /var/www/{{.Name}}/public
    php_fastcgi {{.PhpService}}:9000
    file_server
    encode gzip
}
`

const dockerfileTmpl = `FROM php:{{.Version}}-fpm-alpine

RUN apk add --no-cache \
    freetype-dev \
    icu-dev \
    libjpeg-turbo-dev \
    libpng-dev \
    libwebp-dev \
    libzip-dev \
    sqlite-dev \
    $PHPIZE_DEPS

RUN docker-php-ext-configure gd \
        --with-freetype \
        --with-jpeg \
        --with-webp \
    && docker-php-ext-install -j$(nproc) \
        bcmath \
        exif \
        gd \
        intl \
        mbstring \
        opcache \
        pcntl \
        pdo_mysql \
        pdo_sqlite \
        zip \
    && apk del $PHPIZE_DEPS \
    && rm -rf /var/cache/apk/* /tmp/*

COPY --from=composer:latest /usr/bin/composer /usr/bin/composer

WORKDIR /var/www

EXPOSE 9000
`

const envTmpl = `APP_NAME={{.App.Name}}
APP_ENV=production
APP_KEY=
APP_DEBUG=false
APP_URL=https://{{.App.Domain}}

LOG_CHANNEL=stack
LOG_LEVEL=debug

DB_CONNECTION={{.App.Database}}
{{if eq .App.Database "mysql"}}DB_HOST=mariadb
DB_PORT=3306
DB_DATABASE={{.App.DBName}}
DB_USERNAME={{.App.DBUser}}
DB_PASSWORD={{.DBPassword}}
{{end}}CACHE_DRIVER=file
SESSION_DRIVER=file
QUEUE_CONNECTION={{if .App.Queue}}database{{else}}sync{{end}}

MAIL_MAILER=log
`

// ---------------------------------------------------------------------------
// Compose generator (built programmatically to avoid YAML whitespace issues)
// ---------------------------------------------------------------------------

func genCompose(cfg Config, base string) string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }

	w("# Generated by vps — do not edit manually\n\nservices:\n")

	w("  caddy:\n")
	w("    image: caddy:2-alpine\n")
	w("    container_name: caddy\n")
	w("    restart: unless-stopped\n")
	w("    ports:\n")
	w("      - \"80:80\"\n")
	w("      - \"443:443\"\n")
	w("      - \"443:443/udp\"\n")
	w("    volumes:\n")
	w("      - %s/generated/caddy/Caddyfile:/etc/caddy/Caddyfile:ro\n", base)
	w("      - %s/generated/caddy/sites:/etc/caddy/sites:ro\n", base)
	w("      - /var/www:/var/www:ro\n")
	w("      - caddy_data:/data\n")
	w("      - caddy_config:/config\n")
	w("    networks:\n")
	w("      - vps\n")

	w("\n  mariadb:\n")
	w("    image: mariadb:11\n")
	w("    container_name: mariadb\n")
	w("    restart: unless-stopped\n")
	w("    env_file:\n")
	w("      - %s/generated/env/infrastructure.env\n", base)
	w("    volumes:\n")
	w("      - mariadb_data:/var/lib/mysql\n")
	w("    healthcheck:\n")
	w("      test: [\"CMD\", \"mysqladmin\", \"ping\", \"-h\", \"127.0.0.1\", \"--silent\"]\n")
	w("      interval: 10s\n")
	w("      timeout: 5s\n")
	w("      retries: 5\n")
	w("      start_period: 30s\n")
	w("    networks:\n")
	w("      - vps\n")

	for _, app := range cfg.Apps {
		w("\n  %s:\n", app.PhpService())
		w("    build:\n")
		w("      context: %s\n", base)
		w("      dockerfile: Dockerfile.php%s\n", app.PhpTag())
		w("    image: vps-php%s:latest\n", app.PhpTag())
		w("    container_name: %s\n", app.PhpService())
		w("    restart: unless-stopped\n")
		w("    volumes:\n")
		w("      - /var/www/%s:/var/www/%s\n", app.Name, app.Name)
		w("    working_dir: /var/www/%s\n", app.Name)
		w("    env_file:\n")
		w("      - %s/generated/env/%s.env\n", base, app.Name)
		if app.Database == "mysql" {
			w("    depends_on:\n")
			w("      mariadb:\n")
			w("        condition: service_healthy\n")
		}
		w("    networks:\n")
		w("      - vps\n")

		if app.Scheduler {
			w("\n  %s-scheduler:\n", app.Name)
			w("    image: vps-php%s:latest\n", app.PhpTag())
			w("    container_name: %s-scheduler\n", app.Name)
			w("    restart: unless-stopped\n")
			w("    volumes:\n")
			w("      - /var/www/%s:/var/www/%s\n", app.Name, app.Name)
			w("    working_dir: /var/www/%s\n", app.Name)
			w("    env_file:\n")
			w("      - %s/generated/env/%s.env\n", base, app.Name)
			w("    command: sh -c \"while true; do php artisan schedule:run >> /dev/null 2>&1; sleep 60; done\"\n")
			w("    depends_on:\n")
			w("      - %s\n", app.PhpService())
			w("    networks:\n")
			w("      - vps\n")
		}

		if app.Queue {
			w("\n  %s-worker:\n", app.Name)
			w("    image: vps-php%s:latest\n", app.PhpTag())
			w("    container_name: %s-worker\n", app.Name)
			w("    restart: unless-stopped\n")
			w("    volumes:\n")
			w("      - /var/www/%s:/var/www/%s\n", app.Name, app.Name)
			w("    working_dir: /var/www/%s\n", app.Name)
			w("    env_file:\n")
			w("      - %s/generated/env/%s.env\n", base, app.Name)
			w("    command: php artisan queue:work --sleep=3 --tries=3 --max-time=3600\n")
			w("    depends_on:\n")
			w("      - %s\n", app.PhpService())
			w("    networks:\n")
			w("      - vps\n")
		}
	}

	w("\nvolumes:\n  caddy_data:\n  caddy_config:\n  mariadb_data:\n")
	w("\nnetworks:\n  vps:\n    driver: bridge\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// File generation
// ---------------------------------------------------------------------------

func generateFiles(cfg Config, base, rootPass string, dbPasswords map[string]string) error {
	if _, err := writeFile(filepath.Join(base, "generated", "docker-compose.yml"), genCompose(cfg, base), false); err != nil {
		return fmt.Errorf("docker-compose.yml: %w", err)
	}
	printOk("docker-compose.yml")

	caddyfile, err := renderTmpl(caddyfileTmpl, cfg)
	if err != nil {
		return err
	}
	if _, err := writeFile(filepath.Join(base, "generated", "caddy", "Caddyfile"), caddyfile, false); err != nil {
		return fmt.Errorf("Caddyfile: %w", err)
	}
	for _, app := range cfg.Apps {
		site, err := renderTmpl(siteTmpl, app)
		if err != nil {
			return err
		}
		if _, err := writeFile(filepath.Join(base, "generated", "caddy", "sites", app.Name+".caddy"), site, false); err != nil {
			return fmt.Errorf("site config for %s: %w", app.Name, err)
		}
	}
	printOk(fmt.Sprintf("Caddy config (%d site(s))", len(cfg.Apps)))

	for _, version := range cfg.PHPVersions() {
		dockerfile, err := renderTmpl(dockerfileTmpl, struct{ Version string }{version})
		if err != nil {
			return err
		}
		tag := strings.ReplaceAll(version, ".", "")
		if _, err := writeFile(filepath.Join(base, "Dockerfile.php"+tag), dockerfile, false); err != nil {
			return fmt.Errorf("Dockerfile for PHP %s: %w", version, err)
		}
	}
	printOk(fmt.Sprintf("Dockerfile(s) for PHP %s", strings.Join(cfg.PHPVersions(), ", ")))

	if _, err := writeFile(
		filepath.Join(base, "generated", "env", "infrastructure.env"),
		fmt.Sprintf("MARIADB_ROOT_PASSWORD=%s\n", rootPass),
		false,
	); err != nil {
		return fmt.Errorf("infrastructure.env: %w", err)
	}

	for _, app := range cfg.Apps {
		content, err := renderTmpl(envTmpl, struct {
			App        App
			DBPassword string
		}{app, dbPasswords[app.Name]})
		if err != nil {
			return err
		}
		path := filepath.Join(base, "generated", "env", app.Name+".env")
		created, err := writeFile(path, content, true)
		if err != nil {
			return fmt.Errorf("env for %s: %w", app.Name, err)
		}
		if created {
			os.Chmod(path, 0600) //nolint:errcheck
			printInfo(fmt.Sprintf("Created %s.env — set APP_KEY before going live", app.Name))
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Docker
// ---------------------------------------------------------------------------

func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func composeRun(base string, args ...string) error {
	all := append([]string{"compose", "-f", filepath.Join(base, "generated", "docker-compose.yml")}, args...)
	cmd := exec.Command("docker", all...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func dockerExec(container string, args ...string) (string, error) {
	all := append([]string{"exec", container}, args...)
	out, err := exec.Command("docker", all...).Output()
	return string(out), err
}

// ---------------------------------------------------------------------------
// Databases
// ---------------------------------------------------------------------------

func createDatabases(cfg Config, base string) error {
	rootPass, err := ensureSecret(filepath.Join(base, "secrets", "db_root_password"))
	if err != nil {
		return err
	}

	for _, app := range cfg.MySQLApps() {
		dbPass, err := ensureSecret(filepath.Join(base, "secrets", fmt.Sprintf("db_%s_password", app.Name)))
		if err != nil {
			return err
		}

		mysql := func(sql string) (string, error) {
			return dockerExec("mariadb", "mysql", "-uroot", "-p"+rootPass, "-e", sql)
		}

		existing, err := mysql(fmt.Sprintf("SHOW DATABASES LIKE '%s';", app.DBName))
		if err != nil {
			return fmt.Errorf("checking database %s: %w", app.DBName, err)
		}
		if !strings.Contains(existing, app.DBName) {
			if _, err := mysql(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", app.DBName)); err != nil {
				return fmt.Errorf("creating database %s: %w", app.DBName, err)
			}
			printOk("Created database: " + app.DBName)
		} else {
			printSkip("Database exists: " + app.DBName)
		}

		users, err := mysql(fmt.Sprintf("SELECT User FROM mysql.user WHERE User='%s';", app.DBUser))
		if err != nil {
			return fmt.Errorf("checking user %s: %w", app.DBUser, err)
		}
		if !strings.Contains(users, app.DBUser) {
			sql := fmt.Sprintf(
				"CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';"+
					"GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';"+
					"FLUSH PRIVILEGES;",
				app.DBUser, dbPass, app.DBName, app.DBUser,
			)
			if _, err := mysql(sql); err != nil {
				return fmt.Errorf("creating user %s: %w", app.DBUser, err)
			}
			printOk("Created user: " + app.DBUser)
		} else {
			printSkip("User exists: " + app.DBUser)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

func apply(cfg Config, base string) error {
	printStep("Checking environment")
	if !dockerAvailable() {
		return fmt.Errorf("Docker is not installed or not running")
	}
	printOk("Docker available")

	printStep("Preparing")
	if err := ensureDirs(base); err != nil {
		return fmt.Errorf("creating directories: %w", err)
	}
	rootPass, err := ensureSecret(filepath.Join(base, "secrets", "db_root_password"))
	if err != nil {
		return err
	}
	dbPasswords := map[string]string{}
	for _, app := range cfg.MySQLApps() {
		pw, err := ensureSecret(filepath.Join(base, "secrets", fmt.Sprintf("db_%s_password", app.Name)))
		if err != nil {
			return err
		}
		dbPasswords[app.Name] = pw
	}
	printOk("Directories and secrets ready")

	printStep("Generating configuration")
	if err := generateFiles(cfg, base, rootPass, dbPasswords); err != nil {
		return err
	}

	printStep("Building images")
	if err := composeRun(base, "build"); err != nil {
		return fmt.Errorf("docker compose build failed: %w", err)
	}

	printStep("Starting containers")
	if err := composeRun(base, "up", "-d", "--wait", "--remove-orphans"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	printOk("All containers up")

	printStep("Provisioning databases")
	if err := createDatabases(cfg, base); err != nil {
		printFail("Database error: " + err.Error())
		// non-fatal — containers are running, operator can investigate
	}

	printStep("Reloading Caddy")
	if _, err := dockerExec("caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile"); err != nil {
		printInfo("Caddy reload skipped (may still be starting up)")
	} else {
		printOk("Caddy reloaded")
	}

	fmt.Printf("\n%s%sDone.%s %d app(s) running.\n\n", clrGreen, clrBold, clrReset, len(cfg.Apps))
	return nil
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	base := envOr("VPS_BASE", "/opt/vps")

	var configPath string
	flag.StringVar(&configPath, "config", filepath.Join(base, "apps.yml"), "path to apps.yml")
	flag.StringVar(&configPath, "c", filepath.Join(base, "apps.yml"), "path to apps.yml (shorthand)")
	flag.Parse()

	cfg, err := loadConfig(configPath)
	if err != nil {
		printFail(err.Error())
		os.Exit(1)
	}

	if errs := validateConfig(cfg); len(errs) > 0 {
		for _, e := range errs {
			printFail(e)
		}
		os.Exit(1)
	}

	if err := apply(cfg, base); err != nil {
		printFail(err.Error())
		os.Exit(1)
	}
}
