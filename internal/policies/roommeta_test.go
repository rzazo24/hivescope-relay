package policies

import (
	"context"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

const testAdminPubkey = "1111111111111111111111111111111111111111111111111111111111111111"

func linkedEventFor(pubkey string) *nostr.Event {
	return &nostr.Event{
		Kind:   HiveLinkKind,
		PubKey: pubkey,
		Tags:   nostr.Tags{{"d", HiveLinkDTag}},
	}
}

func TestRoomMetaPolicy_IgnoresOtherKinds(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil))
	reject, _ := policy(context.Background(), &nostr.Event{Kind: 1})
	if reject {
		t.Fatal("no debería rechazar eventos de otro kind")
	}
}

func TestRoomMetaPolicy_IgnoresNonRoomDTag(t *testing.T) {
	// Un evento kind:30078 con otro "d" (por ejemplo hive-link) no es de esta política.
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil))
	ev := &nostr.Event{Kind: AppDataKind, Tags: nostr.Tags{{"d", HiveLinkDTag}}}
	reject, _ := policy(context.Background(), ev)
	if reject {
		t.Fatal("no debería rechazar un evento 30078 que no es de sala")
	}
}

func TestRoomMetaPolicy_RejectsEmptyRoomSlug(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil))
	ev := &nostr.Event{Kind: AppDataKind, Tags: nostr.Tags{{"d", "room:"}}}
	reject, msg := policy(context.Background(), ev)
	if !reject || msg == "" {
		t.Fatal("debería rechazar un nombre de sala vacío")
	}
}

func TestRoomMetaPolicy_RejectsMissingName(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil))
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"admin", testAdminPubkey}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar si falta el tag name")
	}
}

func TestRoomMetaPolicy_RejectsInvalidAdminPubkey(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil))
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", "no-es-un-pubkey"}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar un admin que no es un pubkey hex válido")
	}
}

func TestRoomMetaPolicy_RejectsWithoutHiveLink(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil))
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar si el pubkey no tiene vinculación hive")
	}
}

func TestRoomMetaPolicy_AcceptsFirstCreationByLinkedAccount(t *testing.T) {
	events := []*nostr.Event{linkedEventFor("creador")}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil))
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}},
	}
	reject, msg := policy(context.Background(), ev)
	if reject {
		t.Fatalf("debería aceptar la primera creación de una sala nueva, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_AcceptsUpdateBySameOwner(t *testing.T) {
	events := []*nostr.Event{
		linkedEventFor("creador"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General viejo"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil))
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General nuevo"}, {"admin", testAdminPubkey}},
	}
	reject, msg := policy(context.Background(), ev)
	if reject {
		t.Fatalf("el dueño de la sala debería poder actualizar sus metadatos, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_RejectsClaimByDifferentPubkey(t *testing.T) {
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventFor("otro"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil))
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "otro",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Suplantada"}, {"admin", testAdminPubkey}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar que otro pubkey reclame un nombre de sala ya existente")
	}
}

func TestRoomMetaPolicy_PropagatesQueryError(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, errors.New("db down")))
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}},
	}
	reject, msg := policy(context.Background(), ev)
	if !reject || msg == "" {
		t.Fatal("debería rechazar y devolver un mensaje si falla la consulta al almacenamiento")
	}
}
