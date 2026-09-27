package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fiatjaf/eventstore/sqlite3"
	"github.com/fiatjaf/khatru"

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

	relay.RejectEvent = append(relay.RejectEvent,
		policies.NewHiveLinkPolicy(hiveClient),
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
