package policies

import (
	"context"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func chatEvent(pubkey string) *nostr.Event {
	return &nostr.Event{Kind: ChatMessageKind, PubKey: pubkey, Tags: nostr.Tags{{"t", "general"}}}
}

func deletionBy(pubkey string) *nostr.Event {
	return &nostr.Event{Kind: nostr.KindDeletion, PubKey: pubkey}
}

func TestDeletionOutcome_SamePubkeyAlwaysAllowed(t *testing.T) {
	outcome := NewDeletionOutcome(fakeQueryEvents(nil, nil))
	if ok, msg := outcome(context.Background(), chatEvent("ana"), deletionBy("ana")); !ok {
		t.Fatalf("el mismo pubkey debería poder borrar, rechazado: %s", msg)
	}
	// también para otros kinds, como khatru por defecto
	room := &nostr.Event{Kind: AppDataKind, PubKey: "ana"}
	if ok, _ := outcome(context.Background(), room, deletionBy("ana")); !ok {
		t.Fatal("el mismo pubkey debería poder borrar cualquier kind")
	}
}

func TestDeletionOutcome_AnotherDeviceOfTheSameHiveAccountCanDeleteMessages(t *testing.T) {
	events := []*nostr.Event{linkedEventForAccount("movil", "ana"), linkedEventForAccount("pc", "Ana")}
	outcome := NewDeletionOutcome(fakeQueryEvents(events, nil))
	if ok, msg := outcome(context.Background(), chatEvent("movil"), deletionBy("pc")); !ok {
		t.Fatalf("otro dispositivo de la misma cuenta debería poder borrar el mensaje, rechazado: %s", msg)
	}
}

func TestDeletionOutcome_DifferentHiveAccountIsRefused(t *testing.T) {
	events := []*nostr.Event{linkedEventForAccount("movil", "ana"), linkedEventForAccount("intruso", "carla")}
	outcome := NewDeletionOutcome(fakeQueryEvents(events, nil))
	if ok, _ := outcome(context.Background(), chatEvent("movil"), deletionBy("intruso")); ok {
		t.Fatal("otra cuenta Hive no debería poder borrar")
	}
}

func TestDeletionOutcome_UnlinkedRequesterOrAuthorIsRefused(t *testing.T) {
	events := []*nostr.Event{linkedEventForAccount("movil", "ana")}
	outcome := NewDeletionOutcome(fakeQueryEvents(events, nil))
	if ok, _ := outcome(context.Background(), chatEvent("movil"), deletionBy("sin-vincular")); ok {
		t.Fatal("un pubkey sin vinculación no debería poder borrar mensajes ajenos")
	}
	if ok, _ := outcome(context.Background(), chatEvent("autor-sin-vincular"), deletionBy("movil")); ok {
		t.Fatal("si el autor no tiene cuenta vinculada no hay cuenta que compartir")
	}
}

func TestDeletionOutcome_OnlyChatMessagesGetTheAccountRule(t *testing.T) {
	// Un dispositivo no debe poder borrar la vinculación ni la sala de otro
	// dispositivo de la misma cuenta.
	events := []*nostr.Event{linkedEventForAccount("movil", "ana"), linkedEventForAccount("pc", "ana")}
	outcome := NewDeletionOutcome(fakeQueryEvents(events, nil))
	link := &nostr.Event{Kind: HiveLinkKind, PubKey: "movil", Tags: nostr.Tags{{"d", HiveLinkDTag}}}
	if ok, _ := outcome(context.Background(), link, deletionBy("pc")); ok {
		t.Fatal("no debería poder borrar la vinculación de otro dispositivo")
	}
}

func TestDeletionOutcome_QueryErrorRefuses(t *testing.T) {
	outcome := NewDeletionOutcome(fakeQueryEvents(nil, errors.New("db down")))
	if ok, _ := outcome(context.Background(), chatEvent("movil"), deletionBy("pc")); ok {
		t.Fatal("ante un error de consulta hay que negar, no permitir")
	}
}

func TestDeletionOutcome_AnotherDeviceOfTheSameAccountCanRemoveAReaction(t *testing.T) {
	events := []*nostr.Event{linkedEventForAccount("movil", "ana"), linkedEventForAccount("pc", "ana")}
	outcome := NewDeletionOutcome(fakeQueryEvents(events, nil))
	react := &nostr.Event{Kind: ReactionKind, PubKey: "movil", Content: "👍"}
	if ok, msg := outcome(context.Background(), react, deletionBy("pc")); !ok {
		t.Fatalf("debería poder quitar la reacción: %s", msg)
	}
}
