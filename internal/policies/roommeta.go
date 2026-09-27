package policies

import (
	"context"
	"fmt"
	"strings"

	"github.com/nbd-wtf/go-nostr"
)

// RoomMetaDTagPrefix identifica, dentro del kind AppDataKind, a los eventos
// de metadatos de sala: su tag "d" tiene la forma "room:<nombre-sala>".
const RoomMetaDTagPrefix = "room:"

// NewRoomMetaPolicy construye una política khatru RejectEvent para metadatos
// de sala:
//
//	kind: 30078
//	tags:
//	  ["d", "room:<sala>"]
//	  ["name", "<nombre visible>"]
//	  ["admin", "<pubkey_nostr_admin>"]
//
// Reglas aplicadas:
//  1. El nombre de sala (lo que sigue a "room:" en el tag "d") no puede estar
//     vacío, y deben estar presentes "name" y un "admin" con forma de pubkey
//     nostr válido (64 caracteres hex).
//  2. El pubkey que firma el evento debe tener ya una vinculación hive
//     verificada (mismo requisito que para publicar mensajes de chat): solo
//     cuentas hive vinculadas pueden crear o administrar salas.
//  3. Como este kind es parametrizado reemplazable (NIP-33), cada autor tiene
//     su propia serie independiente de eventos para un mismo "d" -- nada
//     impide, a nivel de protocolo, que dos pubkeys distintos publiquen
//     ambos un "room:general". Para que el nombre de sala sea realmente
//     único, esta política rechaza la publicación si ya existe un evento
//     "room:<sala>" guardado con ese mismo "d" mas perteneciente a OTRO
//     pubkey: la primera cuenta vinculada que reclama un nombre de sala pasa
//     a ser su única dueña. A partir de ahí, solo pueden seguir actualizando
//     esos metadatos (name/admin) quien publicó la versión vigente de la
//     sala (su autor actual, no necesariamente quien la creó originalmente)
//     o quien figure como "admin" en esa versión -- así, el autor vigente
//     puede delegar la administración en otra cuenta publicando con un
//     "admin" distinto, y desde ese momento es esa cuenta delegada la que
//     controla la sala hacia adelante (ver findRoomOwnership).
//  4. Si superadminHiveAccount no está vacío, la cuenta Hive vinculada con
//     ese nombre (comparado sin distinguir mayúsculas/minúsculas) se salta
//     por completo la regla 3: puede actualizar los metadatos de CUALQUIER
//     sala, no solo las que creó o administra, sin dejar de necesitar una
//     vinculación hive válida (regla 2). Sigue aplicando el "primer
//     registrante es dueño" para nombres de sala todavía libres -- esto no
//     le da ningún privilegio ahí, solo le permite saltarse el bloqueo de
//     "ya existe y no sos su dueño". Pensado para un único operador del
//     relé (a pedido explícito del usuario, alcance elegido: solo
//     renombrar/editar, no borrar mensajes ni expulsar cuentas).
//
// Eventos de otro kind, o eventos kind:30078 cuyo "d" no empieza con
// "room:" (por ejemplo el de vinculación, "hive-link"), no son evaluados por
// esta política: devuelve (false, "") y deja que otras políticas decidan.
func NewRoomMetaPolicy(queryEvents QueryEventsFunc, superadminHiveAccount string) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind != AppDataKind {
			return false, ""
		}

		d := event.Tags.GetD()
		if !strings.HasPrefix(d, RoomMetaDTagPrefix) {
			return false, ""
		}

		roomSlug := strings.TrimPrefix(d, RoomMetaDTagPrefix)
		if roomSlug == "" {
			return true, "invalid: the room name in the \"d\" tag (room:<name>) can't be empty"
		}

		if event.Tags.Find("name").Value() == "" {
			return true, "invalid: missing \"name\" tag with the room's display name"
		}

		admin := event.Tags.Find("admin").Value()
		if !nostr.IsValid32ByteHex(admin) {
			return true, "invalid: the \"admin\" tag must be a valid nostr pubkey (64 hex characters)"
		}

		linkedAccount, err := findLinkedHiveAccount(ctx, queryEvents, event.PubKey)
		if err != nil {
			return true, fmt.Sprintf("error: could not check this pubkey's hive link: %v", err)
		}
		if linkedAccount == "" {
			return true, "invalid: only linked hive accounts can create or administer rooms (missing kind 30078 d=hive-link event)"
		}
		isSuperadmin := superadminHiveAccount != "" && strings.EqualFold(linkedAccount, superadminHiveAccount)

		ownership, err := findRoomOwnership(ctx, queryEvents, d)
		if err != nil {
			return true, fmt.Sprintf("error: could not check ownership of room %q: %v", roomSlug, err)
		}
		if !isSuperadmin && ownership.owner != "" && event.PubKey != ownership.owner && event.PubKey != ownership.admin {
			return true, fmt.Sprintf("invalid: room %q already exists and can only be updated by its creator or current admin", roomSlug)
		}

		return false, ""
	}
}

// roomOwnership junta, para una sala ya existente, tanto el pubkey que la
// creó originalmente (owner, el autor del primer evento NIP-33 guardado)
// como el pubkey que figura en el tag "admin" de esa última versión
// guardada -- ambos quedan habilitados para seguir publicando
// actualizaciones de sus metadatos.
type roomOwnership struct {
	owner string
	admin string
}

// findRoomOwnership devuelve el owner y el admin del evento kind:AppDataKind
// "más nuevo" guardado con tag "d" == roomDTag, o una roomOwnership vacía si
// todavía no existe ninguno.
//
// El reemplazo NIP-33 del backend (ver ReplaceEvent) es por (pubkey, kind,
// d): compara solo dentro de la propia serie de un mismo autor. Por eso, una
// vez que se delega la administración a un pubkey distinto del creador
// original, pueden quedar guardadas legítimamente DOS filas con el mismo "d"
// -- la última del creador y la última del admin delegado -- cada una en su
// propia serie. Para decidir cuál manda hay que compararlas por created_at
// (con el mismo criterio de empate que usa el backend: id mayor gana), no
// alcanza con tomar "la última iterada" del canal.
func findRoomOwnership(ctx context.Context, queryEvents QueryEventsFunc, roomDTag string) (roomOwnership, error) {
	filter := nostr.Filter{
		Kinds: []int{AppDataKind},
		Tags:  nostr.TagMap{"d": []string{roomDTag}},
	}

	ch, err := queryEvents(ctx, filter)
	if err != nil {
		return roomOwnership{}, err
	}

	var newest *nostr.Event
	for ev := range ch {
		// el filtro de tags del backend sqlite compara subcadenas del valor
		// contra cualquier tag, ignorando la clave -- por eso se revalida
		// aquí que el "d" coincida exactamente.
		if ev.Tags.GetD() != roomDTag {
			continue
		}
		if newest == nil || isNewerRoomEvent(ev, newest) {
			newest = ev
		}
	}
	if newest == nil {
		return roomOwnership{}, nil
	}
	return roomOwnership{owner: newest.PubKey, admin: newest.Tags.Find("admin").Value()}, nil
}

// isNewerRoomEvent decide si a manda sobre b, con el mismo criterio de
// empate que usa eventstore/sqlite3.ReplaceEvent (mayor created_at gana, y
// en caso de empate el id mayor).
func isNewerRoomEvent(a, b *nostr.Event) bool {
	return a.CreatedAt > b.CreatedAt || (a.CreatedAt == b.CreatedAt && a.ID > b.ID)
}
