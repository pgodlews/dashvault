#!/bin/bash
# configure-tls.sh — TLS setup for the teslausb web interface.
#
# Modes:
#   self-signed   Generate a self-signed cert valid for 10 years (default).
#   letsencrypt   Obtain a cert via Certbot DNS-01 challenge (requires a domain
#                 and DNS API credentials; see Certbot docs for your provider).
#
# Usage (during setup, called from setup-teslausb or run standalone):
#   TLS_MODE=self-signed  ./configure-tls.sh     # default
#   TLS_MODE=letsencrypt  LETSENCRYPT_DOMAIN=teslausb.example.com \
#     LETSENCRYPT_EMAIL=you@example.com          ./configure-tls.sh
#
# After running, nginx listens on :443 with a :80→:443 redirect.
# The HSTS header is set with a 1-year max-age.
set -euo pipefail

CERT_DIR=/etc/nginx/tls
NGINX_CONF=/etc/nginx/sites-available/teslausb.nginx
TLS_MODE="${TLS_MODE:-self-signed}"
LETSENCRYPT_DOMAIN="${LETSENCRYPT_DOMAIN:-}"
LETSENCRYPT_EMAIL="${LETSENCRYPT_EMAIL:-}"

info()  { echo "[tls] $*"; }
die()   { echo "[tls] ERROR: $*" >&2; exit 1; }

require_cmd() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }

# ── cert generation ──────────────────────────────────────────────────────────

generate_self_signed() {
    info "Generating self-signed certificate (valid 10 years)..."
    mkdir -p "$CERT_DIR"
    chmod 700 "$CERT_DIR"

    HOSTNAME="${TESLAUSB_HOSTNAME:-dashvault}"

    openssl req -x509 -nodes -days 3650 \
        -newkey rsa:2048 \
        -keyout "$CERT_DIR/teslausb.key" \
        -out    "$CERT_DIR/teslausb.crt" \
        -subj   "/CN=${HOSTNAME}" \
        -addext "subjectAltName=DNS:${HOSTNAME},DNS:${HOSTNAME}.local,IP:$(hostname -I | awk '{print $1}')"

    chmod 600 "$CERT_DIR/teslausb.key"
    chmod 644 "$CERT_DIR/teslausb.crt"
    info "Self-signed cert written to $CERT_DIR/teslausb.{crt,key}"
}

obtain_letsencrypt() {
    [ -n "$LETSENCRYPT_DOMAIN" ] || die "LETSENCRYPT_DOMAIN must be set for letsencrypt mode"
    [ -n "$LETSENCRYPT_EMAIL"  ] || die "LETSENCRYPT_EMAIL must be set for letsencrypt mode"

    require_cmd certbot

    info "Requesting Let's Encrypt cert for $LETSENCRYPT_DOMAIN (DNS-01)..."
    certbot certonly \
        --non-interactive \
        --agree-tos \
        --email  "$LETSENCRYPT_EMAIL" \
        --dns-cloudflare \
        --dns-cloudflare-credentials /root/.cloudflare.ini \
        -d "$LETSENCRYPT_DOMAIN"

    CERT_DIR="/etc/letsencrypt/live/$LETSENCRYPT_DOMAIN"
    # Symlink into the standard path so the nginx snippet below is identical.
    mkdir -p /etc/nginx/tls
    ln -sf "$CERT_DIR/fullchain.pem" /etc/nginx/tls/teslausb.crt
    ln -sf "$CERT_DIR/privkey.pem"   /etc/nginx/tls/teslausb.key

    # Auto-renew hook: reload nginx after renewal.
    cat > /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh <<'HOOK'
#!/bin/bash
systemctl reload nginx
HOOK
    chmod +x /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
    info "Let's Encrypt cert installed; auto-renewal hook configured."
}

# ── nginx config patch ───────────────────────────────────────────────────────

patch_nginx() {
    info "Patching nginx config for TLS..."

    # Write a new config that keeps all existing proxy/location blocks but
    # switches to port 443 and adds a port-80 redirect.
    # We do this by rewriting the listen lines and injecting TLS directives.
    python3 - "$NGINX_CONF" <<'PYEOF'
import sys, re, pathlib

path = pathlib.Path(sys.argv[1])
src  = path.read_text()

# Replace the two listen lines (80 + [::]:80) with TLS equivalents.
src = re.sub(
    r'listen\s+80\s+default_server\s*;',
    'listen 443 ssl default_server;',
    src
)
src = re.sub(
    r'listen\s+\[::\]:80\s+default_server\s*;',
    'listen [::]:443 ssl default_server;',
    src
)

# Inject TLS directives right after the second listen line.
tls_block = """
    ssl_certificate     /etc/nginx/tls/teslausb.crt;
    ssl_certificate_key /etc/nginx/tls/teslausb.key;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 10m;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
"""
src = src.replace(
    'listen [::]:443 ssl default_server;',
    'listen [::]:443 ssl default_server;' + tls_block,
    1
)

path.write_text(src)
print("patched:", path)
PYEOF

    # Prepend an HTTP→HTTPS redirect server block.
    REDIRECT_CONF=/etc/nginx/snippets/teslausb-http-redirect.conf
    cat > "$REDIRECT_CONF" <<'NGINX'
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    return 301 https://$host$request_uri;
}
NGINX

    # Include the redirect snippet from the main config if not already there.
    if ! grep -q "teslausb-http-redirect" "$NGINX_CONF"; then
        # Insert include at the very top of the file (before the first server block).
        sed -i "1s|^|include /etc/nginx/snippets/teslausb-http-redirect.conf;\n|" "$NGINX_CONF"
    fi

    if nginx -t; then
        systemctl reload nginx
        info "nginx reloaded with TLS."
    else
        die "nginx config test failed — check $NGINX_CONF"
    fi
}

# ── main ─────────────────────────────────────────────────────────────────────

require_cmd openssl
require_cmd nginx

case "$TLS_MODE" in
    self-signed)
        generate_self_signed
        ;;
    letsencrypt)
        obtain_letsencrypt
        ;;
    *)
        die "Unknown TLS_MODE=$TLS_MODE (use 'self-signed' or 'letsencrypt')"
        ;;
esac

patch_nginx

info "TLS setup complete."
info "  Certificate: /etc/nginx/tls/teslausb.crt"
info "  To verify:   openssl x509 -in /etc/nginx/tls/teslausb.crt -noout -text | head -20"
info "  Logs:        journalctl -u nginx"
