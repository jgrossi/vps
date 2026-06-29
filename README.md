# vps

Provision a self-hosted Laravel platform on a single Ubuntu 24 VPS. One binary, zero runtime dependencies.

## Install

```bash
curl -sSL https://raw.githubusercontent.com/jgrossi/vps/main/install.sh | bash
```

Or grab the binary directly:

```bash
curl -sSL https://github.com/jgrossi/vps/releases/latest/download/vps-linux-amd64 -o /usr/local/bin/vps && chmod +x /usr/local/bin/vps
```

## Usage

```bash
cp apps.yml.example /opt/vps/apps.yml
# edit /opt/vps/apps.yml
vps
```

Re-run any time to converge — adding new apps, changing domains, etc. Existing databases and env files are never overwritten.

## apps.yml

```yaml
admin_email: admin@example.com

apps:
  - name: gallery
    domain: gallery.example.com
    php: "8.5"
    database: sqlite
    scheduler: true
    queue: false

  - name: split
    domain: split.ie
    php: "8.4"
    database: mysql
    scheduler: true
    queue: true
    db_name: split
    db_user: split
```

## Build from source

```bash
go mod tidy
make build          # local binary
make linux          # linux/amd64 + linux/arm64
```

## What it provisions

- **caddy** — reverse proxy + automatic TLS
- **mariadb** — databases
- One PHP-FPM container per app (+ optional scheduler and queue worker)

App code lives at `/var/www/{name}` on the host. Generated config at `/opt/vps/generated/`. Secrets at `/opt/vps/secrets/` (never regenerated).

After first run, set `APP_KEY` in each `/opt/vps/generated/env/{name}.env`.
