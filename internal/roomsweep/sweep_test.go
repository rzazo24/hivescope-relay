package roomsweep

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/nbd-wtf/go-nostr"

	"github.com/rzazo24/hivescope-relay/internal/policies"
)

const now = nostr.Timestamp(1_000_000)

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

func fakeDeleteEvent(deleted *[]string) DeleteEventFunc {
	return func(ctx context.Context, evt *nostr.Event) error {
		*deleted = append(*deleted, evt.ID)
		return nil
	}
}

func chatMessage(id, room string) *nostr.Event {
	return &nostr.Event{ID: id, Kind: policies.ChatMessageKind, Tags: nostr.Tags{{"t", room}}}
}

// roomRow arma una fila de metadatos de sala; exp == 0 significa "sin expiration".
func roomRow(id, slug, pubkey string, createdAt nostr.Timestamp, exp nostr.Timestamp) *nostr.Event {
	tags := nostr.Tags{{"d", "room:" + slug}}
	if exp != 0 {
		tags = append(tags, nostr.Tag{"expiration", strconv.FormatInt(int64(exp), 10)})
	}
	return &nostr.Event{ID: id, Kind: policies.AppDataKind, PubKey: pubkey, CreatedAt: createdAt, Tags: tags}
}

func sweep(t *testing.T, events []*nostr.Event) (Result, []string) {
	t.Helper()
	var deleted []string
	res, err := sweepAt(context.Background(), fakeQueryEvents(events, nil), fakeDeleteEvent(&deleted), now)
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	return res, deleted
}

func TestSweep_DeletesMessagesForRoomsThatNoLongerExist(t *testing.T) {
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("r", "general", "a", 10, now+100),
		chatMessage("keep", "general"),
		chatMessage("orphan", "borrada"),
	})
	if res.Messages != 1 || len(deleted) != 1 || deleted[0] != "orphan" {
		t.Fatalf("debería borrar solo el mensaje huérfano, borró: %v", deleted)
	}
}

func TestSweep_KeepsEverythingWhileRoomIsAlive(t *testing.T) {
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("r", "general", "a", 10, now+100),
		chatMessage("a", "general"),
		chatMessage("b", "general"),
	})
	if len(deleted) != 0 || res != (Result{}) {
		t.Fatalf("no debería borrar nada, borró: %v (%+v)", deleted, res)
	}
}

func TestSweep_RoomWithoutExpirationIsImmortal(t *testing.T) {
	// salas creadas antes de que existiera "expiration"
	_, deleted := sweep(t, []*nostr.Event{roomRow("r", "vieja", "a", 10, 0), chatMessage("m", "vieja")})
	if len(deleted) != 0 {
		t.Fatalf("una sala sin expiration no debería borrarse, borró: %v", deleted)
	}
}

func TestSweep_DeletesExpiredRoomAndItsMessages(t *testing.T) {
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("r", "general", "a", 10, now-1),
		chatMessage("m", "general"),
	})
	if res.Expired != 1 || res.Messages != 1 || len(deleted) != 2 {
		t.Fatalf("debería borrar la sala caducada y su mensaje, borró: %v (%+v)", deleted, res)
	}
}

func TestSweep_ExpirationExactlyNowCountsAsExpired(t *testing.T) {
	res, _ := sweep(t, []*nostr.Event{roomRow("r", "general", "a", 10, now)})
	if res.Expired != 1 {
		t.Fatalf("expiration == ahora ya debería contar como caducada: %+v", res)
	}
}

func TestSweep_PrunesSupersededRowsFromOtherPubkeys(t *testing.T) {
	// El caso real: la fila original (otro pubkey) no tiene expiration; la
	// edición desde otro navegador publicó una fila nueva que sí. La vieja
	// tiene que desaparecer o mantendría viva la sala para siempre.
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("old", "general", "a", 10, 0),
		roomRow("new", "general", "b", 20, now+100),
		chatMessage("m", "general"),
	})
	if res.Superseded != 1 || len(deleted) != 1 || deleted[0] != "old" {
		t.Fatalf("debería borrar solo la fila vieja, borró: %v (%+v)", deleted, res)
	}
}

func TestSweep_ExpiredNewestRowKillsTheRoomEvenIfAnOlderRowNeverExpired(t *testing.T) {
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("old", "general", "a", 10, 0),
		roomRow("new", "general", "b", 20, now-5),
		chatMessage("m", "general"),
	})
	if res.Superseded != 1 || res.Expired != 1 || res.Messages != 1 || len(deleted) != 3 {
		t.Fatalf("la última edición manda: sala y mensajes deberían borrarse, borró: %v (%+v)", deleted, res)
	}
}

func TestSweep_RenewedRoomKeepsItsMessages(t *testing.T) {
	// Renovar = publicar una fila más nueva con expiration más lejana.
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("old", "general", "a", 10, now-5), // ya caducada, pero superada
		roomRow("new", "general", "a", 20, now+1000),
		chatMessage("m", "general"),
	})
	if res.Expired != 0 || res.Messages != 0 {
		t.Fatalf("una sala renovada no debería perder nada más que la fila vieja, borró: %v (%+v)", deleted, res)
	}
}

