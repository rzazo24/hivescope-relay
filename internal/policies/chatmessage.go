package policies

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/nbd-wtf/go-nostr"
)

// ChatMessageKind es el kind usado para mensajes de chat efímeros (NIP-C7).
const ChatMessageKind = 9

// QueryEventsFunc tiene la misma firma que las funciones que khatru agrega a
// relay.QueryEvents (por ejemplo, el método SQLite3Backend.QueryEvents). Se
// recibe así, en vez de un *khatru.Relay o un backend concreto, para poder
// reutilizar el propio almacenamiento del relé sin acoplar esta política a
// una implementación de base de datos específica.
type QueryEventsFunc func(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)

// MaxChatMessageLength es el máximo de caracteres (runas) del contenido de un
// mensaje de chat. El frontend lo replica en su campo de texto
// (MAX_MESSAGE_LENGTH); si cambias uno, cambia el otro.
const MaxChatMessageLength = 2000

// NewChatMessagePolicy construye una política khatru RejectEvent para
// mensajes de chat (kind 9):
//
//	tags:
//	  ["t", "<nombre-sala>"]
//	  ["hive_account", "<usuario_hive>"]   (opcional, solo informativo)
//
// Solo se acepta el mensaje si el pubkey emisor ya tiene guardado un evento de
// vinculación (kind 30078, d=hive-link). Esa condición por sí sola certifica
// la vinculación: un evento así solo llega a guardarse si en su momento pasó
// NewHiveLinkPolicy, que verificó hive_sig contra la blockchain de Hive. Por
// eso aquí NO se vuelve a verificar ninguna firma, solo se comprueba que ese
// evento previo exista.
//
// Eventos de otro kind no son evaluados por esta política: devuelve
// (false, "") y deja que otras políticas decidan.
func NewChatMessagePolicy(queryEvents QueryEventsFunc) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind != ChatMessageKind {
			return false, ""
		}

		if event.Tags.Find("t").Value() == "" {
			return true, "invalid: missing \"t\" tag with the room name"
		}

		if n := utf8.RuneCountInString(event.Content); n > MaxChatMessageLength {
			return true, fmt.Sprintf("invalid: message too long (%d characters, the maximum is %d)", n, MaxChatMessageLength)
		}

		linked, err := hasVerifiedHiveLink(ctx, queryEvents, event.PubKey)
		if err != nil {
			return true, fmt.Sprintf("error: could not check this pubkey's hive link: %v", err)
		}
		if !linked {
			return true, "invalid: this pubkey hasn't linked a hive account yet (missing kind 30078 d=hive-link event)"
		}

		return false, ""
	}
}

// hasVerifiedHiveLink busca, entre los eventos ya guardados por el relé, un
// evento kind:30078 d=hive-link publicado por pubkey.
func hasVerifiedHiveLink(ctx context.Context, queryEvents QueryEventsFunc, pubkey string) (bool, error) {
	account, err := findLinkedHiveAccount(ctx, queryEvents, pubkey)
	if err != nil {
		return false, err
	}
	return account != "", nil
}

// findLinkedHiveAccount busca, entre los eventos ya guardados por el relé, un
// evento kind:30078 d=hive-link publicado por pubkey, y devuelve el valor de
// su tag "hive_account" (o "" si pubkey no tiene ninguna vinculación
// guardada).
//
// Se drena el canal por completo aunque ya se haya encontrado una
// coincidencia: el backend sqlite3 sigue escribiendo en el canal hasta que se
// lee todo o se cancela el contexto, así que cortar la iteración a mitad de
// camino dejaría una goroutine bloqueada (y una fila abierta en la base de
// datos) hasta que se cierre la conexión websocket.
func findLinkedHiveAccount(ctx context.Context, queryEvents QueryEventsFunc, pubkey string) (string, error) {
	filter := nostr.Filter{
		Kinds:   []int{HiveLinkKind},
		Authors: []string{pubkey},
	}

	ch, err := queryEvents(ctx, filter)
	if err != nil {
		return "", err
	}

	account := ""
	for ev := range ch {
		if ev.Tags.GetD() == HiveLinkDTag {
			account = ev.Tags.Find("hive_account").Value()
		}
	}
	return account, nil
}
