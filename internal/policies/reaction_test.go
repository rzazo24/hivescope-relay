package policies

import (
	"context"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

var (
	msgID     = strings.Repeat("a", 64)
	msgAuthor = strings.Repeat("b", 64)
	reactor   = strings.Repeat("c", 64)
)

func storedMessage() *nostr.Event {
	return &nostr.Event{ID: msgID, Kind: ChatMessageKind, PubKey: msgAuthor, Tags: nostr.Tags{{"t", "general"}}}
}

func reaction(content string, tags nostr.Tags) *nostr.Event {
	return &nostr.Event{Kind: ReactionKind, PubKey: reactor, Content: content, Tags: tags}
}

func validReactionTags() nostr.Tags {
	return nostr.Tags{{"e", msgID}, {"p", msgAuthor}, {"t", "general"}}
}

func reactionPolicy(extra ...*nostr.Event) func(context.Context, *nostr.Event) (bool, string) {
	events := append([]*nostr.Event{linkedEventForAccount(reactor, "ana"), storedMessage()}, extra...)
	return NewReactionPolicy(fakeQueryEvents(events, nil))
}

func TestReactionPolicy_IgnoresOtherKinds(t *testing.T) {
	if reject, _ := reactionPolicy()(context.Background(), &nostr.Event{Kind: 1}); reject {
		t.Fatal("no debería evaluar otros kinds")
	}
}

func TestReactionPolicy_AcceptsEveryAllowedEmoji(t *testing.T) {
	for _, emoji := range ReactionEmojis {
		if reject, msg := reactionPolicy()(context.Background(), reaction(emoji, validReactionTags())); reject {
			t.Errorf("%s debería aceptarse: %s", emoji, msg)
		}
	}
}

func TestReactionPolicy_RejectsBadContentAndTags(t *testing.T) {
	bad := map[string]*nostr.Event{
		"texto libre":         reaction("hola", validReactionTags()),
		"vacío":               reaction("", validReactionTags()),
		"emoji fuera lista":   reaction("💩", validReactionTags()),
		"sin e":               reaction("👍", nostr.Tags{{"p", msgAuthor}, {"t", "general"}}),
		"sin p":               reaction("👍", nostr.Tags{{"e", msgID}, {"t", "general"}}),
		"sin t":               reaction("👍", nostr.Tags{{"e", msgID}, {"p", msgAuthor}}),
		"e no hex":            reaction("👍", nostr.Tags{{"e", "zz"}, {"p", msgAuthor}, {"t", "general"}}),
		"tag extra":           reaction("👍", append(validReactionTags(), nostr.Tag{"x", "y"})),
		"dos e":               reaction("👍", append(validReactionTags(), nostr.Tag{"e", msgID})),
		"sala inválida":       reaction("👍", nostr.Tags{{"e", msgID}, {"p", msgAuthor}, {"t", "Con Espacios"}}),
		"autor no coincide":   reaction("👍", nostr.Tags{{"e", msgID}, {"p", reactor}, {"t", "general"}}),
		"sala no coincide":    reaction("👍", nostr.Tags{{"e", msgID}, {"p", msgAuthor}, {"t", "otra"}}),
		"mensaje inexistente": reaction("👍", nostr.Tags{{"e", strings.Repeat("d", 64)}, {"p", msgAuthor}, {"t", "general"}}),
	}
	for name, ev := range bad {
		if reject, _ := reactionPolicy()(context.Background(), ev); !reject {
			t.Errorf("%s: debería rechazarse", name)
		}
	}
}

func TestReactionPolicy_RejectsUnlinkedPubkey(t *testing.T) {
	policy := NewReactionPolicy(fakeQueryEvents([]*nostr.Event{storedMessage()}, nil))
	if reject, _ := policy(context.Background(), reaction("👍", validReactionTags())); !reject {
		t.Fatal("sin vinculación no se puede reaccionar")
	}
}

func TestReactionPolicy_RejectsDuplicateButAllowsAnotherEmoji(t *testing.T) {
	existing := &nostr.Event{Kind: ReactionKind, PubKey: reactor, Content: "👍", Tags: validReactionTags()}
	policy := reactionPolicy(existing)
	if reject, _ := policy(context.Background(), reaction("👍", validReactionTags())); !reject {
		t.Fatal("la misma reacción dos veces debería rechazarse")
	}
	if reject, msg := policy(context.Background(), reaction("❤️", validReactionTags())); reject {
		t.Fatalf("otro emoji sí debería aceptarse: %s", msg)
	}
}

func TestReactionPolicy_QueryErrorRejects(t *testing.T) {
	policy := NewReactionPolicy(fakeQueryEvents(nil, context.DeadlineExceeded))
	if reject, _ := policy(context.Background(), reaction("👍", validReactionTags())); !reject {
		t.Fatal("un error de consulta debería rechazar")
	}
}
