package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fiatjaf/eventstore/sqlite3"
	"github.com/fiatjaf/khatru"
	khatrupolicies "github.com/fiatjaf/khatru/policies"

	"github.com/rzazo24/hivescope-relay/internal/hiveapi"
	"github.com/rzazo24/hivescope-relay/internal/policies"
)

func main() {
	dbPath := getenv("HIVESCOPE_DB_PATH", "./data/hivescope-relay.sqlite")
	hiveNode := getenv("HIVESCOPE_HIVE_NODE", hiveapi.DefaultNode)
	addr := getenv("HIVESCOPE_LISTEN_ADDR", ":3334")

	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("no se pudo crear el directorio de datos %q: %v", dir, err)
		}
	}

	relay := khatru.NewRelay()
	relay.Info.Name = "hivescope-relay"
	relay.Info.Description = "Relé Nostr del chat descentralizado de HiveScope, con identidades vinculadas a cuentas Hive"

	db := sqlite3.SQLite3Backend{DatabaseURL: dbPath}
	if err := db.Init(); err != nil {
		log.Fatalf("no se pudo inicializar la base de datos sqlite en %q: %v", dbPath, err)
	}

	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	relay.QueryEvents = append(relay.QueryEvents, db.QueryEvents)
	relay.CountEvents = append(relay.CountEvents, db.CountEvents)
	relay.DeleteEvent = append(relay.DeleteEvent, db.DeleteEvent)
	relay.ReplaceEvent = append(relay.ReplaceEvent, db.ReplaceEvent)

	hiveClient := hiveapi.NewClient(hiveNode)

	// El límite de eventos por IP va primero: RejectEvent corta en la
	// primera política que rechace, así que si alguien satura el relé con
	// eventos de vinculación falsos, se lo frena acá antes de que dispare
	// las llamadas a la API de Hive (NewHiveLinkPolicy) o consultas a la
	// base (NewChatMessagePolicy/NewRoomMetaPolicy) por cada uno.
	relay.RejectEvent = append(relay.RejectEvent,
		khatrupolicies.EventIPRateLimiter(5, time.Minute, 20),
		policies.NewHiveLinkPolicy(hiveClient),
		policies.NewChatMessagePolicy(db.QueryEvents),
		policies.NewRoomMetaPolicy(db.QueryEvents),
		policies.NewAllowedEventsPolicy(),
	)

	relay.RejectFilter = append(relay.RejectFilter,
		khatrupolicies.FilterIPRateLimiter(20, time.Minute, 60),
	)

	relay.RejectConnection = append(relay.RejectConnection,
		khatrupolicies.ConnectionRateLimiter(10, time.Minute, 30),
	)

	fmt.Printf("hivescope-relay escuchando en %s (nodo hive: %s, db: %s)\n", addr, hiveNode, dbPath)
	if err := http.ListenAndServe(addr, relay); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
