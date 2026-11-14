#!/usr/bin/env bash
# FireZAP API installer — any Linux server with Docker (Ubuntu, Debian, Rocky/Alma, Fedora…).
#
# Downloads the published image (nothing is compiled on the server) and starts PostgreSQL and
# the API in /opt/firezap-api. HTTPS, one of two ways:
#   • Caddy (default): takes ports 80/443 and gets Let's Encrypt certificates by itself.
#   • --behind-proxy: the server already has a web server (nginx, Apache, aaPanel, cPanel…);
#     the API stays on 127.0.0.1 and the installer prints the proxy settings to add.
#
#   curl -fsSL https://raw.githubusercontent.com/firezaprepo/firezap-api/main/install.sh -o install.sh
#   sudo bash install.sh
#
# Without questions:
#   sudo bash install.sh --domain api.example.com --email you@example.com --yes
#   sudo bash install.sh --domain api.example.com --behind-proxy --port 8097 --yes
#
# Update to the latest image (keeps keys and data):   sudo bash /opt/firezap-api/install.sh --update
#
# One DNS name (A record) pointing to this server is enough: the dashboard is at /dashboard/.
# --panel-domain adds a second name whose root opens the dashboard.
set -euo pipefail

RAW="https://raw.githubusercontent.com/firezaprepo/firezap-api"
BRANCH="main"
DIR="/opt/firezap-api"
VERSION="latest"
DOMAIN=""
PANEL_DOMAIN=""
EMAIL=""
LANGUAGE=""
PORT="8080"
MODE="" # caddy | proxy
UPDATE=0
DRY_RUN=0
ASSUME_YES=0

c_ok=$'\033[32m'; c_warn=$'\033[33m'; c_err=$'\033[31m'; c_b=$'\033[1m'; c_0=$'\033[0m'
info() { printf '%s\n' "${c_b}==>${c_0} $*"; }
ok()   { printf '%s\n' "${c_ok}  ✓${c_0} $*"; }
warn() { printf '%s\n' "${c_warn}  !${c_0} $*"; }
die()  { printf '%s\n' "${c_err}  ✗ $*${c_0}" >&2; exit 1; }

usage() {
  cat <<'EOF'
FireZAP API installer

Options:
  --domain NAME         Address of the API, e.g. api.example.com (also --api-domain)
  --panel-domain NAME   Optional second address whose root opens the dashboard
  --email ADDRESS       E-mail for Let's Encrypt notices (Caddy mode, optional)
  --behind-proxy        Use the web server already on this machine instead of Caddy
  --port N              Local port of the API on 127.0.0.1 (default 8080)
  --lang en|pt-BR       Dashboard language (default: en)
  --version TAG         Image tag to run (default latest)
  --dir PATH            Install directory (default /opt/firezap-api)
  --update              Update an existing installation to the latest image
  --yes                 Do not ask for confirmation
  --dry-run             Check the input and show what would be done, without changing anything
  -h, --help            Show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --domain|--api-domain) DOMAIN="${2:-}"; shift 2 ;;
    --panel-domain)        PANEL_DOMAIN="${2:-}"; shift 2 ;;
    --email)               EMAIL="${2:-}"; shift 2 ;;
    --behind-proxy)        MODE="proxy"; shift ;;
    --port)                PORT="${2:-}"; shift 2 ;;
    --lang)                LANGUAGE="${2:-}"; shift 2 ;;
    --version)             VERSION="${2:-}"; shift 2 ;;
    --dir)                 DIR="${2:-}"; shift 2 ;;
    --branch)              BRANCH="${2:-}"; shift 2 ;;
    --update)              UPDATE=1; shift ;;
    --yes|-y)              ASSUME_YES=1; shift ;;
    --dry-run)             DRY_RUN=1; shift ;;
    -h|--help)             usage; exit 0 ;;
    *)                     usage; die "unknown option: $1" ;;
  esac
done

# Questions work even with "curl … | sudo bash": read from the terminal, not from the pipe.
ask() { # ask VAR "question" "default"
  local __var="$1" __q="$2" __def="${3:-}" __ans=""
  # A readable /dev/tty is not enough: inside containers and cron it exists but cannot be opened.
  { : </dev/tty; } 2>/dev/null || die "no terminal to ask \"$__q\": pass it as an option (see --help)"
  if [ -n "$__def" ]; then printf '%s [%s]: ' "$__q" "$__def" >/dev/tty; else printf '%s: ' "$__q" >/dev/tty; fi
  IFS= read -r __ans </dev/tty || true
  printf -v "$__var" '%s' "${__ans:-$__def}"
}
yes_no() { case "$1" in y|Y|yes|YES|s|S|sim|SIM) return 0 ;; *) return 1 ;; esac; }

