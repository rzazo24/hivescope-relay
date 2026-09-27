package policies

import (
	"context"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestAllowedEventsPolicy_AcceptsChatMessages(t *testing.T) {
	policy := NewAllowedEventsPolicy()
	reject, _ := policy(context.Background(), &nostr.Event{Kind: ChatMessageKind})
	if reject {
		t.Fatal("no debería rechazar eventos kind 9, los valida NewChatMessagePolicy")
	}
}

func TestAllowedEventsPolicy_AcceptsHiveLink(t *testing.T) {
	policy := NewAllowedEventsPolicy()
	ev := &nostr.Event{Kind: AppDataKind, Tags: nostr.Tags{{"d", HiveLinkDTag}}}
	reject, _ := policy(context.Background(), ev)
	if reject {
		t.Fatal("no debería rechazar eventos kind 30078 d=hive-link, los valida NewHiveLinkPolicy")
	}
}

func TestAllowedEventsPolicy_AcceptsRoomMeta(t *testing.T) {
	policy := NewAllowedEventsPolicy()
	ev := &nostr.Event{Kind: AppDataKind, Tags: nostr.Tags{{"d", "room:general"}}}
	reject, _ := policy(context.Background(), ev)
	if reject {
		t.Fatal("no debería rechazar eventos kind 30078 d=room:*, los valida NewRoomMetaPolicy")
	}
}

func TestAllowedEventsPolicy_RejectsUnknownAppDataTag(t *testing.T) {
	policy := NewAllowedEventsPolicy()
	cases := []nostr.Tags{
		{{"d", "algo-que-no-es-nuestro"}},
		{}, // sin tag "d"
	}
	for _, tags := range cases {
		ev := &nostr.Event{Kind: AppDataKind, Tags: tags}
		reject, msg := policy(context.Background(), ev)
		if !reject || msg == "" {
			t.Fatalf("debería rechazar kind 30078 con d=%q", ev.Tags.GetD())
		}
	}
}

func TestAllowedEventsPolicy_RejectsOtherKinds(t *testing.T) {
	policy := NewAllowedEventsPolicy()
	for _, kind := range []int{0, 1, 3, 4, 5, 7, 20000} {
		reject, msg := policy(context.Background(), &nostr.Event{Kind: kind})
		if !reject || msg == "" {
			t.Fatalf("debería rechazar eventos de kind %d", kind)
		}
	}
}
