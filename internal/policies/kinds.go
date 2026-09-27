package policies

// AppDataKind es el kind 30078 (NIP-78, "application-specific data"),
// parametrizado reemplazable: un mismo (pubkey, kind, "d") solo guarda la
// última versión publicada. hivescope-relay reutiliza este único kind para
// dos propósitos distintos, distinguidos por el valor del tag "d":
//   - HiveLinkDTag ("hive-link"): vinculación de identidad Hive<->Nostr.
//   - el prefijo RoomMetaDTagPrefix ("room:"): metadatos de sala.
const AppDataKind = 30078
