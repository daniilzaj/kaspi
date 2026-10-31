package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/xuri/excelize/v2"
)

func testApp(t *testing.T) *App {
	t.Helper()
	db, e := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db")+"?_foreign_keys=on")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	a := &App{db}
	if e = a.migrate(); e != nil {
		t.Fatal(e)
	}
	if e = a.seed(); e != nil {
		t.Fatal(e)
	}
	return a
}
func TestFamilyPhoneAndTopup(t *testing.T) {
	a := testApp(t)
	var count int
	if e := a.db.QueryRow(`SELECT COUNT(*) FROM profiles WHERE phone='79139990011'`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 2 {
		t.Fatalf("shared phone merged two people: %d profiles", count)
	}
	var ann, pavel int64
	_ = a.db.QueryRow(`SELECT id FROM profiles WHERE display_name='Анна Смирнова'`).Scan(&ann)
	_ = a.db.QueryRow(`SELECT id FROM profiles WHERE display_name='Павел Смирнов'`).Scan(&pavel)
	if ann == pavel {
		t.Fatal("family members have same profile")
	}
	segment, e := a.segmentProfiles(1, 2)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range segment {
		if p["name"] == "Никита Громов" {
			t.Fatal("topup alone counted as visit")
		}
	}
}
func TestRefundAndIdempotence(t *testing.T) {
	a := testApp(t)
	v := EventInput{TenantID: 2, CustomerID: "new-1", EventID: "new-purchase", Name: "Мария Белова", Phone: "79132223344", Email: "maria@example.test", Consent: true, Type: "purchase", OccurredAt: time.Now().UTC().Format(time.RFC3339), Amount: 1200}
	tx, e := a.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.insertEvent(tx, v, "test"); e != nil {
		t.Fatal(e)
	}
	if e = a.insertEvent(tx, v, "test"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM events WHERE external_id='new-purchase'`).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate event inserted: %d", n)
	}
	v.EventID = "refund-1"
	v.Type = "refund"
	v.RelatedEventID = "new-purchase"
	v.Amount = 0
	tx, e = a.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.insertEvent(tx, v, "test"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	segment, e := a.segmentProfiles(1, 2)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, p := range segment {
		if p["name"] == "Мария Белова" {
			found = true
		}
	}
	if !found {
		t.Fatal("refunded purchase still excludes customer")
	}
}
func TestCampaignFirstPurchase(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRecorder()
	a.campaigns(r, httptest.NewRequest("POST", "/api/campaigns", strings.NewReader(`{"name":"Тест","source":1,"target":2}`)))
	if r.Code != 201 {
		t.Fatalf("create campaign: %d %s", r.Code, r.Body.String())
	}
	var created map[string]any
	if e := json.Unmarshal(r.Body.Bytes(), &created); e != nil {
		t.Fatal(e)
	}
	if created["members"].(float64) < 1 {
		t.Fatal("empty campaign")
	}
	v := EventInput{TenantID: 2, CustomerID: "cafe-999", EventID: "first-test", Name: "Мария Белова", Phone: "79132223344", Email: "maria@example.test", Consent: true, Type: "purchase", OccurredAt: time.Now().UTC().Format(time.RFC3339), Amount: 1000}
	tx, e := a.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.insertEvent(tx, v, "test"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRecorder()
	a.overview(r, httptest.NewRequest("GET", "/api/overview", nil))
	var overview map[string]any
	if e = json.Unmarshal(r.Body.Bytes(), &overview); e != nil {
		t.Fatal(e)
	}
	if overview["first_purchases"].(float64) != 1 {
		t.Fatalf("first purchase not counted: %s", r.Body.String())
	}
}

func TestCSVAndXLSXImport(t *testing.T) {
	a := testApp(t)
	headers := []string{"customer_id", "event_id", "event_type", "occurred_at", "name", "phone", "email", "amount", "consent"}
	values := []string{"demo-502", "demo-event-502", "purchase", "2026-10-30", "Тестовый Клиент", "+7 900 123-45-67", "test502@example.test", "750", "true"}
	var xlsx bytes.Buffer
	book := excelize.NewFile()
	for i, v := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = book.SetCellStr("Sheet1", cell, v)
	}
	for i, v := range values {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		_ = book.SetCellStr("Sheet1", cell, v)
	}
	if e := book.Write(&xlsx); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name string
		body []byte
	}{{"customers.xlsx", xlsx.Bytes()}, {"customers.csv", []byte(strings.Join(headers, ",") + "\n" + strings.Join(values, ",") + "\n")}} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if e := writer.WriteField("tenant_id", "2"); e != nil {
			t.Fatal(e)
		}
		part, e := writer.CreateFormFile("file", tc.name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = part.Write(tc.body); e != nil {
			t.Fatal(e)
		}
		if e = writer.Close(); e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequest("POST", "/api/import", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		r := httptest.NewRecorder()
		a.importFile(r, req)
		if r.Code != 200 {
			t.Fatalf("%s import: %d %s", tc.name, r.Code, r.Body.String())
		}
	}
	var count int
	if e := a.db.QueryRow(`SELECT COUNT(*) FROM events WHERE external_id='demo-event-502'`).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 1 {
		t.Fatalf("duplicate import produced %d events", count)
	}
}
