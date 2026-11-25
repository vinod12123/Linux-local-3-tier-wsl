package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type application struct {
	db           *pgxpool.Pool
	controlToken string
}

type payment struct {
	ID           string    `json:"id"`
	CustomerName string    `json:"customerName"`
	AmountCents  int64     `json:"amountCents"`
	Currency     string    `json:"currency"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

type createPaymentRequest struct {
	CustomerName string `json:"customerName"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := http.Get("http://127.0.0.1:8080/health/live")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	databaseURL := requiredEnv("DATABASE_URL")
	ctx := context.Background()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("event=database_pool_failed error=%q", err)
	}
	defer db.Close()

	app := &application{db: db, controlToken: os.Getenv("LAB_CONTROL_TOKEN")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", app.live)
	mux.HandleFunc("GET /health/ready", app.ready)
	mux.HandleFunc("GET /api/payments", app.listPayments)
	mux.HandleFunc("POST /api/payments", app.createPayment)
	mux.HandleFunc("POST /api/lab/exception", app.labException)
	mux.HandleFunc("POST /api/lab/database-error", app.labDatabaseError)
	mux.HandleFunc("POST /api/lab/slow-query", app.labSlowQuery)
	mux.HandleFunc("POST /_lab/crash", app.crash)

	server := &http.Server{
		Addr:              ":" + envOr("PORT", "8080"),
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("event=server_started address=%s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("event=server_failed error=%q", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func (a *application) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "up", "service": "payment-api"})
}

func (a *application) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.db.Ping(ctx); err != nil {
		log.Printf("level=error event=database_unavailable error=%q", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "down", "dependency": "postgresql", "error": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "database": "up"})
}

func (a *application) listPayments(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	rows, err := a.db.Query(ctx, `SELECT id::text, customer_name, amount_cents, currency, status, created_at FROM payments ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		a.databaseError(w, "list_payments", err)
		return
	}
	defer rows.Close()
	items := make([]payment, 0)
	for rows.Next() {
		var p payment
		if err := rows.Scan(&p.ID, &p.CustomerName, &p.AmountCents, &p.Currency, &p.Status, &p.CreatedAt); err != nil {
			a.databaseError(w, "scan_payment", err)
			return
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		a.databaseError(w, "read_payments", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *application) createPayment(w http.ResponseWriter, r *http.Request) {
	var input createPaymentRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return
	}
	input.CustomerName = strings.TrimSpace(input.CustomerName)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.CustomerName == "" || len(input.CustomerName) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "customer name is required and must be at most 80 characters"})
		return
	}
	if input.Currency == "" {
		input.Currency = "USD"
	}
	if len(input.Currency) != 3 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "currency must contain three letters"})
		return
	}
	amountCents, err := parseAmount(input.Amount)
	if err != nil || amountCents <= 0 || amountCents > 100_000_000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount must be between 0.01 and 1,000,000.00"})
		return
	}

	p := payment{ID: newUUID(), CustomerName: input.CustomerName, AmountCents: amountCents, Currency: input.Currency, Status: "approved"}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err = a.db.QueryRow(ctx,
		`INSERT INTO payments (id, customer_name, amount_cents, currency, status) VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		p.ID, p.CustomerName, p.AmountCents, p.Currency, p.Status,
	).Scan(&p.CreatedAt)
	if err != nil {
		a.databaseError(w, "create_payment", err)
		return
	}
	log.Printf("level=info event=payment_created payment_id=%s amount_cents=%d currency=%s", p.ID, p.AmountCents, p.Currency)
	writeJSON(w, http.StatusCreated, p)
}

func (a *application) crash(w http.ResponseWriter, r *http.Request) {
	if a.controlToken == "" || r.Header.Get("X-Lab-Control-Token") != a.controlToken {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "payment-api will crash"})
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Printf("level=critical event=deliberate_lab_crash service=payment-api exit_code=17")
		os.Exit(17)
	}()
}

func (a *application) databaseError(w http.ResponseWriter, operation string, err error) {
	log.Printf("level=error event=database_operation_failed operation=%s error=%q", operation, err)
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database unavailable", "operation": operation})
}

func (a *application) labException(w http.ResponseWriter, r *http.Request) {
	if !allowLabRequest(w, r) {
		return
	}
	defer func() {
		if failure := recover(); failure != nil {
			log.Printf("level=error event=api_exception path=%s error=%q", r.URL.Path, failure)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "request handler panicked"})
		}
	}()
	panic("AstraPay lab: deliberate payment handler panic")
}

func (a *application) labDatabaseError(w http.ResponseWriter, r *http.Request) {
	if !allowLabRequest(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	if _, err := a.db.Exec(ctx, "SELECT 1 / 0"); err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "22012" {
			log.Printf("level=error event=database_query_failed sqlstate=%s error=%q", pgError.Code, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": pgError.Message, "sqlstate": pgError.Code})
			return
		}
		a.databaseError(w, "lab_query", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "query completed without an error"})
}

func (a *application) labSlowQuery(w http.ResponseWriter, r *http.Request) {
	if !allowLabRequest(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if _, err := a.db.Exec(ctx, "SELECT pg_sleep(2)"); err != nil {
		a.databaseError(w, "lab_slow_query", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "database query completed"})
}

func allowLabRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Lab-Action") == "1" {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "lab action header is required"})
	return false
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("event=http_request method=%s path=%s duration_ms=%d", r.Method, r.URL.Path, time.Since(started).Milliseconds())
	})
}

func parseAmount(value string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, fmt.Errorf("invalid amount")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, fmt.Errorf("invalid amount")
	}
	fraction := "00"
	if len(parts) == 2 {
		if len(parts[1]) == 1 {
			fraction = parts[1] + "0"
		} else if len(parts[1]) == 2 {
			fraction = parts[1]
		} else {
			return 0, fmt.Errorf("invalid amount")
		}
	}
	cents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, err
	}
	return whole*100 + cents, nil
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("required environment variable %s is missing", name)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
