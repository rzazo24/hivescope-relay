package policies

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const testAdminPubkey = "1111111111111111111111111111111111111111111111111111111111111111"

// futureExpiration arma un tag "expiration" (NIP-40) válido para usar en los
// eventos de metadatos de sala que se someten a la política bajo prueba.
func futureExpiration() []string {
	return []string{"expiration", strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10)}
}

// linkedEventFor arma un evento hive-link de prueba reusando pubkey también
// como nombre de cuenta hive (no importa para la mayoría de los tests, que
// solo necesitan "esta identidad está vinculada"). Para tests que sí
// necesitan distinguir el pubkey firmante de la cuenta hive vinculada (por
// ejemplo, el superadmin), usar linkedEventForAccount.
func linkedEventFor(pubkey string) *nostr.Event {
	return linkedEventForAccount(pubkey, pubkey)
}

func linkedEventForAccount(pubkey, hiveAccount string) *nostr.Event {
	return &nostr.Event{
		Kind:   HiveLinkKind,
		PubKey: pubkey,
		Tags:   nostr.Tags{{"d", HiveLinkDTag}, {"hive_account", hiveAccount}},
	}
}

func TestRoomMetaPolicy_IgnoresOtherKinds(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	reject, _ := policy(context.Background(), &nostr.Event{Kind: 1})
	if reject {
		t.Fatal("no debería rechazar eventos de otro kind")
	}
}

func TestRoomMetaPolicy_IgnoresNonRoomDTag(t *testing.T) {
	// Un evento kind:30078 con otro "d" (por ejemplo hive-link) no es de esta política.
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{Kind: AppDataKind, Tags: nostr.Tags{{"d", HiveLinkDTag}}}
	reject, _ := policy(context.Background(), ev)
	if reject {
		t.Fatal("no debería rechazar un evento 30078 que no es de sala")
	}
}

func TestRoomMetaPolicy_RejectsEmptyRoomSlug(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{Kind: AppDataKind, Tags: nostr.Tags{{"d", "room:"}}}
	reject, msg := policy(context.Background(), ev)
	if !reject || msg == "" {
		t.Fatal("debería rechazar un nombre de sala vacío")
	}
}

func TestRoomMetaPolicy_RejectsMissingName(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar si falta el tag name")
	}
}

func TestRoomMetaPolicy_RejectsInvalidAdminPubkey(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", "no-es-un-pubkey"}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar un admin que no es un pubkey hex válido")
	}
}

func TestRoomMetaPolicy_RejectsMissingExpiration(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}},
	}
	reject, msg := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar una sala sin tag \"expiration\"")
	}
	if msg == "" {
		t.Fatal("debería devolver un motivo de rechazo")
	}
}

func TestRoomMetaPolicy_RejectsNonNumericExpiration(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}, {"expiration", "mañana"}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar un tag \"expiration\" que no es un timestamp unix")
	}
}

func TestRoomMetaPolicy_RejectsExpirationInThePast(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{
		Kind: AppDataKind,
		Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}, {"expiration", "1"}},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar un tag \"expiration\" que ya pasó")
	}
}

func TestRoomMetaPolicy_RejectsWithoutHiveLink(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, nil), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar si el pubkey no tiene vinculación hive")
	}
}

