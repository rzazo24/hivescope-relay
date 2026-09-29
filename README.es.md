# hivescope-relay

[![CI](https://github.com/rzazo24/hivescope-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/rzazo24/hivescope-relay/actions/workflows/ci.yml)

*[Read in English](README.md)*

Relé [Nostr](https://nostr.com/) self-hosted, escrito en Go con
[khatru](https://github.com/fiatjaf/khatru), pensado como backend de tiempo
real para un chat descentralizado vinculado a cuentas de
[Hive](https://hive.io/) (blockchain).

A diferencia de un relé "de fábrica" (strfry, nostr-rs-relay, etc.), este
relé tiene lógica de validación propia: solo acepta los tres tipos de evento
que necesita el chat de HiveScope, y los valida contra la blockchain de Hive
antes de guardarlos.

## Qué valida

| Evento | kind | Regla |
|---|---|---|
| Vinculación de identidad Hive↔Nostr | `30078`, `d=hive-link` | El `hive_sig` debe ser una firma real, hecha con la clave **posting** de la cuenta `hive_account`, sobre el mensaje `hivescope-relay-link:<pubkey_nostr>`. Se verifica contra la clave posting real, consultada en vivo a un nodo Hive. |
| Mensaje de chat | `9` | Requiere el tag `t` (sala), como máximo 2000 caracteres de contenido y que el pubkey emisor tenga ya un evento de vinculación válido guardado. |
| Reacción | `7` | Uno de una lista cerrada de emojis (👍 ❤️ 😂 🎉 😮 😢) sobre un mensaje de chat existente, con sus tags `e`/`p`/`t` coincidiendo con él; quien la envía debe estar vinculado y no puede repetir el mismo emoji en el mismo mensaje. |
| Metadatos de sala | `30078`, `d=room:<sala>` | Requiere `name`, un `admin` (pubkey nostr válido) y una `expiration` (NIP-40, timestamp unix futuro), y que el pubkey emisor esté vinculado a Hive. La primera cuenta vinculada que publica un nombre de sala pasa a ser su dueña; a partir de ahí, solo la dueña actual de la sala o el pubkey indicado en su tag `admin` —o cualquier otro pubkey vinculado a la misma cuenta Hive que uno de ellos, es decir, tus otros dispositivos— pueden seguir publicando actualizaciones (renombrarla, o delegar la administración en otra cuenta). |

Además de estos (las reacciones son una cuarta forma), el relé acepta borrados NIP-09 (kind `5`) y latidos de presencia efímeros (kind `20078`, ver más abajo). Cualquier evento que no encaje en una de estas formas —cualquier otro
kind, o un evento `30078` con un `d` distinto— se rechaza directamente. Este
relé no está pensado como un relé Nostr de propósito general: existe solo
para dar soporte al chat de HiveScope.

Un mensaje de chat puede borrarlo (NIP-09) el pubkey que lo envió o cualquier otro pubkey vinculado a la misma cuenta Hive; el resto de kinds, solo el mismo pubkey.

Presencia: los clientes vinculados publican latidos efímeros (kind `20078`, reenviados pero nunca guardados, con su propio límite de velocidad) para que todos vean cuántas cuentas Hive están en línea, en total y por sala. El mismo latido lleva un tag `typing` para el aviso «está escribiendo…».

## Caducidad de salas y limpieza

Las salas no son permanentes: todo evento de metadatos de sala lleva una
`expiration` NIP-40. El relé la aplica por su cuenta (`internal/roomsweep`,
al arrancar y cada 5 minutos) en vez de fiarse del barrido interno de
khatru, que resultó poco fiable. Cualquier actualización de una sala
(renombrarla, delegar admin, o simplemente volver a guardarla sin cambios)
publica una `expiration` nueva, que es como se "renueva" — no hay una acción
de renovar aparte.

El mismo barrido mantiene el almacenamiento coherente: borra las filas
viejas de una sala que dejan otros pubkeys (cada navegador tiene el suyo, así
que editar desde otro dispositivo dejaría viva la fila anterior) y borra los
mensajes `kind:9` de una sala cuando ya no existe. Solo comprueba si la sala
existe ahora mismo, no por qué ni cuándo se envió cada mensaje, así que una
sala que se sigue renovando nunca pierde mensajes.

## Estructura del proyecto

```
main.go                          arranca khatru + sqlite + registra las políticas
internal/hiveapi/                cliente JSON-RPC contra la API pública de Hive
internal/hivecrypto/             verificación de firmas Hive (secp256k1 recuperable)
internal/policies/               las tres políticas de validación (RejectEvent de khatru)
test/test-relay.mjs              prueba de humo en Node (nostr-tools) contra un relé real
Dockerfile, docker-compose.yml   build e infraestructura para desplegar
Caddyfile                        reverse proxy con TLS automático (Let's Encrypt)
```

## Correr en local (sin Docker)

Requiere Go 1.27+ y un compilador de C (el driver de SQLite usa cgo).

```bash
go run .
```

Por defecto escucha en `:3334`, guarda la base en
`./data/hivescope-relay.sqlite` y consulta `https://api.hive.blog`. Se puede
cambiar con variables de entorno:

| Variable | Default | Qué hace |
|---|---|---|
| `HIVESCOPE_LISTEN_ADDR` | `:3334` | dirección/puerto donde escucha el relé |
| `HIVESCOPE_DB_PATH` | `./data/hivescope-relay.sqlite` | ruta del archivo SQLite |
| `HIVESCOPE_HIVE_NODE` | `https://api.hive.blog` | nodo Hive contra el que se verifican las cuentas |
| `HIVESCOPE_SUPERADMIN_HIVE_ACCOUNT` | *(vacío, deshabilitado)* | cuenta Hive (sin distinguir mayúsculas/minúsculas) exenta de la comprobación de dueño de sala — puede renombrar/re-delegar cualquier sala, no solo las que creó o administra. Sigue sin tener privilegio de borrar mensajes ni de expulsar/silenciar cuentas. |

## Correr los tests

```bash
# tests unitarios de Go (verificación de firmas y políticas)
go test ./...

# prueba de humo de extremo a extremo (necesita un relé corriendo)
cd test
npm install
RELAY_URL=ws://localhost:3334 npm test
```

El script de Node genera dos pares de claves de prueba y confirma que el
relé rechaza: (1) un evento de vinculación con firma hive inválida, y (2) un
mensaje de chat de un pubkey que nunca se vinculó.

## Desplegar con Docker

```bash
docker compose up -d --build
```

Esto levanta dos contenedores:
- `relay`: el binario de Go + SQLite. **No publica ningún puerto al host** —
  solo es alcanzable desde `caddy`, dentro de la red interna de Docker.
- `caddy`: reverse proxy que expone `80`/`443` al exterior y obtiene
  automáticamente un certificado TLS de Let's Encrypt para el dominio
  configurado en `Caddyfile` (por defecto `relay.hivescope.xyz` — cambialo
  ahí si usás otro dominio).

Los datos de SQLite y los certificados de Caddy quedan en volúmenes con
nombre (`relay-data`, `caddy-data`, `caddy-config`), así que sobreviven a un
`docker compose down` (sin `-v`). Los dos servicios también tienen limitado
el tamaño de sus logs de Docker a 10 MB × 3 archivos (driver `json-file`,
configurado en `docker-compose.yml`) — por defecto no tiene límite, y eso
iría llenando el disco de a poco con el tiempo.

Los dos contenedores tienen `restart: unless-stopped`, y el propio Docker
está habilitado para arrancar con el sistema, así que un reinicio del VPS
levanta todo solo — siempre que `docker.service` esté habilitado
(`systemctl is-enabled docker`) y los contenedores no se hayan parado a mano
antes.

Los dos tienen también un `HEALTHCHECK` de Docker: `relay` se ejecuta a sí
mismo con `--healthcheck` (un modo mínimo que solo hace un GET a su propio
endpoint NIP-11 — la imagen final es debian-slim sin curl/wget, así que esto
evita instalar herramientas extra solo para esto), y `caddy` hace ping a su
propia API de administración en `127.0.0.1:2019`. `caddy` no arranca hasta
que `relay` reporta estar sano (`depends_on: condition: service_healthy`).

Este contenedor `caddy` también hace de entrada HTTPS compartida para todo
el VPS: como solo un proceso puede escuchar en el puerto 443 del host, el
frontend [hivescope-web](https://github.com/rzazo24/hivescope-relay-web)
(un repo aparte, que se espera clonado como directorio hermano,
`../hivescope-web`) se sirve desde el mismo Caddy con otro bloque de sitio
en `Caddyfile`, montando su build (`dist/`) como otro volumen de solo
lectura. Los dos proyectos siguen teniendo código y pasos de despliegue
totalmente independientes — redesplegar el frontend es reconstruir
`hivescope-web` y recrear solo este contenedor `caddy`, sin tocar `relay`.

### Antes de levantarlo en un VPS

1. **DNS**: el dominio que pusiste en `Caddyfile` tiene que resolver (registro
   A/AAAA) a la IP pública del VPS *antes* de levantar Caddy — si no, Let's
   Encrypt no va a poder validar el dominio.
2. **Firewall del VPS** (a nivel de sistema operativo): abrir `tcp/80` y
   `tcp/443`. En Ubuntu con iptables (sin ufw), algo así, insertando antes de
   cualquier regla de `REJECT`/`DROP` general:
   ```bash
   sudo iptables -I INPUT <linea> -p tcp -m state --state NEW -m tcp --dport 80 -j ACCEPT
   sudo iptables -I INPUT <linea> -p tcp -m state --state NEW -m tcp --dport 443 -j ACCEPT
   sudo netfilter-persistent save   # para que sobreviva un reboot
   ```
3. **Firewall de la nube** (si es Oracle Cloud): además del firewall del
   sistema operativo, hay un firewall aparte a nivel de red —el **Security
   List** (o **Network Security Group**) de la VCN— que por defecto no deja
   pasar nada salvo SSH. Sin abrir ahí `tcp/80` y `tcp/443` (Source
   `0.0.0.0/0`), Let's Encrypt nunca va a poder completar la verificación,
   aunque el firewall del sistema operativo ya esté abierto: consola de OCI
   → tu instancia → *Attached VNICs* → el VNIC → *Subnet* → *Security Lists*
   → la lista por defecto → *Add Ingress Rules*.
4. Recién ahí: `docker compose up -d --build`. Podés seguir el progreso del
   certificado con `docker compose logs -f caddy` — si el DNS y los dos
   firewalls están bien, en general se resuelve en segundos.

### Copias de seguridad

`scripts/backup-db.sh` hace una copia consistente de la base SQLite (usando
el propio comando `.backup` de sqlite3, seguro incluso con el relé
escribiendo en ese momento — no es una copia cruda del archivo) y la
comprime con gzip. Corre en un contenedor Alpine desechable que monta el
mismo volumen Docker `relay-data` en modo lectura, así que no necesita nada
instalado en el host aparte de Docker.

```bash
./scripts/backup-db.sh
```

Por defecto escribe en `~/backups/hivescope-relay/` y borra los backups de
más de 14 días; ambas cosas se pueden cambiar con las variables de entorno
`BACKUP_DIR` y `RETENTION_DAYS`. En el VPS de producción está programado a
diario con cron:

```
17 3 * * * /ruta/a/hivescope-relay/scripts/backup-db.sh >> ~/backups/hivescope-relay/backup.log 2>&1
```

Para restaurar, parar el relé, descomprimir un backup encima del archivo de
base del volumen, y arrancarlo de nuevo:

```bash
docker compose stop relay
gunzip -c ~/backups/hivescope-relay/hivescope-relay-<timestamp>.sqlite.gz \
  | docker run --rm -i -v nostr-relay_relay-data:/data alpine:3.20 sh -c 'cat > /data/hivescope-relay.sqlite'
docker compose start relay
```

## Vincular una cuenta Hive (para el frontend)

Un cliente tiene que publicar un evento `kind:30078` así:

```json
{
  "kind": 30078,
  "tags": [
    ["d", "hive-link"],
    ["hive_account", "<usuario_hive>"],
    ["hive_sig", "<firma_hex>"],
    ["hive_key_type", "posting"]
  ],
  "content": ""
}
```

Donde `hive_sig` es el resultado de firmar, **con la clave posting de esa
cuenta** (por ejemplo con `hive_keychain.requestSignBuffer`), exactamente el
string:

```
hivescope-relay-link:<pubkey_nostr_del_evento>
```

(el pubkey del propio evento que se está publicando, en hex). Ver
`internal/policies/hivelink.go` (función `LinkChallenge`) para el detalle
exacto — es un contrato entre el frontend y el relé, no se puede cambiar de
un lado sin el otro.

## Chequeo de salud

`scripts/healthcheck.sh` (lanzado por cron cada 2 minutos) pide por HTTPS el chat y el documento NIP-11 del relé; tras dos fallos seguidos reinicia `caddy` (si cae el chat) o `relay` (si solo cae el relé), como mucho una vez cada 10 minutos, y deja constancia en `~/backups/hivescope-relay/healthcheck.log`. Solo se recupera solo: no avisa a nadie y no sirve si cae la máquina entera.

## Licencia

[MIT](LICENSE)
