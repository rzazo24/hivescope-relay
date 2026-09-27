package policies

import (
	"context"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func fakeQueryEvents(events []*nostr.Event, err error) QueryEventsFunc {
	return func(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
		if err != nil {
			return nil, err
		}
		ch := make(chan *nostr.Event, len(events))
		for _, e := range events {
			ch <- e
		}
		close(ch)
		return ch, nil
	}
}

func TestChatMessagePolicy_IgnoresOtherKinds(t *testing.T) {
	policy := NewChatMessagePolicy(fakeQueryEvents(nil, nil))
	reject, _ := policy(context.Background(), &nostr.Event{Kind: 1})
	if reject {
		t.Fatal("no debería rechazar eventos de otro kind")
	}
}

func TestChatMessagePolicy_RejectsMissingRoomTag(t *testing.T) {
	policy := NewChatMessagePolicy(fakeQueryEvents(nil, nil))
	reject, msg := policy(context.Background(), &nostr.Event{Kind: ChatMessageKind, PubKey: "abc"})
	if !reject {
		t.Fatal("debería rechazar un mensaje sin tag \"t\"")
	}
	if msg == "" {
		t.Fatal("se esperaba un mensaje de rechazo")
	}
}

func TestChatMessagePolicy_RejectsWithoutHiveLink(t *testing.T) {
	policy := NewChatMessagePolicy(fakeQueryEvents(nil, nil))
	ev := &nostr.Event{
		Kind:   ChatMessageKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"t", "sala-general"}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar si no hay ningún evento de vinculación guardado")
	}
}

func TestChatMessagePolicy_AcceptsWithHiveLink(t *testing.T) {
	linkEvent := &nostr.Event{
		Kind:   HiveLinkKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"d", HiveLinkDTag}},
	}
	policy := NewChatMessagePolicy(fakeQueryEvents([]*nostr.Event{linkEvent}, nil))
	ev := &nostr.Event{
		Kind:   ChatMessageKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"t", "sala-general"}},
	}
	reject, _ := policy(context.Background(), ev)
	if reject {
		t.Fatal("debería aceptar si existe un evento de vinculación válido guardado")
	}
}

func TestChatMessagePolicy_IgnoresRoomMetadataEvent(t *testing.T) {
	// Un evento kind:30078 con otro "d" (metadatos de sala) no cuenta como
	// vinculación, aunque comparta kind con hive-link.
	roomEvent := &nostr.Event{
		Kind:   HiveLinkKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"d", "room:general"}},
	}
	policy := NewChatMessagePolicy(fakeQueryEvents([]*nostr.Event{roomEvent}, nil))
	ev := &nostr.Event{
		Kind:   ChatMessageKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"t", "sala-general"}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("un evento de metadatos de sala no debería contar como vinculación")
	}
}

func TestChatMessagePolicy_PropagatesQueryError(t *testing.T) {
	policy := NewChatMessagePolicy(fakeQueryEvents(nil, errors.New("db down")))
	ev := &nostr.Event{
		Kind:   ChatMessageKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"t", "sala-general"}},
	}
	reject, msg := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar si falla la consulta al almacenamiento")
	}
	if msg == "" {
		t.Fatal("se esperaba un mensaje de error")
	}
}
