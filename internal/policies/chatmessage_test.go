package policies

import (
	"context"
	"errors"
	"slices"
	"strings"
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
			// El backend real respeta Authors; el resto de filtros (kinds, tags)
			// los revalidan las propias políticas, así que no se replican aquí.
			if len(filter.Authors) > 0 && !slices.Contains(filter.Authors, e.PubKey) {
				continue
			}
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
		Tags:   nostr.Tags{{"d", HiveLinkDTag}, {"hive_account", "abc"}},
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

func TestChatMessagePolicy_EnforcesMaxLength(t *testing.T) {
	linkEvent := &nostr.Event{
		Kind:   HiveLinkKind,
		PubKey: "abc",
		Tags:   nostr.Tags{{"d", HiveLinkDTag}, {"hive_account", "abc"}},
	}
	policy := NewChatMessagePolicy(fakeQueryEvents([]*nostr.Event{linkEvent}, nil))
	msg := func(content string) *nostr.Event {
		return &nostr.Event{Kind: ChatMessageKind, PubKey: "abc", Tags: nostr.Tags{{"t", "sala"}}, Content: content}
	}
	if reject, m := policy(context.Background(), msg(strings.Repeat("a", MaxChatMessageLength))); reject {
		t.Fatalf("el máximo exacto debería aceptarse: %s", m)
	}
	// se cuentan caracteres, no bytes: 2000 emojis (4 bytes cada uno) caben
	if reject, m := policy(context.Background(), msg(strings.Repeat("😀", MaxChatMessageLength))); reject {
		t.Fatalf("2000 emojis deberían aceptarse: %s", m)
	}
	if reject, _ := policy(context.Background(), msg(strings.Repeat("a", MaxChatMessageLength+1))); !reject {
		t.Fatal("un carácter de más debería rechazarse")
	}
}
