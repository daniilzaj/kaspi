package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed web/*
var assets embed.FS

type App struct{ db *sql.DB }

func main() {
	path := os.Getenv("KASPI_DB")
	if path == "" {
		path = "data/kaspi.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	app := &App{db: db}
	if err := app.migrate(); err != nil {
		log.Fatal(err)
	}
	if err := app.seed(); err != nil {
		log.Fatal(err)
	}
	if os.Getenv("KASPI_SEED_SAMPLE") == "1" {
		if err := app.seedSampleDataset(); err != nil {
			log.Fatal(err)
		}
		log.Print("Sample data ready")
		return
	}
	mux := http.NewServeMux()
	web, err := fs.Sub(assets, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(web)))
	mux.HandleFunc("/partner", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/partner" {
			http.NotFound(w, r)
			return
		}
		page, err := assets.ReadFile("web/partner.html")
		if err != nil {
			http.Error(w, "Портал недоступен", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/api/overview", app.overview)
	mux.HandleFunc("/api/tenants", app.tenants)
	mux.HandleFunc("/api/customers", app.customers)
	mux.HandleFunc("/api/customers/", app.customer)
	mux.HandleFunc("/api/imports", app.imports)
	mux.HandleFunc("/api/import", app.importFile)
	mux.HandleFunc("/api/events", app.events)
	mux.HandleFunc("/api/segments", app.segments)
	mux.HandleFunc("/api/campaigns", app.campaigns)
	mux.HandleFunc("/api/campaigns/", app.campaignDetail)
	mux.HandleFunc("/api/connectors", app.connectors)
	mux.HandleFunc("/api/connectors/test", app.testConnector)
	mux.HandleFunc("/api/connectors/", app.connectorAction)
	addr := os.Getenv("KASPI_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	log.Printf("Kaspi: http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, logRequests(mux)))
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]string{"error": msg})
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		fail(w, 405, "Метод не поддерживается")
		return false
	}
	return true
}
func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func idParam(s string) (int64, error) {
	id, e := strconv.ParseInt(strings.Trim(s, "/"), 10, 64)
	if e != nil || id < 1 {
		return 0, errors.New("Некорректный ID")
	}
	return id, nil
}
func now() string { return time.Now().UTC().Format(time.RFC3339) }
