#!/usr/bin/env bash
# Chequeo de salud en el propio servidor: comprueba que el chat (Caddy) y el
# relé responden por HTTPS y, si fallan varias veces seguidas, reinicia el
# contenedor que toca. No avisa a nadie (para eso haría falta algo externo):
# solo intenta recuperarse solo y deja constancia en el log.
#
#   - El chat falla            -> se reinicia caddy (sirve el frontend y hace de proxy)
#   - Solo falla el relé       -> se reinicia relay (NIP-11 pasa por caddy hasta el relé)
#
# Para no reiniciar por un fallo puntual hacen falta FAIL_THRESHOLD fallos
# seguidos, y tras un reinicio se espera COOLDOWN_SECONDS antes de otro (evita
# bucles si el problema no lo arregla un reinicio).
#
# Pensado para cron, p. ej. cada 2 minutos:
#   */2 * * * * /home/ubuntu/proyectos/nostr-relay/scripts/healthcheck.sh
#
# Variables de entorno opcionales (las usan también las pruebas):
#   CHAT_URL, RELAY_URL, FAIL_THRESHOLD (2), COOLDOWN_SECONDS (600),
#   STATE_DIR, LOG_FILE, COMPOSE_DIR, RESTART_CMD (por defecto "docker compose restart")

set -uo pipefail

CHAT_URL="${CHAT_URL:-https://chat.hivescope.xyz}"
RELAY_URL="${RELAY_URL:-https://relay.hivescope.xyz}"
FAIL_THRESHOLD="${FAIL_THRESHOLD:-2}"
COOLDOWN_SECONDS="${COOLDOWN_SECONDS:-600}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/hivescope-health}"
LOG_FILE="${LOG_FILE:-$HOME/backups/hivescope-relay/healthcheck.log}"
COMPOSE_DIR="${COMPOSE_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
RESTART_CMD="${RESTART_CMD:-docker compose restart}"

mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"

# Un solo chequeo a la vez (cron cada 2 min y un reinicio puede tardar).
exec 9>"$STATE_DIR/lock"
flock -n 9 || exit 0

# El log no crece sin límite: pasado 1 MB se queda con la mitad final.
if [ -f "$LOG_FILE" ] && [ "$(stat -c %s "$LOG_FILE")" -gt 1048576 ]; then
  tail -n 2000 "$LOG_FILE" > "$LOG_FILE.tmp" && mv "$LOG_FILE.tmp" "$LOG_FILE"
fi
log() { echo "$(date -u +%FT%TZ) $*" >> "$LOG_FILE"; }

up() { curl -fsS -o /dev/null --max-time 10 "$@" 2>/dev/null; }

# name url [curl args...]: devuelve 0 si responde bien.
check() { local url="$1"; shift; up "$@" "$url"; }

fails() { cat "$STATE_DIR/$1.fails" 2>/dev/null || echo 0; }

# Registra el resultado y devuelve 0 solo cuando toca actuar (umbral alcanzado).
record() {
  local name="$1" ok="$2" n
  n="$(fails "$name")"
  if [ "$ok" = 1 ]; then
    [ "$n" -gt 0 ] && log "$name recuperado tras $n fallo(s)"
    echo 0 > "$STATE_DIR/$name.fails"
    return 1
  fi
  n=$((n + 1))
  echo "$n" > "$STATE_DIR/$name.fails"
  log "$name NO responde ($n/$FAIL_THRESHOLD)"
  [ "$n" -ge "$FAIL_THRESHOLD" ]
}

restart() {
  local service="$1" now last
  now="$(date +%s)"
  last="$(cat "$STATE_DIR/last-restart" 2>/dev/null || echo 0)"
  if [ $((now - last)) -lt "$COOLDOWN_SECONDS" ]; then
    log "reinicio de $service omitido (cooldown, último hace $((now - last))s)"
    return
  fi
  echo "$now" > "$STATE_DIR/last-restart"
  log "reiniciando $service"
  (cd "$COMPOSE_DIR" && $RESTART_CMD "$service") >> "$LOG_FILE" 2>&1 || log "el reinicio de $service falló"
  echo 0 > "$STATE_DIR/chat.fails"
  echo 0 > "$STATE_DIR/relay.fails"
}

chat_ok=0; relay_ok=0
check "$CHAT_URL" && chat_ok=1
check "$RELAY_URL" -H 'Accept: application/nostr+json' && relay_ok=1

# Se anotan los dos, pero solo se actúa sobre uno por pasada: si el chat cae,
# el relé (que pasa por el mismo caddy) también, y ahí basta reiniciar caddy.
act_chat=1; act_relay=1
record chat "$chat_ok" && act_chat=0
record relay "$relay_ok" && act_relay=0

if [ "$act_chat" = 0 ]; then
  restart caddy
elif [ "$act_relay" = 0 ] && [ "$chat_ok" = 1 ]; then
  restart relay
fi
exit 0