func TestRoomMetaPolicy_AcceptsFirstCreationByLinkedAccount(t *testing.T) {
	events := []*nostr.Event{linkedEventFor("creador")}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}, futureExpiration()},
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
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General nuevo"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, msg := policy(context.Background(), ev)
	if reject {
		t.Fatalf("el dueño de la sala debería poder actualizar sus metadatos, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_AcceptsUpdateByCurrentAdmin(t *testing.T) {
	// El "admin" delegado (distinto del creador original) también debería
	// poder actualizar el nombre/admin de la sala.
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventFor("delegado"),
		{
			Kind:   AppDataKind,
			PubKey: "creador",
			Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General viejo"}, {"admin", "delegado"}},
		},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "delegado",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General nuevo"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, msg := policy(context.Background(), ev)
	if reject {
		t.Fatalf("el admin delegado de la sala debería poder actualizar sus metadatos, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_ResolvesOwnershipFromNewestRowAcrossAuthors(t *testing.T) {
	// El reemplazo NIP-33 del backend es por (pubkey, kind, d): si el
	// delegado publica su propia actualización, queda una fila del creador
	// (vieja) y otra del delegado (nueva) con el mismo "d". La política debe
	// basar la autorización en la fila más nueva (por created_at), no en la
	// primera o última iterada del canal de resultados.
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventFor("delegado"),
		linkedEventFor("otro"),
		{
			ID:        "aa",
			Kind:      AppDataKind,
			PubKey:    "creador",
			CreatedAt: 100,
			Tags:      nostr.Tags{{"d", "room:general"}, {"name", "General viejo"}, {"admin", "delegado"}},
		},
		{
			ID:        "bb",
			Kind:      AppDataKind,
			PubKey:    "delegado",
			CreatedAt: 200,
			Tags:      nostr.Tags{{"d", "room:general"}, {"name", "General nuevo"}, {"admin", "delegado"}},
		},
	}

	// El creador original ya no es ni la fila vigente ni el admin vigente:
	// debería quedar afuera a partir de la delegación.
	rejectCreador, _ := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")(context.Background(), &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Reclamo viejo"}, {"admin", testAdminPubkey}, futureExpiration()},
	})
	if !rejectCreador {
		t.Fatal("el creador original debería perder el control tras delegar y ser superado por una fila más nueva")
	}

	// Un tercero sigue sin poder reclamarla.
	rejectOtro, _ := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")(context.Background(), &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "otro",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Suplantada"}, {"admin", testAdminPubkey}, futureExpiration()},
	})
	if !rejectOtro {
		t.Fatal("un tercero sin relación con la sala sigue sin poder reclamarla")
	}

	// El delegado, dueño de la fila vigente, sí puede seguir actualizando.
	rejectDelegado, msg := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")(context.Background(), &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "delegado",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General más nuevo"}, {"admin", testAdminPubkey}, futureExpiration()},
	})
	if rejectDelegado {
		t.Fatalf("el delegado, autor de la fila vigente, debería poder seguir actualizando, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_SuperadminCanUpdateAnyRoom(t *testing.T) {
	// "operador", cuya cuenta hive vinculada es "rzazo24" (el superadmin
	// configurado), no es ni dueño ni admin de la sala -- pero al ser el
	// superadmin debería poder actualizarla igual.
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventForAccount("operador", "rzazo24"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", "creador"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "rzazo24")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "operador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Renombrada por el superadmin"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, msg := policy(context.Background(), ev)
	if reject {
		t.Fatalf("el superadmin debería poder actualizar una sala que no le pertenece, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_SuperadminMatchIsCaseInsensitive(t *testing.T) {
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventForAccount("operador", "RzAzO24"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", "creador"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "rzazo24")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "operador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Renombrada"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, msg := policy(context.Background(), ev)
	if reject {
		t.Fatalf("la comparación de la cuenta superadmin debería ignorar mayúsculas/minúsculas, rechazado: %s", msg)
	}
}

func TestRoomMetaPolicy_NonSuperadminAccountStillRejectedForOthersRooms(t *testing.T) {
	// El superadmin está configurado como "rzazo24", pero quien firma este
	// evento tiene vinculada otra cuenta hive distinta -- no debería
	// beneficiarse del bypass.
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventForAccount("otro", "cuenta-cualquiera"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", "creador"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "rzazo24")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "otro",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Suplantada"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("una cuenta hive que no es la superadmin configurada no debería poder tocar salas ajenas")
	}
}

func TestRoomMetaPolicy_SuperadminDisabledWhenUnconfigured(t *testing.T) {
	// Con superadminHiveAccount == "" (valor por defecto, HIVESCOPE_SUPERADMIN_HIVE_ACCOUNT
	// sin configurar), ni siquiera la cuenta "rzazo24" tiene ningún privilegio especial.
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventForAccount("operador", "rzazo24"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", "creador"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "operador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Renombrada"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("sin superadminHiveAccount configurado, nadie debería tener el bypass, ni siquiera la cuenta que sería la superadmin")
	}
}

func TestRoomMetaPolicy_RejectsClaimByDifferentPubkey(t *testing.T) {
	events := []*nostr.Event{
		linkedEventFor("creador"),
		linkedEventFor("otro"),
		{Kind: AppDataKind, PubKey: "creador", Tags: nostr.Tags{{"d", "room:general"}, {"name", "General"}}},
	}
	policy := NewRoomMetaPolicy(fakeQueryEvents(events, nil), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "otro",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "Suplantada"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, _ := policy(context.Background(), ev)
	if !reject {
		t.Fatal("debería rechazar que otro pubkey reclame un nombre de sala ya existente")
	}
}

func TestRoomMetaPolicy_PropagatesQueryError(t *testing.T) {
	policy := NewRoomMetaPolicy(fakeQueryEvents(nil, errors.New("db down")), "")
	ev := &nostr.Event{
		Kind:   AppDataKind,
		PubKey: "creador",
		Tags:   nostr.Tags{{"d", "room:general"}, {"name", "General"}, {"admin", testAdminPubkey}, futureExpiration()},
	}
	reject, msg := policy(context.Background(), ev)
	if !reject || msg == "" {
		t.Fatal("debería rechazar y devolver un mensaje si falla la consulta al almacenamiento")
	}
}
