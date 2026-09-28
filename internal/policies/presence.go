package policies

import (
	"context"
	"fmt"
	"regexp"

	"github.com/nbd-wtf/go-nostr"
)

// presenceMaxSkew es cuánto puede desviarse el created_at de un latido de la
// hora del relé. Los latidos no se guardan, pero esto evita que alguien
// reenvíe latidos viejos capturados para aparentar presencia.
const presenceMaxSkew = 120 // segundos

var presenceRoomSlug = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// NewPresencePolicy valida los latidos de presencia (kind PresenceKind):
//
//	tags:
//	  ["t", "<sala>"]   (opcional; sin él, el usuario está en la lista de salas)
//	  ["left"]          (opcional; el usuario acaba de salir)
//	content: ""
//
// Solo pubkeys con vinculación hive verificada pueden anunciar presencia (así
// "usuarios en línea" son cuentas Hive reales, no cualquier conexión), y el
// evento tiene que ser exactamente eso: sin contenido, con solo esos tags y
// con la hora cerca de la del relé. Otros kinds no se evalúan aquí.
func NewPresencePolicy(queryEvents QueryEventsFunc) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind != PresenceKind {
			return false, ""
		}

		if event.Content != "" {
			return true, "invalid: presence events must have empty content"
		}
		for _, tag := range event.Tags {
			switch {
			case len(tag) == 2 && tag[0] == "t" && presenceRoomSlug.MatchString(tag[1]):
			case len(tag) == 1 && tag[0] == "left":
			default:
				return true, "invalid: presence events only allow a \"t\" tag with a room name and/or a \"left\" tag"
			}
		}

		skew := int64(event.CreatedAt) - int64(nostr.Now())
		if skew > presenceMaxSkew || skew < -presenceMaxSkew {
			return true, fmt.Sprintf("invalid: presence event created_at is more than %ds away from the relay's clock", presenceMaxSkew)
		}

		linked, err := hasVerifiedHiveLink(ctx, queryEvents, event.PubKey)
		if err != nil {
			return true, fmt.Sprintf("error: could not check this pubkey's hive link: %v", err)
		}
		if !linked {
			return true, "invalid: only linked hive accounts can announce presence (missing kind 30078 d=hive-link event)"
		}
		return false, ""
	}
}

// SplitRateLimit aplica limitPresence a los latidos y limitOthers al resto de
// eventos. Los latidos (unos 2-3 por minuto por pestaña) gastarían enseguida
// el límite del chat, y varias personas tras la misma IP compartirían el
// cupo, así que tienen el suyo, más holgado.
func SplitRateLimit(
	limitPresence, limitOthers func(ctx context.Context, event *nostr.Event) (bool, string),
) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind == PresenceKind {
			return limitPresence(ctx, event)
		}
		return limitOthers(ctx, event)
	}
}
