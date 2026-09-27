package policies

import (
	"context"
	"fmt"
	"strings"

	"github.com/nbd-wtf/go-nostr"
)

// NewAllowedEventsPolicy construye una política khatru RejectEvent que
// rechaza cualquier evento que no sea uno de los tres tipos que
// hivescope-relay conoce: mensajes de chat (kind 9), vinculación hive
// (kind 30078 d=hive-link) o metadatos de sala (kind 30078 d=room:<sala>).
//
// hivescope-relay no es un relé Nostr de propósito general: existe
// específicamente para el chat de HiveScope, así que cualquier otro tipo de
// evento (notas, perfiles, reacciones, DMs, lo que sea) se rechaza por
// defecto en vez de aceptarse silenciosamente. Las políticas específicas de
// cada tipo (NewChatMessagePolicy, NewHiveLinkPolicy, NewRoomMetaPolicy) ya
// deciden si aceptan o rechazan un evento válido de su propia forma; esta
// política deja pasar esas tres formas sin opinar (para no contradecirlas) y
// solo actúa de red de seguridad para todo lo demás. El orden en que se
// registre respecto a las otras no importa.
//
// kind:5 (NIP-09, borrado) también se deja pasar sin opinar. khatru maneja
// los eventos de borrado con su propia lógica (handleDeleteRequest, que
// verifica que el pubkey coincida con el autor del evento a borrar) *antes*
// de tratarlos como un evento "normal" -- pero igual los hace pasar después
// por handleNormal (y por lo tanto por este RejectEvent) porque kind:5 no
// es un kind efímero. Si esta política lo rechazara, el borrado ya
// aplicado igual se reportaría como fallido al cliente (falso rechazo):
// se comprobó en la práctica que el evento se borraba de la base a pesar
// del "OK false" devuelto.
func NewAllowedEventsPolicy() func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		switch event.Kind {
		case ChatMessageKind, nostr.KindDeletion:
			return false, ""
		case AppDataKind:
			d := event.Tags.GetD()
			if d == HiveLinkDTag || strings.HasPrefix(d, RoomMetaDTagPrefix) {
				return false, ""
			}
			return true, "invalid: this relay only accepts kind 30078 events with \"d\" = \"hive-link\" or \"room:<name>\""
		default:
			return true, fmt.Sprintf("invalid: this relay only accepts hivescope-relay events (kind 9, or kind 30078 hive-link/room); kind %d is not allowed", event.Kind)
		}
	}
}
