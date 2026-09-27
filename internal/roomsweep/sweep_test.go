package roomsweep

import (
	"context"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"

	"github.com/rzazo24/hivescope-relay/internal/policies"
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

func fakeDeleteEvent(deleted *[]*nostr.Event) DeleteEventFunc {
	return func(ctx context.Context, evt *nostr.Event) error {
		*deleted = append(*deleted, evt)
		return nil
	}
}

func chatMessage(id, room string) *nostr.Event {
	return &nostr.Event{ID: id, Kind: policies.ChatMessageKind, Tags: nostr.Tags{{"t", room}}}
}

func roomMeta(dTag string) *nostr.Event {
	return &nostr.Event{Kind: policies.AppDataKind, Tags: nostr.Tags{{"d", dTag}}}
}

func TestSweepOnce_DeletesMessagesForRoomsThatNoLongerExist(t *testing.T) {
	events := []*nostr.Event{
		roomMeta("room:general"),
		chatMessage("keep", "general"),
		chatMessage("orphan", "borrada"), // no hay ningún room:borrada guardado
	}
	var deleted []*nostr.Event
	n, err := SweepOnce(context.Background(), fakeQueryEvents(events, nil), fakeDeleteEvent(&deleted))
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if n != 1 || len(deleted) != 1 || deleted[0].ID != "orphan" {
		t.Fatalf("debería borrar solo el mensaje huérfano, borró: %v", deleted)
	}
}

func TestSweepOnce_KeepsMessagesWhenRoomStillExists(t *testing.T) {
	events := []*nostr.Event{
		roomMeta("room:general"),
		chatMessage("a", "general"),
		chatMessage("b", "general"),
	}
	var deleted []*nostr.Event
	n, err := SweepOnce(context.Background(), fakeQueryEvents(events, nil), fakeDeleteEvent(&deleted))
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if n != 0 || len(deleted) != 0 {
		t.Fatalf("no debería borrar nada mientras la sala siga existiendo, borró: %v", deleted)
	}
}

func TestSweepOnce_IgnoresMessagesWithoutRoomTag(t *testing.T) {
	events := []*nostr.Event{
		{ID: "no-room-tag", Kind: policies.ChatMessageKind, Tags: nostr.Tags{}},
	}
	var deleted []*nostr.Event
	n, err := SweepOnce(context.Background(), fakeQueryEvents(events, nil), fakeDeleteEvent(&deleted))
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if n != 0 {
		t.Fatalf("un mensaje sin tag \"t\" no debería intentar borrarse, borró %d", n)
	}
}

func TestSweepOnce_IgnoresNonRoomAppData(t *testing.T) {
	// Un evento kind:30078 que no es de sala (por ejemplo hive-link) no
	// cuenta como "sala viva" para ningún "t".
	events := []*nostr.Event{
		{Kind: policies.AppDataKind, Tags: nostr.Tags{{"d", "hive-link"}}},
		chatMessage("orphan", "hive-link"),
	}
	var deleted []*nostr.Event
	n, err := SweepOnce(context.Background(), fakeQueryEvents(events, nil), fakeDeleteEvent(&deleted))
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if n != 1 {
		t.Fatalf("debería borrar el mensaje, un evento hive-link no cuenta como sala")
	}
}

func TestSweepOnce_PropagatesRoomQueryError(t *testing.T) {
	_, err := SweepOnce(context.Background(), fakeQueryEvents(nil, errors.New("db down")), fakeDeleteEvent(&[]*nostr.Event{}))
	if err == nil {
		t.Fatal("debería propagar el error de la consulta de salas")
	}
}

func TestSweepOnce_ContinuesAfterAFailedDelete(t *testing.T) {
	events := []*nostr.Event{
		chatMessage("a", "borrada"),
		chatMessage("b", "borrada"),
	}
	calls := 0
	deleteEvent := func(ctx context.Context, evt *nostr.Event) error {
		calls++
		if evt.ID == "a" {
			return errors.New("delete failed")
		}
		return nil
	}
	n, err := SweepOnce(context.Background(), fakeQueryEvents(events, nil), deleteEvent)
	if err != nil {
		t.Fatalf("no debería fallar el barrido entero por un borrado puntual fallido: %v", err)
	}
	if calls != 2 {
		t.Fatalf("debería intentar borrar los dos mensajes, intentó %d", calls)
	}
	if n != 1 {
		t.Fatalf("debería contar solo el borrado que sí funcionó, contó %d", n)
	}
}
