#!/usr/bin/env bash
# FireZAP API installer — Ubuntu 24.04 (22.04 also works).
#
# Installs Docker (when missing), downloads FireZAP API to /opt/firezap-api and starts
# PostgreSQL, the API and Caddy, which gets HTTPS certificates from Let's Encrypt by itself.
#
#   curl -fsSL https://raw.githubusercontent.com/firezaprepo/firezap-api/main/install.sh -o install.sh
#   sudo bash install.sh
#
# Without questions:
#   sudo bash install.sh --api-domain api.example.com --panel-domain panel.example.com --email you@example.com
#
# Update to the latest version (keeps your keys and data):
#   sudo bash /opt/firezap-api/install.sh --update
#
# Before installing, point the DNS of both addresses (A record) to this server and make sure
# ports 80 and 443 are free and open.
set -euo pipefail

REPO_URL="https://github.com/firezaprepo/firezap-api.git"
DIR="/opt/firezap-api"
BRANCH="main"
API_DOMAIN=""
PANEL_DOMAIN=""
EMAIL=""
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
  --api-domain NAME     Address of the API, e.g. api.example.com
  --panel-domain NAME   Address of the dashboard, e.g. panel.example.com (may be the same)
  --email ADDRESS       E-mail for Let's Encrypt notices (optional, recommended)
  --dir PATH            Install directory (default /opt/firezap-api)
  --branch NAME         Git branch or tag to install (default main)
  --update              Update an existing installation (keeps .env and data)
  --yes                 Do not ask for confirmation
  --dry-run             Check the input and show what would be done, without changing anything
  -h, --help            Show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --api-domain)   API_DOMAIN="${2:-}"; shift 2 ;;
    --panel-domain) PANEL_DOMAIN="${2:-}"; shift 2 ;;
    --email)        EMAIL="${2:-}"; shift 2 ;;
    --dir)          DIR="${2:-}"; shift 2 ;;
    --branch)       BRANCH="${2:-}"; shift 2 ;;
    --update)       UPDATE=1; shift ;;
    --yes|-y)       ASSUME_YES=1; shift ;;
    --dry-run)      DRY_RUN=1; shift ;;
    -h|--help)      usage; exit 0 ;;
    *)              usage; die "unknown option: $1" ;;
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

