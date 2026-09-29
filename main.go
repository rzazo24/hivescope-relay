package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fiatjaf/eventstore/sqlite3"
	"github.com/fiatjaf/khatru"
	khatrupolicies "github.com/fiatjaf/khatru/policies"
	"github.com/nbd-wtf/go-nostr"

	"github.com/rzazo24/hivescope-relay/internal/hiveapi"
	"github.com/rzazo24/hivescope-relay/internal/policies"
	"github.com/rzazo24/hivescope-relay/internal/roomsweep"
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

	// El backend limita por defecto cada consulta a 100 eventos y a 10 valores de
	// tag por filtro. Eso rompía cosas en silencio: la web pedía 200-5000 y recibía
	// 100, un `#t` con más de 10 salas fallaba, y roomsweep (que consulta "todos los
	// kind 30078 / kind 9") veía solo los 100 más nuevos -- con más de 100 filas
	// consideraría "muertas" salas vivas y borraría sus mensajes. Por eso el
	// límite interno (políticas y barrido, que usan db.QueryEvents directamente) es
	// enorme, y a los clientes se les limita aparte, más abajo.
	db := sqlite3.SQLite3Backend{DatabaseURL: dbPath, QueryLimit: 100000, QueryTagsLimit: 1000}
	if err := db.Init(); err != nil {
		log.Fatalf("no se pudo inicializar la base de datos sqlite en %q: %v", dbPath, err)
	}

	relay.StoreEvent = append(relay.StoreEvent, db.SaveEvent)
	// Consultas de clientes (REQ): como mucho clientQueryLimit eventos por filtro,
	// pidan lo que pidan (o nada). No afecta a las internas de arriba.
	relay.QueryEvents = append(relay.QueryEvents, func(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
		if filter.Limit < 1 || filter.Limit > clientQueryLimit {
			filter.Limit = clientQueryLimit
		}
		return db.QueryEvents(ctx, filter)
	})
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
		// Los latidos de presencia tienen su propio cupo por IP (más holgado)
		// para no gastar el del chat ni el de otras personas tras la misma IP.
		policies.SplitRateLimit(
			khatrupolicies.EventIPRateLimiter(30, time.Minute, 60),
			// Las reacciones también tienen su propio cupo: son más frecuentes
			// que los mensajes y no deben gastar el del chat.
			policies.SplitRateLimitKind(
				policies.ReactionKind,
				khatrupolicies.EventIPRateLimiter(20, time.Minute, 40),
				khatrupolicies.EventIPRateLimiter(5, time.Minute, 20),
			),
		),
		policies.NewHiveLinkPolicy(hiveClient),
		policies.NewChatMessagePolicy(db.QueryEvents),
		policies.NewReactionPolicy(db.QueryEvents),
		policies.NewRoomMetaPolicy(db.QueryEvents, superadminHiveAccount),
		policies.NewPresencePolicy(db.QueryEvents),
		policies.NewAllowedEventsPolicy(),
	)

	// Quién puede borrar un mensaje (NIP-09): el mismo pubkey, o cualquier otro
	// dispositivo vinculado a la misma cuenta Hive -- ver NewDeletionOutcome.
	relay.OverwriteDeletionOutcome = append(relay.OverwriteDeletionOutcome, policies.NewDeletionOutcome(db.QueryEvents))

	relay.RejectFilter = append(relay.RejectFilter,
		// Cada carga de la web hace ~10 suscripciones (REQ) por la conexión compartida:
		// con 20/min y ráfaga de 60, unas 6 recargas seguidas dejaban la app sin
		// lista de salas. Leer es barato; el límite solo frena abusos.
		khatrupolicies.FilterIPRateLimiter(60, time.Minute, 180),
	)

	relay.RejectConnection = append(relay.RejectConnection,
		khatrupolicies.ConnectionRateLimiter(10, time.Minute, 30),
	)

	// Tras aceptar una publicación de sala, borra las filas más viejas de esa
	// misma sala que quedaron de otros pubkeys (el reemplazo NIP-33 es por
	// autor) -- ver internal/roomsweep. Se registra DESPUÉS de db.ReplaceEvent
	// para que corra cuando ya está guardada la nueva.
	relay.ReplaceEvent = append(relay.ReplaceEvent, func(ctx context.Context, evt *nostr.Event) error {
		if evt.Kind == policies.AppDataKind && strings.HasPrefix(evt.Tags.GetD(), policies.RoomMetaDTagPrefix) {
			if n, err := roomsweep.PruneSuperseded(ctx, db.QueryEvents, db.DeleteEvent, evt.Tags.GetD()); err != nil {
				fmt.Printf("roomsweep: error limpiando filas viejas de %q: %v\n", evt.Tags.GetD(), err)
			} else if n > 0 {
				fmt.Printf("roomsweep: %d fila(s) vieja(s) de %q borrada(s)\n", n, evt.Tags.GetD())
			}
		}
		return nil
	})

	// Barrido al arrancar y cada 5 minutos: filas de sala superadas, salas
	// caducadas y mensajes huérfanos. Ver internal/roomsweep.
	go roomsweep.Start(context.Background(), db.QueryEvents, db.DeleteEvent, 5*time.Minute, func(r roomsweep.Result, err error) {
		if err != nil {
			fmt.Printf("roomsweep: error en el barrido: %v\n", err)
		} else if r.Superseded+r.Expired+r.Messages+r.Reactions > 0 {
			fmt.Printf("roomsweep: %d fila(s) superada(s), %d sala(s) caducada(s), %d mensaje(s) huérfano(s) y %d reacción(es) borrado(s)\n", r.Superseded, r.Expired, r.Messages, r.Reactions)
		}
	})

	fmt.Printf("hivescope-relay escuchando en %s (nodo hive: %s, db: %s)\n", addr, hiveNode, dbPath)
	if err := http.ListenAndServe(addr, relay); err != nil {
		log.Fatal(err)
	}
}

// clientQueryLimit es el máximo de eventos por filtro que se le devuelve a un cliente.
const clientQueryLimit = 2000

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
