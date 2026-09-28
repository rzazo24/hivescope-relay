package policies

import (
	"context"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func presenceEvent(pubkey string, tags nostr.Tags, createdAt nostr.Timestamp) *nostr.Event {
	return &nostr.Event{Kind: PresenceKind, PubKey: pubkey, CreatedAt: createdAt, Tags: tags}
}

func TestPresencePolicy_IgnoresOtherKinds(t *testing.T) {
	policy := NewPresencePolicy(fakeQueryEvents(nil, nil))
	if reject, _ := policy(context.Background(), &nostr.Event{Kind: 1}); reject {
		t.Fatal("no debería evaluar otros kinds")
	}
}

func TestPresencePolicy_AcceptsHeartbeatsFromLinkedPubkeys(t *testing.T) {
	policy := NewPresencePolicy(fakeQueryEvents([]*nostr.Event{linkedEventFor("ana")}, nil))
	for name, tags := range map[string]nostr.Tags{
		"en una sala":          {{"t", "general"}},
		"en la lista de salas": {},
		"saliendo":             {{"t", "general"}, {"left"}},
	} {
		if reject, msg := policy(context.Background(), presenceEvent("ana", tags, nostr.Now())); reject {
			t.Errorf("%s: debería aceptarse, rechazado: %s", name, msg)
		}
	}
}

func TestPresencePolicy_RejectsUnlinkedPubkeys(t *testing.T) {
	policy := NewPresencePolicy(fakeQueryEvents(nil, nil))
	if reject, _ := policy(context.Background(), presenceEvent("anonimo", nil, nostr.Now())); !reject {
		t.Fatal("un pubkey sin vinculación no debería poder anunciar presencia")
	}
}

func TestPresencePolicy_RejectsMalformedEvents(t *testing.T) {
	policy := NewPresencePolicy(fakeQueryEvents([]*nostr.Event{linkedEventFor("ana")}, nil))
	bad := map[string]*nostr.Event{
		"con contenido":       {Kind: PresenceKind, PubKey: "ana", CreatedAt: nostr.Now(), Content: "hola"},
		"tag desconocido":     presenceEvent("ana", nostr.Tags{{"p", "x"}}, nostr.Now()),
		"sala inválida":       presenceEvent("ana", nostr.Tags{{"t", "Con Espacios"}}, nostr.Now()),
		"sala vacía":          presenceEvent("ana", nostr.Tags{{"t", ""}}, nostr.Now()),
		"t con valores extra": presenceEvent("ana", nostr.Tags{{"t", "general", "extra"}}, nostr.Now()),
		"muy antiguo":         presenceEvent("ana", nil, nostr.Now()-3600),
		"del futuro":          presenceEvent("ana", nil, nostr.Now()+3600),
	}
	for name, ev := range bad {
		if reject, _ := policy(context.Background(), ev); !reject {
			t.Errorf("%s: debería rechazarse", name)
		}
	}
}

func TestPresencePolicy_PropagatesQueryError(t *testing.T) {
	policy := NewPresencePolicy(fakeQueryEvents(nil, errors.New("db down")))
	if reject, msg := policy(context.Background(), presenceEvent("ana", nil, nostr.Now())); !reject || msg == "" {
		t.Fatal("debería rechazar con un motivo si falla la consulta")
	}
}

func TestSplitRateLimit_RoutesByKind(t *testing.T) {
	var presenceCalls, otherCalls int
	limit := SplitRateLimit(
		func(context.Context, *nostr.Event) (bool, string) { presenceCalls++; return false, "" },
		func(context.Context, *nostr.Event) (bool, string) { otherCalls++; return true, "rate-limited" },
	)
	if reject, _ := limit(context.Background(), &nostr.Event{Kind: PresenceKind}); reject {
		t.Fatal("los latidos usan el limitador de presencia")
	}
	if reject, _ := limit(context.Background(), &nostr.Event{Kind: ChatMessageKind}); !reject {
		t.Fatal("el resto usa el otro limitador")
	}
	if presenceCalls != 1 || otherCalls != 1 {
		t.Fatalf("cada limitador debería llamarse una vez: presencia=%d otros=%d", presenceCalls, otherCalls)
	}
}