func TestSweep_TieOnCreatedAtKeepsHigherID(t *testing.T) {
	_, deleted := sweep(t, []*nostr.Event{
		roomRow("aa", "general", "a", 10, now+100),
		roomRow("bb", "general", "b", 10, now+100),
	})
	if len(deleted) != 1 || deleted[0] != "aa" {
		t.Fatalf("en empate debería quedarse el id mayor, borró: %v", deleted)
	}
}

func TestSweep_IgnoresMessagesWithoutRoomTag(t *testing.T) {
	res, _ := sweep(t, []*nostr.Event{{ID: "x", Kind: policies.ChatMessageKind, Tags: nostr.Tags{}}})
	if res.Messages != 0 {
		t.Fatalf("un mensaje sin tag \"t\" no debería borrarse")
	}
}

func TestSweep_IgnoresNonRoomAppData(t *testing.T) {
	// hive-link (mismo kind) ni cuenta como sala viva ni se toca.
	res, deleted := sweep(t, []*nostr.Event{
		{ID: "link", Kind: policies.AppDataKind, Tags: nostr.Tags{{"d", "hive-link"}}},
		chatMessage("orphan", "hive-link"),
	})
	if res.Messages != 1 || len(deleted) != 1 || deleted[0] != "orphan" {
		t.Fatalf("solo debería borrar el mensaje, borró: %v", deleted)
	}
}

func TestSweep_PropagatesQueryError(t *testing.T) {
	_, err := sweepAt(context.Background(), fakeQueryEvents(nil, errors.New("db down")), fakeDeleteEvent(&[]string{}), now)
	if err == nil {
		t.Fatal("debería propagar el error de la consulta")
	}
}

func TestSweep_ContinuesAfterAFailedDelete(t *testing.T) {
	calls := 0
	del := func(ctx context.Context, evt *nostr.Event) error {
		calls++
		if evt.ID == "a" {
			return errors.New("delete failed")
		}
		return nil
	}
	res, err := sweepAt(context.Background(), fakeQueryEvents([]*nostr.Event{chatMessage("a", "x"), chatMessage("b", "x")}, nil), del, now)
	if err != nil || calls != 2 || res.Messages != 1 {
		t.Fatalf("debería intentar los dos y contar solo el que funcionó: calls=%d res=%+v err=%v", calls, res, err)
	}
}

func TestPruneSuperseded_KeepsOnlyNewestRowOfThatRoom(t *testing.T) {
	var deleted []string
	n, err := PruneSuperseded(context.Background(), fakeQueryEvents([]*nostr.Event{
		roomRow("old", "general", "a", 10, 0),
		roomRow("new", "general", "b", 20, now+100),
		roomRow("other", "otra", "c", 5, 0), // otra sala: no se toca
	}, nil), fakeDeleteEvent(&deleted), "room:general")
	if err != nil || n != 1 || len(deleted) != 1 || deleted[0] != "old" {
		t.Fatalf("debería borrar solo la vieja de esa sala: n=%d deleted=%v err=%v", n, deleted, err)
	}
}

func TestPruneSuperseded_NoopWithASingleRow(t *testing.T) {
	var deleted []string
	n, _ := PruneSuperseded(context.Background(), fakeQueryEvents([]*nostr.Event{roomRow("r", "general", "a", 10, 0)}, nil), fakeDeleteEvent(&deleted), "room:general")
	if n != 0 || len(deleted) != 0 {
		t.Fatalf("con una sola fila no hay nada que podar")
	}
}

func reactionTo(id, msgID, room string) *nostr.Event {
	return &nostr.Event{ID: id, Kind: policies.ReactionKind, Tags: nostr.Tags{{"e", msgID}, {"t", room}}}
}

func TestSweep_DeletesOrphanReactions(t *testing.T) {
	res, deleted := sweep(t, []*nostr.Event{
		roomRow("r1", "viva", "p", now-10, now+100),
		chatMessage("m1", "viva"),
		reactionTo("ok", "m1", "viva"),
		reactionTo("sin-mensaje", "borrado", "viva"),  // el mensaje ya no existe
		reactionTo("sin-sala", "m9", "muerta"),        // la sala ya no existe
		{ID: "sin-tags", Kind: policies.ReactionKind}, // malformada
	})
	if res.Reactions != 3 {
		t.Fatalf("se esperaban 3 reacciones borradas, hubo %d (%v)", res.Reactions, deleted)
	}
	for _, id := range deleted {
		if id == "ok" {
			t.Fatal("la reacción válida no debe borrarse")
		}
	}
}

func TestSweep_ReactionsGoWhenTheirRoomExpires(t *testing.T) {
	res, _ := sweep(t, []*nostr.Event{
		roomRow("r1", "vieja", "p", now-100, now-1),
		chatMessage("m1", "vieja"),
		reactionTo("x", "m1", "vieja"),
	})
	if res.Messages != 1 || res.Reactions != 1 {
		t.Fatalf("mensaje y reacción deberían borrarse con la sala: %+v", res)
	}
}
