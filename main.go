package main

import (
	"context" // set timers for database operations and define the length
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq" // ① PostgreSQL driver — the blank import registers the "postgres" driver
	//                          with database/sql. We never call lib/pq directly; the driver hooks
	//                          in automatically via its init() function.
)

// ─── Application ──────────────────────────────────────────────────────────────

// ② application holds the database connection pool.
//
// A *sql.DB is NOT a single connection — it is a managed pool that opens and
// closes connections automatically as demand fluctuates. You create it once at
// startup, store it here, and pass *application to every handler via the
// method receiver. This is the "dependency injection via struct" pattern used
// throughout this codebase.
type application struct {
	db *sql.DB
}

// GET /health
func (app *application) health(w http.ResponseWriter, r *http.Request) {

	// ⑧  Ping the database so the health check is genuinely meaningful.
	//     Same two-layer context pattern: r.Context() as parent, hard deadline on top.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	err := app.db.PingContext(ctx)
	dbStatus := "reachable"
	if err != nil {
		dbStatus = "unreachable: " + err.Error()
	}

	extra := http.Header{"Cache-Control": []string{"public, max-age=30"}}
	err = app.writeJSON(w, http.StatusOK, envelope{
		"status":    "available",
		"database":  dbStatus,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}, extra)
	if err != nil {
		app.serverError(w, err)
	}
}

// GET /headers
func (app *application) echoHeaders(w http.ResponseWriter, r *http.Request) {
	received := make(map[string]string, len(r.Header))
	for name, values := range r.Header {
		received[name] = strings.Join(values, ", ")
	}
	extra := http.Header{"X-Total-Headers": []string{strconv.Itoa(len(received))}}
	err := app.writeJSON(w, http.StatusOK, envelope{
		"headers_received": received,
		"count":            len(received),
	}, extra)
	if err != nil {
		app.serverError(w, err)
	}
}

// ─── Routes & main ────────────────────────────────────────────────────────────

func main() {

	// ⑨  Open the connection pool.
	//
	//     sql.Open does NOT open a real connection — it only validates the DSN
	//     and registers the pool. The first actual connection happens lazily
	//     when a query is executed. db.Ping() (or PingContext) forces that
	//     first connection immediately so we fail fast at startup rather than
	//     discovering a bad DSN on the first request.
	//
	//     DSN format for lib/pq:
	//       postgres://<user>:<password>@<host>:<port>/<dbname>?sslmode=disable
	//
	//     For a local development database with no password and SSL disabled:
	dsn := "postgres://university:password@localhost:5432/university?sslmode=disable"

	db, err := openDB(dsn)
	if err != nil {
		log.Fatalf("cannot open database: %v", err)
	}
	defer db.Close() // ⑨-b  Return all connections on shutdown.

	app := &application{db: db}

	mux := app.routes()

	log.Println("Starting server on :4000")
	log.Println()
	log.Println("  GET    /students")
	log.Println("  GET    /students/{id}")
	log.Println("  POST   /students")
	log.Println("  PUT    /students/{id}")
	log.Println("  DELETE /students/{id}")
	log.Println("  GET    /courses")
	log.Println("  GET    /health")
	log.Println("  GET    /headers")

	err = http.ListenAndServe(":4000", mux)
	log.Fatal(err)
}

// openDB encapsulates the boilerplate for building a *sql.DB and verifying it.
//
// ⑩  Connection pool configuration.
//
//	SetMaxOpenConns   — cap the total number of open connections.
//	                    Prevents overwhelming PostgreSQL when traffic spikes.
//	                    A common starting point is 25; tune for your hardware.
//
//	SetMaxIdleConns   — how many connections are kept open but unused.
//	                    Should be <= MaxOpenConns. Keeping some idle avoids the
//	                    latency of opening a new connection on every request.
//
//	SetConnMaxIdleTime — discard a connection that has been idle for longer
//	                     than this duration. Prefer this over SetConnMaxLifetime:
//	                     it reclaims connections that are genuinely unused rather
//	                     than ones that are merely old.
func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(15 * time.Minute)

	// Force the pool to open its first real connection, with a hard deadline.
	// Using context.WithTimeout here means the startup fails fast if PostgreSQL
	// is unreachable, rather than blocking indefinitely.
	// This is the same context discipline we apply to every query in the handlers.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}

	log.Println("Database connection pool established")
	return db, nil
}
