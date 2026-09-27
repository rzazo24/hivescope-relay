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
func NewAllowedEventsPolicy() func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		switch event.Kind {
		case ChatMessageKind:
			return false, ""
		case AppDataKind:
			d := event.Tags.GetD()
			if d == HiveLinkDTag || strings.HasPrefix(d, RoomMetaDTagPrefix) {
				return false, ""
			}
			return true, "invalid: este relé no acepta eventos kind 30078 con \"d\" distinto de \"hive-link\" o \"room:<sala>\""
		default:
			return true, fmt.Sprintf("invalid: este relé solo acepta eventos de hivescope-relay (kind 9, o kind 30078 hive-link/room); kind %d no está permitido", event.Kind)
		}
	}
}