valid_domain() { [[ "$1" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]]; }
valid_email()  { [[ "$1" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; }
clean_domain() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's#^https?://##; s#[/:].*$##'; }
port_busy()    { command -v ss >/dev/null && ss -ltnH "sport = :$1" 2>/dev/null | grep -q .; }

run() { # run a command, or only print it in --dry-run
  if [ "$DRY_RUN" = 1 ]; then printf '    [dry-run] %s\n' "$*"; else "$@"; fi
}
fetch() { # fetch <path in the repository> <destination>
  run curl -fsSL --retry 3 -o "$2" "$RAW/$BRANCH/$1"
}
compose() { (cd "$DIR" && docker compose "$@"); }

# ── Checks ───────────────────────────────────────────────────────────────────
[ "$(uname -s)" = "Linux" ] || die "this installer runs on Linux"
if [ "$DRY_RUN" = 0 ] && [ "$(id -u)" -ne 0 ]; then die "run as root: sudo bash install.sh"; fi
case "$DIR" in /*) ;; *) die "--dir must be an absolute path" ;; esac
[[ "$PORT" =~ ^[0-9]{2,5}$ ]] && [ "$PORT" -ge 1024 ] && [ "$PORT" -le 65535 ] || die "--port must be between 1024 and 65535"
[[ "$VERSION" =~ ^[A-Za-z0-9._-]{1,64}$ ]] || die "invalid --version"
[[ "$BRANCH" =~ ^[A-Za-z0-9._/-]{1,100}$ ]] || die "invalid --branch"
case "$LANGUAGE" in ""|en|pt-BR) ;; *) die "--lang must be en or pt-BR" ;; esac
case "$(uname -m)" in x86_64|amd64|aarch64|arm64) ;; *) die "unsupported CPU architecture: $(uname -m) (images exist for amd64 and arm64)" ;; esac

need_curl() {
  command -v curl >/dev/null && return
  info "Installing curl"
  if command -v apt-get >/dev/null; then run apt-get update -qq; run apt-get install -y -qq curl ca-certificates
  elif command -v dnf >/dev/null; then run dnf install -y -q curl
  elif command -v yum >/dev/null; then run yum install -y -q curl
  elif command -v apk >/dev/null; then run apk add --no-cache curl
  elif command -v zypper >/dev/null; then run zypper -n install curl
  else die "install curl and run again"; fi
}

need_docker() {
  if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then
    ok "Docker already installed ($(docker --version | cut -d, -f1))"
    return
  fi
  if command -v docker >/dev/null; then
    die "Docker is installed without the Compose plugin: install docker-compose-plugin (or update Docker) and run again"
  fi
  info "Installing Docker with the official script (get.docker.com)"
  if [ "$DRY_RUN" = 1 ]; then
    printf '    [dry-run] curl -fsSL https://get.docker.com | sh\n'
  else
    curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
    sh /tmp/get-docker.sh
    rm -f /tmp/get-docker.sh
    systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
  fi
  ok "Docker installed"
}

# ── Update ───────────────────────────────────────────────────────────────────
if [ "$UPDATE" = 1 ]; then
  [ -f "$DIR/.env" ] && [ -f "$DIR/docker-compose.yml" ] || die "no installation found in $DIR"
  info "Updating FireZAP API in $DIR"
  fetch deploy/docker-compose.yml "$DIR/docker-compose.yml"
  fetch install.sh "$DIR/install.sh"
  if [ "$DRY_RUN" = 0 ]; then
    compose pull --quiet
    compose up -d --remove-orphans
    docker image prune -f >/dev/null 2>&1 || true
  fi
  ok "updated — $(curl -fsS --max-time 5 "http://127.0.0.1:$(grep -E '^FZ_LOCAL_PORT=' "$DIR/.env" | cut -d= -f2 || echo 8080)/" 2>/dev/null || echo 'running')"
  exit 0
fi

# ── Questions ────────────────────────────────────────────────────────────────
printf '\n%s\n\n' "${c_b}FireZAP API — installation${c_0}"
[ -n "$DOMAIN" ] || ask DOMAIN "Address of the API (e.g. api.example.com)"
DOMAIN="$(clean_domain "$DOMAIN")"
valid_domain "$DOMAIN" || die "invalid address: \"$DOMAIN\" (use a name like api.example.com)"
if [ -n "$PANEL_DOMAIN" ]; then
  PANEL_DOMAIN="$(clean_domain "$PANEL_DOMAIN")"
  valid_domain "$PANEL_DOMAIN" || die "invalid dashboard address: \"$PANEL_DOMAIN\""
fi
[ -n "$LANGUAGE" ] || LANGUAGE="en"

# Ports 80/443 taken by another web server: offer to install behind it.
web_busy=0
for p in 80 443; do
  if port_busy "$p" && ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^firezap-api-caddy'; then web_busy=1; fi
done
if [ -z "$MODE" ]; then
  if [ "$web_busy" = 1 ]; then
    warn "ports 80/443 are already used by a web server on this machine (nginx, Apache, aaPanel, cPanel…)"
    if [ "$ASSUME_YES" = 1 ]; then die "use --behind-proxy to install behind it"; fi
    ask ANSWER "Install behind it (it will proxy to 127.0.0.1:$PORT)? (Y/n)" "Y"
    yes_no "$ANSWER" || die "cancelled: free ports 80/443 or use --behind-proxy"
    MODE="proxy"
  else
    MODE="caddy"
  fi
fi
if [ "$MODE" = "caddy" ] && [ "$web_busy" = 1 ]; then die "ports 80/443 are in use: use --behind-proxy"; fi
if [ "$MODE" = "caddy" ] && [ -z "$EMAIL" ] && [ "$ASSUME_YES" = 0 ]; then
  ask EMAIL "E-mail for Let's Encrypt notices (optional, Enter to skip)"
fi
[ -z "$EMAIL" ] || valid_email "$EMAIL" || die "invalid e-mail: \"$EMAIL\""
if port_busy "$PORT" && ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^firezap-api-api'; then
  die "port $PORT is already in use on this machine: choose another with --port"
fi

# DNS: the names must point to this server, or HTTPS cannot work.
local_ips="$(hostname -I 2>/dev/null || true)"
public_ip="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
for d in $(printf '%s\n%s\n' "$DOMAIN" "$PANEL_DOMAIN" | grep . | sort -u); do
  # getent fails for a name that does not resolve yet: that is a warning, not a reason to stop.
  resolved="$(getent ahostsv4 "$d" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)"
  if [ -z "$resolved" ]; then
    warn "$d does not resolve yet: create an A record pointing to this server (${public_ip:-its public IP})"
    continue
  fi
  match=0
  for ip in $resolved; do case " $local_ips $public_ip " in *" $ip "*) match=1 ;; esac; done
  if [ "$match" = 1 ]; then ok "$d points to this server (${resolved% })"
  else warn "$d points to ${resolved% }, but this server is ${public_ip:-${local_ips% }} — fix the DNS (or check your proxy/CDN)"; fi
done

if [ -n "$PANEL_DOMAIN" ]; then PANEL_SHOWN="https://$PANEL_DOMAIN"; else PANEL_SHOWN="https://$DOMAIN/dashboard/"; fi
printf '\n  API:        https://%s\n  Dashboard:  %s\n  HTTPS:      %s\n  Folder:     %s\n\n' "$DOMAIN" "$PANEL_SHOWN" \
  "$([ "$MODE" = caddy ] && echo "Caddy (automatic certificates)" || echo "your web server → 127.0.0.1:$PORT")" "$DIR"
if [ "$ASSUME_YES" = 0 ] && [ "$DRY_RUN" = 0 ]; then
  ask CONFIRM "Install now? (y/N)" "N"
  yes_no "$CONFIRM" || die "cancelled"
fi

# ── Install ──────────────────────────────────────────────────────────────────
need_curl
need_docker

if [ -e "$DIR" ] && [ ! -f "$DIR/.env" ] && [ -n "$(ls -A "$DIR" 2>/dev/null)" ]; then
  die "$DIR exists and is not a FireZAP API installation"
fi
run mkdir -p "$DIR"
info "Downloading the configuration"
fetch deploy/docker-compose.yml "$DIR/docker-compose.yml"
fetch install.sh "$DIR/install.sh"

ENV_FILE="$DIR/.env"
NEW_KEY=""
set_env() { # set_env KEY VALUE — replace or append one line of .env
  if grep -q "^$1=" "$ENV_FILE" 2>/dev/null; then sed -i "s#^$1=.*#$1=$2#" "$ENV_FILE"; else printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"; fi
}
if [ "$DRY_RUN" = 1 ]; then
  printf '    [dry-run] write %s (admin key and database password generated)\n' "$ENV_FILE"
  if [ "$MODE" = caddy ]; then
    printf '    [dry-run] write %s/Caddyfile for %s\n' "$DIR" "$(printf '%s %s' "$DOMAIN" "$PANEL_DOMAIN" | xargs)"
  fi
else
  umask 077
  if [ ! -f "$ENV_FILE" ]; then
    NEW_KEY="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    {
      echo "# FireZAP API — generated by install.sh. Keep this file private."
      echo "FZ_ADMIN_KEY=$NEW_KEY"
      echo "POSTGRES_PASSWORD=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    } >"$ENV_FILE"
    ok "keys generated in $ENV_FILE (readable only by root)"
  else
    ok "keeping the keys in $ENV_FILE"
  fi
  set_env PUBLIC_URL "https://$DOMAIN"
  # Only a separate dashboard address: on the API address "/" keeps answering the API.
  if [ -n "$PANEL_DOMAIN" ]; then set_env PANEL_URL "https://$PANEL_DOMAIN"; else set_env PANEL_URL ""; fi
  set_env DEFAULT_LANGUAGE "$LANGUAGE"
  set_env FZ_VERSION "$VERSION"
  set_env FZ_LOCAL_PORT "$PORT"
  set_env COMPOSE_PROFILES "$([ "$MODE" = caddy ] && echo caddy || true)"
  chmod 600 "$ENV_FILE"
  umask 022
  if [ "$MODE" = caddy ]; then
    {
      echo "# Generated by install.sh — run it again to change the addresses."
      if [ -n "$EMAIL" ]; then printf '{\n\temail %s\n}\n\n' "$EMAIL"; fi
      printf '%s {\n\tencode gzip\n\treverse_proxy api:8080\n}\n' "$(printf '%s %s' "$DOMAIN" "$PANEL_DOMAIN" | xargs | tr ' ' ',')"
    } >"$DIR/Caddyfile"
  else
    rm -f "$DIR/Caddyfile"
  fi
fi

# Firewall: only opens the ports when ufw or firewalld is already active (never turns one on:
# that could lock you out of SSH on a custom port).
if [ "$MODE" = caddy ]; then
  if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
    run ufw allow 80/tcp >/dev/null; run ufw allow 443 >/dev/null; ok "ufw: ports 80 and 443 opened"
  elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    run firewall-cmd -q --permanent --add-service=http --add-service=https; run firewall-cmd -q --reload
    ok "firewalld: http and https opened"
  fi
fi

if [ "$DRY_RUN" = 1 ]; then
  printf '    [dry-run] cd %s && docker compose pull && docker compose up -d\n' "$DIR"
  ok "dry run finished: nothing was changed"
  exit 0
fi

info "Downloading the image and starting"
compose pull --quiet
compose up -d --remove-orphans

info "Waiting for the API"
healthy=0
for _ in $(seq 1 60); do
  if curl -fsS --max-time 3 "http://127.0.0.1:$PORT/health" 2>/dev/null | grep -q '"ok"'; then healthy=1; break; fi
  sleep 2
done
if [ "$healthy" = 1 ]; then ok "API running on 127.0.0.1:$PORT"; else warn "the API did not answer yet: cd $DIR && docker compose logs api"; fi
if [ "$MODE" = caddy ]; then
  if curl -fsS --max-time 20 "https://$DOMAIN/health" >/dev/null 2>&1; then ok "HTTPS working at https://$DOMAIN"
  else warn "HTTPS not answering yet: Caddy keeps trying once the DNS points here (cd $DIR && docker compose logs caddy)"; fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────
printf '\n%s\n\n  API:        https://%s\n  Dashboard:  %s\n' "${c_ok}${c_b}FireZAP API installed.${c_0}" "$DOMAIN" "$PANEL_SHOWN"
if [ -n "$NEW_KEY" ]; then
  printf '\n  %s (sign in to the dashboard with it — also in %s):\n  %s\n' "${c_b}Admin key${c_0}" "$ENV_FILE" "$NEW_KEY"
else
  printf '  Admin key:  unchanged (FZ_ADMIN_KEY in %s)\n' "$ENV_FILE"
fi
if [ "$MODE" = proxy ]; then
  cat <<EOF

  ${c_b}Add to your web server${c_0} (site $DOMAIN${PANEL_DOMAIN:+ and $PANEL_DOMAIN}, with its HTTPS certificate). nginx:

    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        client_max_body_size 32m;     # media up to MEDIA_MAX_MB, sent as base64
        proxy_read_timeout 180s;
    }

  Apache: ProxyPass / http://127.0.0.1:$PORT/  and  ProxyPassReverse / http://127.0.0.1:$PORT/
EOF
fi
cat <<EOF

  Update:  sudo bash $DIR/install.sh --update
  Logs:    cd $DIR && docker compose logs -f api
  Stop:    cd $DIR && docker compose down        (data is kept; "down -v" erases it)

EOF
