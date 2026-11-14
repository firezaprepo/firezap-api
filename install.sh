#!/usr/bin/env bash
# FireZAP API installer — sets up everything on a server and leaves it ready in the browser:
# Docker, PostgreSQL, the API with its web dashboard, and HTTPS.
#
# Supported systems: Ubuntu 22.04, 24.04 and 26.04 LTS; Debian 12 and 13 (amd64 or arm64).
#
#   curl -fsSL https://raw.githubusercontent.com/firezaprepo/firezap-api/main/install.sh -o install.sh
#   sudo bash install.sh
#
# Without questions:
#   sudo bash install.sh --domain api.example.com --email you@example.com --yes
#
# Update to the latest version (keeps keys and data):  sudo bash /opt/firezap-api/install.sh --update
#
# HTTPS: with a domain (one A record pointing to the server) Caddy gets a Let's Encrypt
# certificate by itself. Without a domain the server's IP is used with a self-signed
# certificate (the browser warns once). On a server that already runs a web server on ports
# 80/443, --behind-proxy keeps the API on 127.0.0.1 and prints the proxy settings to add.
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
NO_DOMAIN=0

# ── Language of the messages: --lang, else the system's ─────────────────────
for a in "$@"; do case "$a" in pt-BR|pt) LANGUAGE="pt-BR" ;; en) LANGUAGE="en" ;; esac; done
if [ -z "$LANGUAGE" ]; then case "${LC_ALL:-${LANG:-}}" in pt*) LANGUAGE="pt-BR" ;; *) LANGUAGE="en" ;; esac; fi
m() { if [ "$LANGUAGE" = "pt-BR" ]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }

c_ok=$'\033[32m'; c_warn=$'\033[33m'; c_err=$'\033[31m'; c_b=$'\033[1m'; c_0=$'\033[0m'
info() { printf '%s\n' "${c_b}==>${c_0} $*"; }
ok()   { printf '%s\n' "${c_ok}  ✓${c_0} $*"; }
warn() { printf '%s\n' "${c_warn}  !${c_0} $*"; }
die()  { printf '%s\n' "${c_err}  ✗ $*${c_0}" >&2; exit 1; }

usage() {
  m "FireZAP API installer

Options:
  --domain NAME         Address of the API and dashboard, e.g. api.example.com
  --no-domain           Use the server's IP (self-signed certificate: the browser warns once)
  --panel-domain NAME   Optional second address whose root opens the dashboard
  --email ADDRESS       E-mail for Let's Encrypt notices (optional)
  --lang en|pt-BR       Language of the installer and the dashboard
  --behind-proxy        Use the web server already on this machine instead of Caddy
  --port N              Local port of the API on 127.0.0.1 (default 8080)
  --version TAG         Image tag to run (default latest)
  --dir PATH            Install directory (default /opt/firezap-api)
  --update              Update an existing installation to the latest version
  --yes                 Do not ask for confirmation
  --dry-run             Check everything and show what would be done, without changing anything
  -h, --help            Show this help
" "Instalador da FireZAP API

Opções:
  --domain NOME         Endereço da API e do painel, ex.: api.exemplo.com.br
  --no-domain           Usar o IP do servidor (certificado próprio: o navegador avisa uma vez)
  --panel-domain NOME   Segundo endereço opcional, cuja raiz abre o painel
  --email ENDEREÇO      E-mail para avisos do Let's Encrypt (opcional)
  --lang en|pt-BR       Idioma do instalador e do painel
  --behind-proxy        Usar o servidor web que já existe na máquina em vez do Caddy
  --port N              Porta local da API em 127.0.0.1 (padrão 8080)
  --version TAG         Versão da imagem (padrão latest)
  --dir CAMINHO         Pasta da instalação (padrão /opt/firezap-api)
  --update              Atualizar uma instalação existente para a última versão
  --yes                 Não pedir confirmação
  --dry-run             Conferir tudo e mostrar o que seria feito, sem mudar nada
  -h, --help            Mostrar esta ajuda
"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --domain|--api-domain) DOMAIN="${2:-}"; shift 2 ;;
    --no-domain)           NO_DOMAIN=1; shift ;;
    --panel-domain)        PANEL_DOMAIN="${2:-}"; shift 2 ;;
    --email)               EMAIL="${2:-}"; shift 2 ;;
    --lang)                LANGUAGE="${2:-}"; shift 2 ;;
    --behind-proxy)        MODE="proxy"; shift ;;
    --port)                PORT="${2:-}"; shift 2 ;;
    --version)             VERSION="${2:-}"; shift 2 ;;
    --dir)                 DIR="${2:-}"; shift 2 ;;
    --branch)              BRANCH="${2:-}"; shift 2 ;;
    --update)              UPDATE=1; shift ;;
    --yes|-y)              ASSUME_YES=1; shift ;;
    --dry-run)             DRY_RUN=1; shift ;;
    -h|--help)             usage; exit 0 ;;
    *)                     usage; die "$(m "unknown option: $1" "opção desconhecida: $1")" ;;
  esac
