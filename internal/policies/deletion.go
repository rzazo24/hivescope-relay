package policies

import (
	"context"
	"strings"

	"github.com/nbd-wtf/go-nostr"
)

// NewDeletionOutcome construye la función que khatru consulta (en
// relay.OverwriteDeletionOutcome) para decidir si una petición de borrado
// NIP-09 (kind 5) puede eliminar un evento. khatru trae su propia regla
// -- solo el mismo pubkey -- pero, si hay una función de este tipo, usa su
// resultado EN LUGAR de esa regla, así que aquí hay que reproducirla también.
//
// Reglas:
//  1. El mismo pubkey que publicó el evento siempre puede borrarlo (es lo que
//     haría khatru por defecto).
//  2. Solo para mensajes de chat (kind 9) y reacciones (kind 7): otro pubkey vinculado a la MISMA
//     cuenta Hive que el autor también puede. Cada navegador/dispositivo
//     genera su propio pubkey, pero la identidad real es la cuenta Hive (igual
//     que con la propiedad de las salas, ver sharesHiveAccount).
//
// Deliberadamente NO se extiende a otros kinds: con eso, un dispositivo podría
// borrar, por ejemplo, la vinculación (hive-link) de otro dispositivo de la
// misma cuenta.
func NewDeletionOutcome(queryEvents QueryEventsFunc) func(ctx context.Context, target, deletion *nostr.Event) (bool, string) {
	return func(ctx context.Context, target, deletion *nostr.Event) (bool, string) {
		if target.PubKey == deletion.PubKey {
			return true, ""
		}
		const denied = "you are not the author of this event"
		if target.Kind != ChatMessageKind && target.Kind != ReactionKind {
			return false, denied
		}

		requester, err := findLinkedHiveAccount(ctx, queryEvents, deletion.PubKey)
		if err != nil || requester == "" {
			return false, denied
		}
		author, err := findLinkedHiveAccount(ctx, queryEvents, target.PubKey)
		if err != nil || author == "" || !strings.EqualFold(author, requester) {
			return false, denied
		}
		return true, ""
	}
}
