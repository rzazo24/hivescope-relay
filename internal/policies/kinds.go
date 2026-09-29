package policies

// AppDataKind es el kind 30078 (NIP-78, "application-specific data"),
// parametrizado reemplazable: un mismo (pubkey, kind, "d") solo guarda la
// última versión publicada. hivescope-relay reutiliza este único kind para
// dos propósitos distintos, distinguidos por el valor del tag "d":
//   - HiveLinkDTag ("hive-link"): vinculación de identidad Hive<->Nostr.
//   - el prefijo RoomMetaDTagPrefix ("room:"): metadatos de sala.
const AppDataKind = 30078

// PresenceKind es un kind EFÍMERO (20000-29999): el relé lo reenvía a los
// suscriptores pero no lo guarda. Es el "latido" de presencia: cada cliente
// vinculado lo publica cada pocos segundos indicando en qué sala está (tag
// "t"; sin tag = en la lista de salas) y los demás cuentan cuentas Hive
// distintas con un latido reciente. Un latido con el tag "left" retira la
// presencia al salir. Ver NewPresencePolicy.
const PresenceKind = 20078

// ReactionKind es el kind 7 (NIP-25, reacciones). Una reacción es un emoji de
// una lista cerrada (ReactionEmojis) sobre un mensaje de chat concreto. Quitar
// una reacción es un borrado NIP-09 de ese evento. Ver NewReactionPolicy.
const ReactionKind = 7

// ReactionEmojis son los únicos contenidos aceptados en una reacción. Cerrada
// a propósito: nada de texto libre en un evento que el cliente pinta tal cual.
// El frontend (REACTION_EMOJIS) usa exactamente los mismos; si cambias uno,
// cambia el otro.
var ReactionEmojis = []string{"👍", "❤️", "😂", "🎉", "😮", "😢"}
