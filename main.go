package main

import (
	"fmt"
	"log"
	"net"
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
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		runHealthcheck()
		return
	}

	dbPath := getenv("HIVESCOPE_DB_PATH", "./data/hivescope-relay.sqlite")
	hiveNode := getenv("HIVESCOPE_HIVE_NODE", hiveapi.DefaultNode)
	addr := getenv("HIVESCOPE_LISTEN_ADDR", ":3334")
	// Cuenta Hive (vacío = deshabilitado) que puede renombrar/editar
	// cualquier sala, no solo las que creó o administra -- ver el punto 4 del
	// comentario de NewRoomMetaPolicy.
	superadminHiveAccount := getenv("HIVESCOPE_SUPERADMIN_HIVE_ACCOUNT", "")

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
		policies.NewRoomMetaPolicy(db.QueryEvents, superadminHiveAccount),
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

// runHealthcheck se usa como HEALTHCHECK de Docker (ver docker-compose.yml):
// se invoca como `hivescope-relay --healthcheck` dentro del propio
// contenedor. La imagen final es debian-slim sin curl ni wget, así que en
// vez de instalar herramientas solo para esto, el propio binario se
// autochequea pegándole al endpoint NIP-11 de su propia instancia.
func runHealthcheck() {
	addr := getenv("HIVESCOPE_LISTEN_ADDR", ":3334")

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: HIVESCOPE_LISTEN_ADDR inválido (%q): %v\n", addr, err)
		os.Exit(1)
	}

	client := http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+"/", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Accept", "application/nostr+json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
