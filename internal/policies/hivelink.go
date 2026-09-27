// Package policies contiene las reglas de aceptación/rechazo de eventos
// (khatru RejectEvent) específicas de hivescope-relay.
package policies

import (
	"context"
	"fmt"

	"github.com/nbd-wtf/go-nostr"

	"github.com/rzazo24/hivescope-relay/internal/hiveapi"
	"github.com/rzazo24/hivescope-relay/internal/hivecrypto"
)

// HiveLinkKind es el kind usado para vincular una cuenta Hive con un pubkey
// Nostr. Ver AppDataKind.
const HiveLinkKind = AppDataKind

// HiveLinkDTag es el valor del tag "d" que identifica a un evento de
// vinculación Hive<->Nostr dentro del kind 30078 (que también se usa, con
// otro "d", para metadatos de sala).
const HiveLinkDTag = "hive-link"

// LinkChallenge es el mensaje exacto que la cuenta Hive debe firmar (con su
// clave posting, por ejemplo con Hive Keychain's `requestSignBuffer`) para
// demostrar que controla nostrPubkey.
//
// El cliente que publica el evento de vinculación y esta política DEBEN estar
// de acuerdo en este formato exacto: cualquier cambio aquí invalida todas las
// vinculaciones ya firmadas por los usuarios.
func LinkChallenge(nostrPubkey string) string {
	return "hivescope-relay-link:" + nostrPubkey
}

// NewHiveLinkPolicy construye una política khatru RejectEvent que valida
// eventos de vinculación Hive<->Nostr con esta forma:
//
//	kind: 30078
//	tags:
//	  ["d", "hive-link"]
//	  ["hive_account", "<usuario_hive>"]
//	  ["hive_sig", "<firma_hex>"]
//	  ["hive_key_type", "posting"]   (opcional, por ahora solo se soporta "posting")
//
// La política consulta el nodo Hive configurado en hiveClient para obtener la
// clave pública "posting" real de hive_account, y verifica que hive_sig sea
// una firma válida de LinkChallenge(event.PubKey) hecha con esa clave.
//
// Eventos de otro kind, o eventos kind:30078 con un "d" distinto (por ejemplo
// metadatos de sala, "room:<sala>"), no son evaluados por esta política:
// devuelve (false, "") y deja que otras políticas decidan.
func NewHiveLinkPolicy(hiveClient *hiveapi.Client) func(ctx context.Context, event *nostr.Event) (bool, string) {
	return func(ctx context.Context, event *nostr.Event) (bool, string) {
		if event.Kind != HiveLinkKind {
			return false, ""
		}
		if event.Tags.GetD() != HiveLinkDTag {
			return false, ""
		}

		account := event.Tags.Find("hive_account").Value()
		sig := event.Tags.Find("hive_sig").Value()
		keyType := event.Tags.Find("hive_key_type").Value()

		if account == "" {
			return true, "invalid: falta el tag hive_account"
		}
		if sig == "" {
			return true, "invalid: falta el tag hive_sig"
		}
		if keyType == "" {
			keyType = "posting"
		}
		if keyType != "posting" {
			return true, "invalid: hive_key_type debe ser \"posting\" (todavía no se soportan otros tipos)"
		}

		postingKey, err := hiveClient.GetPostingPublicKey(ctx, account)
		if err != nil {
			return true, fmt.Sprintf("error: no se pudo verificar la cuenta hive %q: %v", account, err)
		}

		challenge := LinkChallenge(event.PubKey)
		valid, err := hivecrypto.VerifySignature([]byte(challenge), sig, postingKey)
		if err != nil {
			return true, fmt.Sprintf("invalid: hive_sig malformada: %v", err)
		}
		if !valid {
			return true, "invalid: hive_sig no corresponde a la clave posting de esa cuenta hive"
		}

		return false, ""
	}
}
