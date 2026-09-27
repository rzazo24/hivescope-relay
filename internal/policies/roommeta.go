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
//     a ser su única dueña; solo ella podrá seguir actualizando esos
//     metadatos (name/admin) más adelante.
//
// Eventos de otro kind, o eventos kind:30078 cuyo "d" no empieza con
// "room:" (por ejemplo el de vinculación, "hive-link"), no son evaluados por
// esta política: devuelve (false, "") y deja que otras políticas decidan.
func NewRoomMetaPolicy(queryEvents QueryEventsFunc) func(ctx context.Context, event *nostr.Event) (bool, string) {
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

		linked, err := hasVerifiedHiveLink(ctx, queryEvents, event.PubKey)
		if err != nil {
			return true, fmt.Sprintf("error: could not check this pubkey's hive link: %v", err)
		}
		if !linked {
			return true, "invalid: only linked hive accounts can create or administer rooms (missing kind 30078 d=hive-link event)"
		}

		owner, err := findRoomOwner(ctx, queryEvents, d)
		if err != nil {
			return true, fmt.Sprintf("error: could not check ownership of room %q: %v", roomSlug, err)
		}
		if owner != "" && owner != event.PubKey {
			return true, fmt.Sprintf("invalid: room %q already exists and belongs to a different admin pubkey", roomSlug)
		}

		return false, ""
	}
}

// findRoomOwner devuelve el pubkey autor del evento kind:AppDataKind guardado
// con tag "d" == roomDTag, o "" si todavía no existe ninguno.
func findRoomOwner(ctx context.Context, queryEvents QueryEventsFunc, roomDTag string) (string, error) {
	filter := nostr.Filter{
		Kinds: []int{AppDataKind},
		Tags:  nostr.TagMap{"d": []string{roomDTag}},
	}

	ch, err := queryEvents(ctx, filter)
	if err != nil {
		return "", err
	}

	owner := ""
	for ev := range ch {
		// el filtro de tags del backend sqlite compara subcadenas del valor
		// contra cualquier tag, ignorando la clave -- por eso se revalida
		// aquí que el "d" coincida exactamente.
		if ev.Tags.GetD() == roomDTag {
			owner = ev.PubKey
		}
	}
	return owner, nil
}
