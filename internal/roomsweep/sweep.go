// Package roomsweep borra los mensajes de chat (kind 9) que quedan
// "huérfanos" -- ya no tienen una sala viva a la que pertenecer -- una vez
// que el evento de metadatos de esa sala deja de existir, sea porque expiró
// (NIP-40, que khatru ya borra solo) o porque se borró a mano (NIP-09).
//
// No hace falta que este paquete sepa NADA sobre cuándo expira una sala ni
// por qué desapareció: solo compara qué "t" tags tienen los mensajes de chat
// guardados contra qué "d" tags de sala siguen existiendo ahora mismo, y
// borra los mensajes que apunten a una sala que ya no está. Esto es lo que
// lo hace "robusto" frente a que una sala se renueve (republicando su evento
// de metadatos con un "expiration" más lejano): mientras la sala exista, sus
// mensajes nunca se tocan, sin importar cuántas veces se haya renovado o
// cuándo se publicó cada mensaje individual.
package roomsweep

import (
	"context"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"github.com/rzazo24/hivescope-relay/internal/policies"
)

// QueryEventsFunc tiene la misma firma que policies.QueryEventsFunc (y que
// los métodos QueryEvents de khatru/eventstore) -- se redeclara acá para no
// atar este paquete a policies más de lo necesario.
type QueryEventsFunc func(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)

// DeleteEventFunc tiene la misma firma que los métodos DeleteEvent de
// khatru/eventstore (y que relay.DeleteEvent en main.go).
type DeleteEventFunc func(ctx context.Context, evt *nostr.Event) error

// Start corre SweepOnce cada interval hasta que ctx se cancela. Pensado para
// lanzarse en su propia goroutine desde main.go, igual que el
// expirationManager interno de khatru.
func Start(ctx context.Context, queryEvents QueryEventsFunc, deleteEvent DeleteEventFunc, interval time.Duration, onSwept func(deleted int, err error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := SweepOnce(ctx, queryEvents, deleteEvent)
			if onSwept != nil {
				onSwept(deleted, err)
			}
		}
	}
}

// SweepOnce hace un barrido: borra todo mensaje de chat (kind 9) cuyo tag
// "t" no coincida con el nombre de ninguna sala actualmente guardada.
// Devuelve cuántos mensajes borró.
func SweepOnce(ctx context.Context, queryEvents QueryEventsFunc, deleteEvent DeleteEventFunc) (int, error) {
	liveSlugs, err := liveRoomSlugs(ctx, queryEvents)
	if err != nil {
		return 0, err
	}

	ch, err := queryEvents(ctx, nostr.Filter{Kinds: []int{policies.ChatMessageKind}})
	if err != nil {
		return 0, err
	}

	deleted := 0
	for ev := range ch {
		room := ev.Tags.Find("t").Value()
		if room == "" || liveSlugs[room] {
			continue
		}
		if err := deleteEvent(ctx, ev); err == nil {
			deleted++
		}
		// un error al borrar un mensaje puntual no aborta el barrido entero
		// -- se reintenta solo en el próximo ciclo.
	}

	return deleted, nil
}

// liveRoomSlugs devuelve el conjunto de nombres de sala (sin el prefijo
// "room:") que tienen ahora mismo un evento de metadatos guardado, sin
// importar de qué autor ni si hay más de una fila para el mismo "d" (ver el
// comentario de findRoomOwnership en roommeta.go) -- para este barrido basta
// con que exista AL MENOS una, la sala sigue viva.
func liveRoomSlugs(ctx context.Context, queryEvents QueryEventsFunc) (map[string]bool, error) {
	ch, err := queryEvents(ctx, nostr.Filter{Kinds: []int{policies.AppDataKind}})
	if err != nil {
		return nil, err
	}

	slugs := map[string]bool{}
	for ev := range ch {
		d := ev.Tags.GetD()
		if slug, ok := strings.CutPrefix(d, policies.RoomMetaDTagPrefix); ok && slug != "" {
			slugs[slug] = true
		}
	}
	return slugs, nil
}
