#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# Start Docker daemon (DinD)
# ---------------------------------------------------------------------------

dockerd &>/var/log/dockerd.log &

echo "Waiting for Docker..."
for i in $(seq 1 30); do
    docker info &>/dev/null && break
    sleep 1
    [ "$i" -eq 30 ] && { echo "✗ Docker failed to start"; cat /var/log/dockerd.log; exit 1; }
done
echo "✓ Docker ready"

# ---------------------------------------------------------------------------
# Run vps apply
# ---------------------------------------------------------------------------

vps

# ---------------------------------------------------------------------------
# Checks
# ---------------------------------------------------------------------------

echo ""
echo "Running checks..."
echo ""

fail=0

check_container() {
    local name=$1
    if docker ps --filter "name=^${name}$" --filter status=running -q | grep -q .; then
        echo "  ✓ container: $name"
    else
        echo "  ✗ container: $name (not running)"
        fail=1
    fi
}

check_file() {
    local path=$1
    if [ -f "$path" ]; then
        echo "  ✓ file: $path"
    else
        echo "  ✗ file: $path (missing)"
        fail=1
    fi
}

# Containers
check_container "caddy"
check_container "mariadb"
check_container "myapp-php84"
check_container "myapp2-php84"
check_container "myapp2-scheduler"
check_container "myapp2-worker"

# Generated files
check_file "/opt/vps/generated/docker-compose.yml"
check_file "/opt/vps/generated/caddy/Caddyfile"
check_file "/opt/vps/generated/caddy/sites/myapp.caddy"
check_file "/opt/vps/generated/caddy/sites/myapp2.caddy"
check_file "/opt/vps/generated/env/myapp.env"
check_file "/opt/vps/generated/env/myapp2.env"
check_file "/opt/vps/secrets/db_root_password"
check_file "/opt/vps/secrets/db_myapp2_password"

# MySQL database
echo ""
echo "Checking MySQL..."
ROOT_PASS=$(cat /opt/vps/secrets/db_root_password)
if docker exec mariadb mysql -uroot "-p${ROOT_PASS}" -e "SHOW DATABASES LIKE 'myapp2';" 2>/dev/null | grep -q myapp2; then
    echo "  ✓ database: myapp2"
else
    echo "  ✗ database: myapp2 (not found)"
    fail=1
fi

# Idempotency — run a second time, must succeed cleanly
echo ""
echo "Testing idempotency (second apply)..."
vps && echo "  ✓ second apply succeeded"

echo ""
if [ "$fail" -eq 0 ]; then
    echo "All checks passed."
else
    echo "Some checks failed."
    exit 1
fi
