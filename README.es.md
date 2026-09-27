# hivescope-relay

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
| Mensaje de chat | `9` | Requiere el tag `t` (sala) y que el pubkey emisor tenga ya un evento de vinculación válido guardado. |
| Metadatos de sala | `30078`, `d=room:<sala>` | Requiere `name` y un `admin` (pubkey nostr válido), y que el pubkey emisor esté vinculado a Hive. La primera cuenta vinculada que publica un nombre de sala pasa a ser su única dueña. |

El detalle de cada regla está documentado como comentario en el archivo de
la política correspondiente, en `internal/policies/`.

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
`docker compose down` (sin `-v`).

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
