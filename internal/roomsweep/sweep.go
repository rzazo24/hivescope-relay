// Package roomsweep mantiene limpio el almacenamiento de salas y mensajes:
//
//  1. Borra las filas de metadatos de sala "superadas": el reemplazo NIP-33
//     del backend es por (pubkey, kind, d), así que cada pubkey que publica
//     una sala deja su propia fila; solo la más nueva (por created_at, id
//     mayor en empate) es la sala vigente. Sin esto, una fila vieja -- por
//     ejemplo una anterior a que existiera "expiration" -- sobrevive a
//     cualquier edición hecha desde otro navegador/dispositivo (otro pubkey)
//     y mantiene viva la sala para siempre.
//  2. Borra las filas vigentes cuya "expiration" (NIP-40) ya pasó. khatru
//     trae su propio barrido de NIP-40, pero en la práctica no es fiable
//     aquí: corre cada hora, solo rastrea lo publicado desde el último
//     arranque (y reinicia su cuenta atrás en cada despliegue) y se vio dejar
//     una sala caducada 20 h sin borrar. Este paquete lo aplica por su cuenta.
//  3. Borra los mensajes de chat (kind 9) huérfanos: los de salas que ya no
//     tienen una fila vigente.
//  4. Borra las reacciones (kind 7) huérfanas: las de salas que ya no existen
//     y las que apuntan a un mensaje que ya no está (borrado por su autor).
//
// Nada de esto sabe por qué desapareció una sala ni cuándo se envió cada
// mensaje: solo compara qué "t" tienen los mensajes contra qué salas siguen
// vivas, así una sala renovada nunca pierde mensajes por desincronización.
package roomsweep

import (
	"context"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip40"

	"github.com/rzazo24/hivescope-relay/internal/policies"
)

// QueryEventsFunc tiene la misma firma que los métodos QueryEvents de
// khatru/eventstore.
type QueryEventsFunc func(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)

// DeleteEventFunc tiene la misma firma que los métodos DeleteEvent de
// khatru/eventstore (y que relay.DeleteEvent en main.go).
type DeleteEventFunc func(ctx context.Context, evt *nostr.Event) error

// Result cuenta lo que borró un barrido.
type Result struct {
	Superseded int // filas de sala reemplazadas por una más nueva de otro pubkey
	Expired    int // salas vigentes cuya expiration ya pasó
	Messages   int // mensajes de salas que ya no existen
	Reactions  int // reacciones de salas que ya no existen o a mensajes que ya no existen
}

// Start corre un barrido al arrancar y luego cada interval hasta que ctx se
// cancela. Pensado para lanzarse en su propia goroutine desde main.go.
func Start(ctx context.Context, queryEvents QueryEventsFunc, deleteEvent DeleteEventFunc, interval time.Duration, onSwept func(Result, error)) {
	run := func() {
		res, err := SweepOnce(ctx, queryEvents, deleteEvent)
		if onSwept != nil {
			onSwept(res, err)
		}
	}
	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// SweepOnce hace un barrido completo (ver la doc del paquete).
func SweepOnce(ctx context.Context, queryEvents QueryEventsFunc, deleteEvent DeleteEventFunc) (Result, error) {
	return sweepAt(ctx, queryEvents, deleteEvent, nostr.Now())
}

func sweepAt(ctx context.Context, queryEvents QueryEventsFunc, deleteEvent DeleteEventFunc, now nostr.Timestamp) (Result, error) {
	var res Result

	ch, err := queryEvents(ctx, nostr.Filter{Kinds: []int{policies.AppDataKind}})
	if err != nil {
		return res, err
	}
	rowsByD := map[string][]*nostr.Event{}
	for ev := range ch {
		if ev.Kind != policies.AppDataKind {
			continue
		}
		d := ev.Tags.GetD()
		if slug, ok := strings.CutPrefix(d, policies.RoomMetaDTagPrefix); ok && slug != "" {
			rowsByD[d] = append(rowsByD[d], ev)
		}
	}

	live := map[string]bool{}
	for d, rows := range rowsByD {
		newest := newestOf(rows)
		for _, row := range rows {
			if row != newest && deleteEvent(ctx, row) == nil {
				res.Superseded++
			}
		}
		if exp := nip40.GetExpiration(newest.Tags); exp != -1 && exp <= now {
			if deleteEvent(ctx, newest) == nil {
				res.Expired++
			}
			continue
		}
		live[strings.TrimPrefix(d, policies.RoomMetaDTagPrefix)] = true
	}

	msgs, err := queryEvents(ctx, nostr.Filter{Kinds: []int{policies.ChatMessageKind}})
	if err != nil {
		return res, err
	}
	surviving := map[string]bool{} // ids de mensajes que siguen guardados
	for ev := range msgs {
		if ev.Kind != policies.ChatMessageKind {
			continue
		}
		room := ev.Tags.Find("t").Value()
		if room == "" || live[room] {
			surviving[ev.ID] = true
			continue
		}
		// un error al borrar un mensaje puntual no aborta el barrido: se
		// reintenta en el próximo ciclo.
		if deleteEvent(ctx, ev) == nil {
			res.Messages++
		} else {
			surviving[ev.ID] = true
		}
	}

	reactions, err := queryEvents(ctx, nostr.Filter{Kinds: []int{policies.ReactionKind}})
	if err != nil {
		return res, err
	}
	for ev := range reactions {
		if ev.Kind != policies.ReactionKind {
			continue
		}
		room := ev.Tags.Find("t").Value()
		target := ev.Tags.Find("e").Value()
		if room != "" && live[room] && surviving[target] {
			continue
		}
		if deleteEvent(ctx, ev) == nil {
			res.Reactions++
		}
	}

	return res, nil
}

// PruneSuperseded borra, para la sala con tag "d" == d, todas las filas menos
// la más nueva. Se llama justo después de aceptar una publicación de sala
// (ver main.go) para que la limpieza sea inmediata y no espere al barrido.
func PruneSuperseded(ctx context.Context, queryEvents QueryEventsFunc, deleteEvent DeleteEventFunc, d string) (int, error) {
	ch, err := queryEvents(ctx, nostr.Filter{
		Kinds: []int{policies.AppDataKind},
		Tags:  nostr.TagMap{"d": []string{d}},
	})
	if err != nil {
		return 0, err
	}
	var rows []*nostr.Event
	for ev := range ch {
		// el filtro de tags del backend sqlite compara subcadenas: revalidar.
		if ev.Kind == policies.AppDataKind && ev.Tags.GetD() == d {
			rows = append(rows, ev)
		}
	}
	if len(rows) < 2 {
		return 0, nil
	}
	newest := newestOf(rows)
	deleted := 0
	for _, row := range rows {
		if row != newest && deleteEvent(ctx, row) == nil {
			deleted++
		}
	}
	return deleted, nil
}

// newestOf devuelve la fila vigente: mayor created_at, y en empate el id
// mayor (mismo criterio que eventstore/sqlite3.ReplaceEvent).
func newestOf(rows []*nostr.Event) *nostr.Event {
	newest := rows[0]
	for _, r := range rows[1:] {
		if r.CreatedAt > newest.CreatedAt || (r.CreatedAt == newest.CreatedAt && r.ID > newest.ID) {
			newest = r
		}
	}
	return newest
}
