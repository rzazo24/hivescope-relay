package policies

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/nbd-wtf/go-nostr"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// NewReactionPolicy valida las reacciones (kind 7, NIP-25):
//
//	content: uno de ReactionEmojis
//	tags:
//	  ["e", "<id del mensaje>"]     (64 hex; un mensaje kind 9 ya guardado)
//	  ["p", "<pubkey del autor>"]   (el autor de ese mensaje)
//	  ["t", "<sala>"]               (la sala del mensaje)
//
// Exige vinculación hive verificada (como el chat), que el mensaje exista y
// que sala y autor coincidan con los del mensaje (un cliente no puede colgar
// una reacción de una sala en un mensaje de otra), y que este pubkey no haya
// reaccionado ya con ese mismo emoji a ese mensaje (evita el relleno). Quitar
// la reacción es un borrado NIP-09, no un tipo de evento aparte.
func NewReactionPolicy(queryEvents QueryEventsFunc) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind != ReactionKind {
			return false, ""
		}
		if !slices.Contains(ReactionEmojis, event.Content) {
			return true, "invalid: reaction content must be one of the allowed emojis"
		}

		var target, author, room string
		for _, tag := range event.Tags {
			switch {
			case len(tag) == 2 && tag[0] == "e" && target == "" && hex64.MatchString(tag[1]):
				target = tag[1]
			case len(tag) == 2 && tag[0] == "p" && author == "" && hex64.MatchString(tag[1]):
				author = tag[1]
			case len(tag) == 2 && tag[0] == "t" && room == "" && presenceRoomSlug.MatchString(tag[1]):
				room = tag[1]
			default:
				return true, "invalid: reactions need exactly one \"e\" (message id), one \"p\" (its author) and one \"t\" (room) tag"
			}
		}
		if target == "" || author == "" || room == "" {
			return true, "invalid: reactions need exactly one \"e\" (message id), one \"p\" (its author) and one \"t\" (room) tag"
		}

		linked, err := hasVerifiedHiveLink(ctx, queryEvents, event.PubKey)
		if err != nil {
			return true, fmt.Sprintf("error: could not check this pubkey's hive link: %v", err)
		}
		if !linked {
			return true, "invalid: only linked hive accounts can react (missing kind 30078 d=hive-link event)"
		}

		// Se revalida en código lo que devuelve la consulta (id y kind) en vez de
		// fiarse de que el backend aplicó el filtro.
		found, err := queryEvents(ctx, nostr.Filter{IDs: []string{target}, Kinds: []int{ChatMessageKind}, Limit: 1})
		if err != nil {
			return true, fmt.Sprintf("error: could not look up the message: %v", err)
		}
		var msg *nostr.Event
		for ev := range found {
			if ev.ID == target && ev.Kind == ChatMessageKind {
				msg = ev
			}
		}
		if msg == nil {
			return true, "invalid: the message being reacted to doesn't exist"
		}
		if msg.PubKey != author || msg.Tags.Find("t").Value() != room {
			return true, "invalid: the \"p\" and \"t\" tags don't match the message being reacted to"
		}

		mine, err := queryEvents(ctx, nostr.Filter{Kinds: []int{ReactionKind}, Authors: []string{event.PubKey}, Tags: nostr.TagMap{"e": []string{target}}})
		if err != nil {
			return true, fmt.Sprintf("error: could not check existing reactions: %v", err)
		}
		dup := false
		for ev := range mine {
			if ev.Kind == ReactionKind && ev.Content == event.Content && ev.Tags.FindWithValue("e", target) != nil {
				dup = true
			}
		}
		if dup {
			return true, "duplicate: you already reacted to this message with that emoji"
		}
		return false, ""
	}
}

// SplitRateLimitKind aplica limitKind a los eventos de ese kind y limitOthers
// al resto. Sirve para darle su propio cupo por IP a un tipo de evento (las
// reacciones, más frecuentes que los mensajes) sin gastar el del chat.
func SplitRateLimitKind(
	kind int,
	limitKind, limitOthers func(ctx context.Context, event *nostr.Event) (bool, string),
) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind == kind {
			return limitKind(ctx, event)
		}
		return limitOthers(ctx, event)
	}
}