done
case "$LANGUAGE" in pt|pt-br|pt-BR) LANGUAGE="pt-BR" ;; en) ;; *) die "--lang: en | pt-BR" ;; esac

# Questions work even with "curl … | sudo bash": read from the terminal, not from the pipe.
ask() { # ask VAR "question" "default"
  local __var="$1" __q="$2" __def="${3:-}" __ans=""
  # A readable /dev/tty is not enough: inside containers and cron it exists but cannot be opened.
  { : </dev/tty; } 2>/dev/null || die "$(m "no terminal to ask \"$__q\": pass it as an option (see --help)" "sem terminal para perguntar \"$__q\": informe como opção (veja --help)")"
  if [ -n "$__def" ]; then printf '%s [%s]: ' "$__q" "$__def" >/dev/tty; else printf '%s: ' "$__q" >/dev/tty; fi
  IFS= read -r __ans </dev/tty || true
  printf -v "$__var" '%s' "${__ans:-$__def}"
}
yes_no() { case "$1" in y|Y|yes|YES|s|S|sim|SIM) return 0 ;; *) return 1 ;; esac; }

valid_domain() { [[ "$1" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]]; }
valid_ipv4()   { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; }
valid_email()  { [[ "$1" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; }
clean_domain() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's#^https?://##; s#[/:].*$##'; }
port_busy()    { command -v ss >/dev/null && ss -ltnH "sport = :$1" 2>/dev/null | grep -q .; }
ours()         { docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^firezap-api-$1"; }

run() { # run a command, or only print it in --dry-run
  if [ "$DRY_RUN" = 1 ]; then printf '    [dry-run] %s\n' "$*"; else "$@"; fi
}
fetch() { run curl -fsSL --retry 3 -o "$2" "$RAW/$BRANCH/$1"; }
compose() { (cd "$DIR" && docker compose "$@"); }
random_hex() { head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# ── System ───────────────────────────────────────────────────────────────────
[ "$(uname -s)" = "Linux" ] || die "Linux only"
if [ "$DRY_RUN" = 0 ] && [ "$(id -u)" -ne 0 ]; then die "$(m "run as root: sudo bash install.sh" "rode como root: sudo bash install.sh")"; fi
case "$DIR" in /*) ;; *) die "--dir: /absolute/path" ;; esac
[[ "$PORT" =~ ^[0-9]{4,5}$ ]] && [ "$PORT" -ge 1024 ] && [ "$PORT" -le 65535 ] || die "$(m "--port must be between 1024 and 65535" "--port deve ficar entre 1024 e 65535")"
[[ "$VERSION" =~ ^[A-Za-z0-9._-]{1,64}$ ]] || die "--version?"
[[ "$BRANCH" =~ ^[A-Za-z0-9._/-]{1,100}$ ]] || die "--branch?"

OS_ID=""; OS_VERSION=""; OS_CODENAME=""; OS_NAME="?"
if [ -r /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-}"; OS_VERSION="${VERSION_ID:-}"; OS_CODENAME="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"; OS_NAME="${PRETTY_NAME:-?}"
fi
case "$OS_ID:$OS_VERSION" in
  ubuntu:22.04|ubuntu:24.04|ubuntu:26.04|debian:12|debian:13) ;;
  *) die "$(m "unsupported system: $OS_NAME. Supported: Ubuntu 22.04, 24.04 and 26.04 LTS; Debian 12 and 13." \
              "sistema não suportado: $OS_NAME. Suportados: Ubuntu 22.04, 24.04 e 26.04 LTS; Debian 12 e 13.")" ;;
esac
case "$(uname -m)" in x86_64|amd64|aarch64|arm64) ;; *) die "$(m "unsupported CPU: $(uname -m) (amd64 or arm64)" "processador não suportado: $(uname -m) (amd64 ou arm64)")" ;; esac

apt_install() {
  run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o Dpkg::Use-Pty=0 "$@" >/dev/null
}

install_base() {
  local missing=""
  for p in curl ca-certificates gnupg; do dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"; done
  command -v ss >/dev/null || missing="$missing iproute2"
  if [ -n "$missing" ]; then
    info "$(m "Installing base packages:" "Instalando pacotes básicos:")$missing"
    run apt-get update -qq
    # shellcheck disable=SC2086
    apt_install $missing
  fi
}

install_docker() {
  if command -v docker >/dev/null && docker compose version >/dev/null 2>&1; then
    ok "$(m "Docker already installed" "Docker já instalado") ($(docker --version | cut -d, -f1))"
    return
  fi
  info "$(m "Installing Docker (official repository)" "Instalando o Docker (repositório oficial)")"
  run install -m 0755 -d /etc/apt/keyrings
  run curl -fsSL "https://download.docker.com/linux/$OS_ID/gpg" -o /etc/apt/keyrings/docker.asc
  run chmod a+r /etc/apt/keyrings/docker.asc
  if [ "$DRY_RUN" = 1 ]; then
    printf '    [dry-run] write /etc/apt/sources.list.d/docker.list (%s %s)\n' "$OS_ID" "$OS_CODENAME"
  else
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$OS_ID $OS_CODENAME stable" \
      >/etc/apt/sources.list.d/docker.list
  fi
  run apt-get update -qq
  apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  run systemctl enable --now docker
  ok "$(m "Docker installed" "Docker instalado")"
}

# ── Update ───────────────────────────────────────────────────────────────────
if [ "$UPDATE" = 1 ]; then
  [ -f "$DIR/.env" ] && [ -f "$DIR/docker-compose.yml" ] || die "$(m "no installation found in $DIR" "nenhuma instalação encontrada em $DIR")"
  info "$(m "Updating FireZAP API" "Atualizando a FireZAP API")"
  fetch deploy/docker-compose.yml "$DIR/docker-compose.yml"
  fetch install.sh "$DIR/install.sh"
  if [ "$DRY_RUN" = 0 ]; then
    compose pull --quiet
    compose up -d --remove-orphans
    docker image prune -f >/dev/null 2>&1 || true
  fi
  ok "$(m "updated" "atualizada")"
  exit 0
fi

# ── Questions ────────────────────────────────────────────────────────────────
printf '\n%s\n%s\n\n' "${c_b}FireZAP API — $(m "installation" "instalação")${c_0}" "  $OS_NAME, $(uname -m)"
install_base # curl and ss are needed to find the public IP and check the ports

PUBLIC_IP="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
valid_ipv4 "$PUBLIC_IP" || PUBLIC_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
LOCAL_IPS="$(hostname -I 2>/dev/null || true)"

if [ -z "$DOMAIN" ] && [ "$NO_DOMAIN" = 0 ]; then
  ask DOMAIN "$(m "Address (domain) of the API, e.g. api.example.com — Enter to use only the IP ${PUBLIC_IP}" \
                  "Endereço (domínio) da API, ex.: api.exemplo.com.br — Enter para usar só o IP ${PUBLIC_IP}")"
fi
DOMAIN="$(clean_domain "$DOMAIN")"
if [ -z "$DOMAIN" ]; then
  NO_DOMAIN=1
  valid_ipv4 "$PUBLIC_IP" || die "$(m "could not find this server's IP: use --domain" "não encontrei o IP deste servidor: use --domain")"
  HOST="$PUBLIC_IP"
else
  valid_domain "$DOMAIN" || die "$(m "invalid address: \"$DOMAIN\" (use a name like api.example.com)" "endereço inválido: \"$DOMAIN\" (use um nome como api.exemplo.com.br)")"
  HOST="$DOMAIN"
fi
if [ -n "$PANEL_DOMAIN" ]; then
  PANEL_DOMAIN="$(clean_domain "$PANEL_DOMAIN")"
  valid_domain "$PANEL_DOMAIN" || die "$(m "invalid dashboard address" "endereço do painel inválido"): \"$PANEL_DOMAIN\""
  [ "$NO_DOMAIN" = 0 ] || die "$(m "--panel-domain needs --domain" "--panel-domain exige --domain")"
fi

# Ports 80/443 taken by another web server: offer to install behind it.
web_busy=0
for p in 80 443; do if port_busy "$p" && ! ours caddy; then web_busy=1; fi; done
if [ -z "$MODE" ]; then
  if [ "$web_busy" = 1 ]; then
    warn "$(m "ports 80/443 are already used by a web server on this machine (nginx, Apache, aaPanel, cPanel…)" \
              "as portas 80/443 já são usadas por um servidor web nesta máquina (nginx, Apache, aaPanel, cPanel…)")"
    [ "$ASSUME_YES" = 0 ] || die "$(m "use --behind-proxy to install behind it" "use --behind-proxy para instalar atrás dele")"
    ask ANSWER "$(m "Install behind it (you will add a proxy to 127.0.0.1:$PORT)? (Y/n)" "Instalar atrás dele (você vai acrescentar um proxy para 127.0.0.1:$PORT)? (S/n)")" "S"
    yes_no "$ANSWER" || die "$(m "cancelled: free ports 80/443 or use --behind-proxy" "cancelado: libere as portas 80/443 ou use --behind-proxy")"
    MODE="proxy"
  else
    MODE="caddy"
  fi
fi
[ "$MODE" = proxy ] || [ "$web_busy" = 0 ] || die "$(m "ports 80/443 are in use: use --behind-proxy" "as portas 80/443 estão em uso: use --behind-proxy")"
if [ "$MODE" = caddy ] && [ "$NO_DOMAIN" = 0 ] && [ -z "$EMAIL" ] && [ "$ASSUME_YES" = 0 ]; then
  ask EMAIL "$(m "E-mail for Let's Encrypt notices (optional, Enter to skip)" "E-mail para avisos do Let's Encrypt (opcional, Enter para pular)")"
fi
[ -z "$EMAIL" ] || valid_email "$EMAIL" || die "$(m "invalid e-mail" "e-mail inválido"): \"$EMAIL\""
if port_busy "$PORT" && ! ours api; then
  die "$(m "port $PORT is already in use on this machine: choose another with --port" "a porta $PORT já está em uso nesta máquina: escolha outra com --port")"
fi

# DNS: the names must point to this server, or Let's Encrypt cannot issue the certificate.
if [ "$NO_DOMAIN" = 0 ]; then
  for d in $(printf '%s\n%s\n' "$DOMAIN" "$PANEL_DOMAIN" | grep . | sort -u); do
    # getent fails for a name that does not resolve yet: that is a warning, not a reason to stop.
    resolved="$(getent ahostsv4 "$d" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' || true)"
    if [ -z "$resolved" ]; then
      warn "$(m "$d does not resolve yet: create an A record pointing to ${PUBLIC_IP:-this server}" "$d ainda não resolve: crie um registro A apontando para ${PUBLIC_IP:-este servidor}")"
      continue
    fi
    match=0
    for ip in $resolved; do case " $LOCAL_IPS $PUBLIC_IP " in *" $ip "*) match=1 ;; esac; done
    if [ "$match" = 1 ]; then ok "$(m "$d points to this server" "$d aponta para este servidor") (${resolved% })"
    else warn "$(m "$d points to ${resolved% }, but this server is ${PUBLIC_IP} — fix the DNS (or check your CDN/proxy)" "$d aponta para ${resolved% }, mas este servidor é ${PUBLIC_IP} — corrija o DNS (ou confira a CDN/proxy)")"; fi
  done
fi

if [ -n "$PANEL_DOMAIN" ]; then PANEL_LINK="https://$PANEL_DOMAIN/"; else PANEL_LINK="https://$HOST/dashboard/"; fi
printf '\n  %-11s https://%s\n  %-11s %s\n  %-11s %s\n  %-11s %s\n\n' \
  "API:" "$HOST" "$(m "Dashboard:" "Painel:")" "$PANEL_LINK" \
  "HTTPS:" "$(if [ "$MODE" = proxy ]; then m "your web server → 127.0.0.1:$PORT" "seu servidor web → 127.0.0.1:$PORT"; elif [ "$NO_DOMAIN" = 1 ]; then m "Caddy, self-signed certificate (browser warning)" "Caddy, certificado próprio (aviso no navegador)"; else m "Caddy + Let's Encrypt (automatic)" "Caddy + Let's Encrypt (automático)"; fi)" \
  "$(m "Folder:" "Pasta:")" "$DIR"
if [ "$ASSUME_YES" = 0 ] && [ "$DRY_RUN" = 0 ]; then
  ask CONFIRM "$(m "Install now? (y/N)" "Instalar agora? (s/N)")" "N"
  yes_no "$CONFIRM" || die "$(m "cancelled" "cancelado")"
fi

# ── Install ──────────────────────────────────────────────────────────────────
install_docker

if [ -e "$DIR" ] && [ ! -f "$DIR/.env" ] && [ -n "$(ls -A "$DIR" 2>/dev/null)" ]; then
  die "$(m "$DIR exists and is not a FireZAP API installation" "$DIR existe e não é uma instalação da FireZAP API")"
fi
run mkdir -p "$DIR"
info "$(m "Downloading the configuration" "Baixando a configuração")"
fetch deploy/docker-compose.yml "$DIR/docker-compose.yml"
fetch install.sh "$DIR/install.sh"

ENV_FILE="$DIR/.env"
NEW_KEY=""
set_env() { # set_env KEY VALUE — replace or append one line of .env
  if grep -q "^$1=" "$ENV_FILE" 2>/dev/null; then sed -i "s#^$1=.*#$1=$2#" "$ENV_FILE"; else printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"; fi
}
SITES="$HOST"
[ -z "$PANEL_DOMAIN" ] || SITES="$HOST, $PANEL_DOMAIN"
if [ "$DRY_RUN" = 1 ]; then
  printf '    [dry-run] write %s (%s)\n' "$ENV_FILE" "$(m "admin key and database password generated" "chave de administrador e senha do banco geradas")"
  if [ "$MODE" = caddy ]; then printf '    [dry-run] write %s/Caddyfile: %s%s\n' "$DIR" "$SITES" "$([ "$NO_DOMAIN" = 1 ] && echo " (tls internal)" || true)"; fi
else
  umask 077
  if [ ! -f "$ENV_FILE" ]; then
    NEW_KEY="$(random_hex)"
    {
      echo "# FireZAP API — generated by install.sh. Keep this file private."
      echo "FZ_ADMIN_KEY=$NEW_KEY"
      echo "POSTGRES_PASSWORD=$(random_hex)"
    } >"$ENV_FILE"
    ok "$(m "keys generated in $ENV_FILE (readable only by root)" "chaves geradas em $ENV_FILE (só o root lê)")"
  else
    ok "$(m "keeping the keys in $ENV_FILE" "mantendo as chaves de $ENV_FILE")"
  fi
  set_env PUBLIC_URL "https://$HOST"
  # Only a separate dashboard address: on the API address "/" keeps answering the API.
  if [ -n "$PANEL_DOMAIN" ]; then set_env PANEL_URL "https://$PANEL_DOMAIN"; else set_env PANEL_URL ""; fi
  set_env DEFAULT_LANGUAGE "$LANGUAGE"
  set_env FZ_VERSION "$VERSION"
  set_env FZ_LOCAL_PORT "$PORT"
  if [ "$MODE" = caddy ]; then set_env COMPOSE_PROFILES caddy; else set_env COMPOSE_PROFILES ""; fi
  chmod 600 "$ENV_FILE"
  umask 022
  if [ "$MODE" = caddy ]; then
    {
      echo "# Generated by install.sh — run it again to change the address."
      if [ -n "$EMAIL" ]; then printf '{\n\temail %s\n}\n\n' "$EMAIL"; fi
      if [ "$NO_DOMAIN" = 1 ]; then
        printf 'https://%s {\n\ttls internal\n\tencode gzip\n\treverse_proxy api:8080\n}\n' "$HOST"
      else
        printf '%s {\n\tencode gzip\n\treverse_proxy api:8080\n}\n' "$SITES"
      fi
    } >"$DIR/Caddyfile"
  else
    rm -f "$DIR/Caddyfile"
  fi
fi

# Firewall: only opens the ports when ufw is already active (never turns it on: that could lock
# you out of SSH on a custom port).
if [ "$MODE" = caddy ] && command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  run ufw allow 80/tcp >/dev/null
  run ufw allow 443 >/dev/null
  ok "$(m "firewall (ufw): ports 80 and 443 opened" "firewall (ufw): portas 80 e 443 liberadas")"
fi

if [ "$DRY_RUN" = 1 ]; then
  printf '    [dry-run] cd %s && docker compose pull && docker compose up -d\n' "$DIR"
  ok "$(m "dry run finished: nothing was changed" "simulação concluída: nada foi alterado")"
  exit 0
fi

info "$(m "Downloading the images and starting (1–3 minutes)" "Baixando as imagens e iniciando (1 a 3 minutos)")"
compose pull --quiet
compose up -d --remove-orphans

healthy=0
for _ in $(seq 1 60); do
  if curl -fsS --max-time 3 "http://127.0.0.1:$PORT/health" 2>/dev/null | grep -q '"ok"'; then healthy=1; break; fi
  sleep 2
done
[ "$healthy" = 1 ] || die "$(m "the API did not start: cd $DIR && docker compose logs api" "a API não subiu: cd $DIR && docker compose logs api")"
ok "$(m "API and dashboard running" "API e painel no ar")"

if [ "$MODE" = caddy ]; then
  info "$(m "Waiting for HTTPS" "Aguardando o HTTPS")"
  https_ok=0
  for _ in $(seq 1 45); do
    # Without a domain the certificate is self-signed (tls internal): do not verify it here.
    if [ "$NO_DOMAIN" = 1 ]; then insecure="-k"; else insecure=""; fi
    if curl -fsS --max-time 5 $insecure "https://$HOST/health" >/dev/null 2>&1; then https_ok=1; break; fi
    sleep 2
  done
  if [ "$https_ok" = 1 ]; then ok "$(m "HTTPS working" "HTTPS funcionando")"
  else warn "$(m "HTTPS is not answering yet. Caddy keeps trying by itself once the DNS points here and ports 80/443 are open in the provider's firewall (logs: cd $DIR && docker compose logs caddy)" \
                 "o HTTPS ainda não respondeu. O Caddy continua tentando sozinho assim que o DNS apontar para cá e as portas 80/443 estiverem abertas no firewall do provedor (logs: cd $DIR && docker compose logs caddy)")"; fi
fi

# ── Summary ──────────────────────────────────────────────────────────────────
printf '\n%s\n\n' "${c_ok}${c_b}$(m "FireZAP API is ready." "A FireZAP API está pronta.")${c_0}"
printf '  %s\n     %s\n\n' "$(m "Open in the browser:" "Abra no navegador:")" "${c_b}$PANEL_LINK${c_0}"
if [ -n "$NEW_KEY" ]; then
  printf '  %s\n     %s\n     %s\n\n' "$(m "Sign in with the admin key (save it now):" "Entre com a chave de administrador (guarde agora):")" "${c_b}$NEW_KEY${c_0}" \
    "$(m "(also in $ENV_FILE)" "(também em $ENV_FILE)")"
else
  printf '  %s\n\n' "$(m "Admin key unchanged: FZ_ADMIN_KEY in $ENV_FILE" "Chave de administrador mantida: FZ_ADMIN_KEY em $ENV_FILE")"
fi
if [ "$NO_DOMAIN" = 1 ] && [ "$MODE" = caddy ]; then
  printf '  %s\n\n' "$(m "Without a domain the certificate is self-signed: the browser shows a warning — choose Advanced › Proceed. Run the installer again with --domain when you have one." \
                         "Sem domínio o certificado é próprio: o navegador mostra um aviso — escolha Avançado › Continuar. Rode o instalador de novo com --domain quando tiver um.")"
fi
if [ "$MODE" = proxy ]; then
  printf '  %s\n' "$(m "Add to your web server (site $HOST, with its HTTPS certificate) — nginx:" "Acrescente no seu servidor web (site $HOST, com o certificado HTTPS dele) — nginx:")"
  cat <<EOF

    location / {
        proxy_pass http://127.0.0.1:$PORT;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        client_max_body_size 32m;
        proxy_read_timeout 180s;
    }

  Apache: ProxyPass / http://127.0.0.1:$PORT/  +  ProxyPassReverse / http://127.0.0.1:$PORT/

EOF
fi
printf '  %-12s sudo bash %s/install.sh --update\n' "$(m "Update:" "Atualizar:")" "$DIR"
printf '  %-12s cd %s && docker compose logs -f api\n' "Logs:" "$DIR"
printf '  %-12s cd %s && docker compose down   %s\n\n' "$(m "Stop:" "Parar:")" "$DIR" "$(m "(data is kept; \"down -v\" erases it)" "(os dados ficam; \"down -v\" apaga)")"