valid_domain() { [[ "$1" =~ ^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$ ]]; }
valid_email()  { [[ "$1" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; }
valid_branch() { [[ "$1" =~ ^[A-Za-z0-9._/-]{1,100}$ ]]; }

run() { # run a command, or only print it in --dry-run
  if [ "$DRY_RUN" = 1 ]; then printf '    [dry-run] %s\n' "$*"; else "$@"; fi
}

compose() { docker compose -f "$DIR/deploy/docker-compose.yml" --env-file "$DIR/.env" "$@"; }

# ── Checks ───────────────────────────────────────────────────────────────────
if [ "$DRY_RUN" = 0 ] && [ "$(id -u)" -ne 0 ]; then die "run as root: sudo bash install.sh"; fi
valid_branch "$BRANCH" || die "invalid --branch"
case "$DIR" in /*) ;; *) die "--dir must be an absolute path" ;; esac

if [ -r /etc/os-release ]; then . /etc/os-release; else ID=unknown; VERSION_ID=""; fi
if [ "${ID:-}" != "ubuntu" ]; then
  warn "this installer is made for Ubuntu (found: ${PRETTY_NAME:-unknown}); continuing anyway"
elif [ "${VERSION_ID:-}" != "24.04" ] && [ "${VERSION_ID:-}" != "22.04" ]; then
  warn "tested on Ubuntu 24.04 and 22.04 (found: $VERSION_ID); continuing anyway"
fi
case "$(uname -m)" in x86_64|amd64|aarch64|arm64) ;; *) die "unsupported CPU architecture: $(uname -m)" ;; esac

# ── Update ───────────────────────────────────────────────────────────────────
if [ "$UPDATE" = 1 ]; then
  [ -f "$DIR/.env" ] && [ -d "$DIR/.git" ] || die "no installation found in $DIR"
  info "Updating FireZAP API in $DIR"
  run git -C "$DIR" fetch --depth 1 origin "$BRANCH"
  run git -C "$DIR" reset --hard FETCH_HEAD
  if [ "$DRY_RUN" = 0 ]; then compose up -d --build --remove-orphans; fi
  ok "updated to $(git -C "$DIR" log -1 --format='%h %s' 2>/dev/null || echo "$BRANCH")"
  exit 0
fi

# ── Questions ────────────────────────────────────────────────────────────────
printf '\n%s\n\n' "${c_b}FireZAP API — installation${c_0}"
[ -n "$API_DOMAIN" ] || ask API_DOMAIN "Address of the API (e.g. api.example.com)"
API_DOMAIN="$(printf '%s' "$API_DOMAIN" | tr 'A-Z' 'a-z' | sed -E 's#^https?://##; s#/.*$##')"
valid_domain "$API_DOMAIN" || die "invalid API address: \"$API_DOMAIN\" (use a name like api.example.com)"
[ -n "$PANEL_DOMAIN" ] || ask PANEL_DOMAIN "Address of the dashboard (e.g. panel.example.com)" "$API_DOMAIN"
PANEL_DOMAIN="$(printf '%s' "$PANEL_DOMAIN" | tr 'A-Z' 'a-z' | sed -E 's#^https?://##; s#/.*$##')"
valid_domain "$PANEL_DOMAIN" || die "invalid dashboard address: \"$PANEL_DOMAIN\""
if [ -z "$EMAIL" ] && [ "$ASSUME_YES" = 0 ]; then
  ask EMAIL "E-mail for Let's Encrypt notices (optional, Enter to skip)"
fi
[ -z "$EMAIL" ] || valid_email "$EMAIL" || die "invalid e-mail: \"$EMAIL\""

# DNS: both names must point to this server, or Let's Encrypt cannot issue the certificates.
local_ips="$(hostname -I 2>/dev/null || true)"
public_ip="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
for d in $(printf '%s\n%s\n' "$API_DOMAIN" "$PANEL_DOMAIN" | sort -u); do
  # getent fails for a name that does not resolve yet: that is a warning, not a reason to stop.
  resolved="$(getent ahostsv4 "$d" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)"
  if [ -z "$resolved" ]; then
    warn "$d does not resolve yet: create an A record pointing to this server (${public_ip:-its public IP})"
  else
    match=0
    for ip in $resolved; do
      case " $local_ips $public_ip " in *" $ip "*) match=1 ;; esac
    done
    if [ "$match" = 1 ]; then ok "$d points to this server ($resolved)"
    else warn "$d points to ${resolved% }, but this server is ${public_ip:-${local_ips% }} — HTTPS will fail until the DNS is fixed"; fi
  fi
done

printf '\n  API:       https://%s\n  Dashboard: https://%s\n  Folder:    %s\n\n' "$API_DOMAIN" "$PANEL_DOMAIN" "$DIR"
if [ "$ASSUME_YES" = 0 ] && [ "$DRY_RUN" = 0 ]; then
  ask CONFIRM "Install now? (y/N)" "N"
  case "$CONFIRM" in y|Y|yes|YES|s|S|sim|SIM) ;; *) die "cancelled" ;; esac
fi

# ── Docker ───────────────────────────────────────────────────────────────────
for p in 80 443; do
  if command -v ss >/dev/null && ss -ltnH "sport = :$p" 2>/dev/null | grep -q .; then
    if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^firezap-api-caddy'; then
      die "port $p is already in use by another program (web server?). Free it or use a server without one."
    fi
  fi
done

if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then
  ok "Docker already installed ($(docker --version | cut -d, -f1))"
else
  info "Installing Docker (official repository)"
  run apt-get update -qq
  run apt-get install -y -qq ca-certificates curl git gnupg
  run install -m 0755 -d /etc/apt/keyrings
  run curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  run chmod a+r /etc/apt/keyrings/docker.asc
  if [ "$DRY_RUN" = 0 ]; then
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${UBUNTU_CODENAME:-$VERSION_CODENAME} stable" \
      > /etc/apt/sources.list.d/docker.list
  fi
  run apt-get update -qq
  run apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  run systemctl enable --now docker
  ok "Docker installed"
fi
command -v git >/dev/null || run apt-get install -y -qq git

# ── Code ─────────────────────────────────────────────────────────────────────
if [ -d "$DIR/.git" ]; then
  info "Existing installation in $DIR: updating the code (keys and data are kept)"
  run git -C "$DIR" fetch --depth 1 origin "$BRANCH"
  run git -C "$DIR" reset --hard FETCH_HEAD
else
  [ ! -e "$DIR" ] || [ -z "$(ls -A "$DIR" 2>/dev/null)" ] || die "$DIR exists and is not a FireZAP API installation"
  info "Downloading FireZAP API to $DIR"
  run git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$DIR"
fi

# ── Configuration ────────────────────────────────────────────────────────────
ENV_FILE="$DIR/.env"
NEW_KEY=""
set_env() { # set_env KEY VALUE — replace or append one line of .env
  if grep -q "^$1=" "$ENV_FILE" 2>/dev/null; then
    sed -i "s#^$1=.*#$1=$2#" "$ENV_FILE"
  else
    printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"
  fi
}
if [ "$DRY_RUN" = 1 ]; then
  printf '    [dry-run] write %s (FZ_ADMIN_KEY and POSTGRES_PASSWORD generated, PUBLIC_URL=https://%s)\n' "$ENV_FILE" "$API_DOMAIN"
  printf '    [dry-run] write %s/data/Caddyfile for %s\n' "$DIR" "$(printf '%s %s' "$API_DOMAIN" "$PANEL_DOMAIN" | tr ' ' '\n' | sort -u | tr '\n' ' ')"
else
  umask 077
  if [ ! -f "$ENV_FILE" ]; then
    cp "$DIR/.env.example" "$ENV_FILE"
    NEW_KEY="$(openssl rand -hex 24)"
    set_env FZ_ADMIN_KEY "$NEW_KEY"
    set_env POSTGRES_PASSWORD "$(openssl rand -hex 24)"
    ok "keys generated in $ENV_FILE (readable only by root)"
  else
    ok "keeping the keys in $ENV_FILE"
  fi
  set_env PUBLIC_URL "https://$API_DOMAIN"
  set_env PANEL_URL "https://$PANEL_DOMAIN"
  chmod 600 "$ENV_FILE"

  mkdir -p "$DIR/data"
  {
    echo "# Generated by install.sh — run it again to change the addresses."
    if [ -n "$EMAIL" ]; then printf '{\n\temail %s\n}\n\n' "$EMAIL"; fi
    if [ "$API_DOMAIN" = "$PANEL_DOMAIN" ]; then
      printf '%s {\n\tencode gzip\n\treverse_proxy api:8080\n}\n' "$API_DOMAIN"
    else
      printf '%s {\n\tencode gzip\n\treverse_proxy api:8080\n}\n\n' "$API_DOMAIN"
      printf '%s {\n\tencode gzip\n\treverse_proxy api:8080\n}\n' "$PANEL_DOMAIN"
    fi
  } >"$DIR/data/Caddyfile"
  chmod 644 "$DIR/data/Caddyfile"
  umask 022
fi

# Firewall: only opens the ports when ufw is already active (never turns it on: that could
# lock you out of SSH on a custom port).
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  run ufw allow 80/tcp >/dev/null
  run ufw allow 443 >/dev/null
  ok "firewall (ufw): ports 80 and 443 opened"
fi

# ── Start ────────────────────────────────────────────────────────────────────
if [ "$DRY_RUN" = 1 ]; then
  printf '    [dry-run] docker compose -f %s/deploy/docker-compose.yml --env-file %s up -d --build\n' "$DIR" "$ENV_FILE"
  ok "dry run finished: nothing was changed"
  exit 0
fi
info "Building and starting (the first build takes a few minutes)"
compose up -d --build --remove-orphans

info "Waiting for the API"
healthy=0
for _ in $(seq 1 60); do
  if compose exec -T caddy wget -qO- http://api:8080/health 2>/dev/null | grep -q '"ok"'; then healthy=1; break; fi
  sleep 2
done
[ "$healthy" = 1 ] && ok "API running" || warn "the API did not answer yet: check with  docker compose -f $DIR/deploy/docker-compose.yml logs api"

if curl -fsS --max-time 20 "https://$API_DOMAIN/health" >/dev/null 2>&1; then
  ok "HTTPS working at https://$API_DOMAIN"
else
  warn "HTTPS not answering yet: Caddy keeps trying to get the certificate once the DNS points here (logs: docker logs firezap-api-caddy-1)"
fi

# ── Summary ──────────────────────────────────────────────────────────────────
cat <<EOF

${c_ok}${c_b}FireZAP API installed.${c_0}

  API:        https://$API_DOMAIN
  Dashboard:  https://$PANEL_DOMAIN   (the web dashboard arrives in v0.4; until then this address answers like the API)
EOF
if [ -n "$NEW_KEY" ]; then
  cat <<EOF

  ${c_b}Admin key${c_0} (save it now — also stored in $ENV_FILE):
  $NEW_KEY
EOF
else
  echo "  Admin key:  unchanged (FZ_ADMIN_KEY in $ENV_FILE)"
fi
cat <<EOF

  Create your first instance:
    curl -X POST https://$API_DOMAIN/instances -H "Authorization: Bearer <admin key>" \\
      -H "Content-Type: application/json" -d '{"id": "my-number"}'

  Update:  sudo bash $DIR/install.sh --update
  Logs:    docker compose -f $DIR/deploy/docker-compose.yml --env-file $ENV_FILE logs -f api
  Stop:    docker compose -f $DIR/deploy/docker-compose.yml --env-file $ENV_FILE down

EOF
