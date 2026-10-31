package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSampleSeedIsRepeatable(t *testing.T) {
	a := testApp(t)
	if err := a.seedSampleDataset(); err != nil {
		t.Fatal(err)
	}
	var tenants, profiles, events, campaigns, imports int
	for _, item := range []struct {
		query string
		out   *int
	}{
		{`SELECT COUNT(*) FROM tenants`, &tenants},
		{`SELECT COUNT(*) FROM profiles`, &profiles},
		{`SELECT COUNT(*) FROM events`, &events},
		{`SELECT COUNT(*) FROM campaigns`, &campaigns},
		{`SELECT COUNT(*) FROM imports`, &imports},
	} {
		if err := a.db.QueryRow(item.query).Scan(item.out); err != nil {
			t.Fatal(err)
		}
	}
	if tenants != 5 || profiles < 40 || events < 75 || campaigns != 3 || imports != 5 {
		t.Fatalf("unexpected demo size: tenants=%d profiles=%d events=%d campaigns=%d imports=%d", tenants, profiles, events, campaigns, imports)
	}
	var sofia, artem int64
	if err := a.db.QueryRow(`SELECT id FROM profiles WHERE display_name='София Лебедева'`).Scan(&sofia); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT id FROM profiles WHERE display_name='Артём Лебедев'`).Scan(&artem); err != nil {
		t.Fatal(err)
	}
	if sofia == artem {
		t.Fatal("family members were merged")
	}
	var cafePurchase int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM events e JOIN tenants t ON t.id=e.tenant_id WHERE e.profile_id=? AND t.name='Кафе Лист' AND e.kind='purchase'`, sofia).Scan(&cafePurchase); err != nil {
		t.Fatal(err)
	}
	if cafePurchase != 1 {
		t.Fatalf("Sofia's cafe conversion missing: %d", cafePurchase)
	}
	if err := a.seedSampleDataset(); err != nil {
		t.Fatal(err)
	}
	var again int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != events {
		t.Fatalf("repeat seed added events: %d -> %d", events, again)
	}
}

func TestSampleFileUpdatesCampaign(t *testing.T) {
	a := testApp(t)
	if err := a.seedSampleDataset(); err != nil {
		t.Fatal(err)
	}
	readOutcome := func() float64 {
		r := httptest.NewRecorder()
		a.overview(r, httptest.NewRequest("GET", "/api/overview", nil))
		var metrics map[string]float64
		if err := json.Unmarshal(r.Body.Bytes(), &metrics); err != nil {
			t.Fatal(err)
		}
		return metrics["first_purchases"]
	}
	before := readOutcome()
	file, err := os.ReadFile("testdata/sample-live-cafe.csv")
	if err != nil {
		t.Fatal(err)
	}
	file = []byte(strings.ReplaceAll(string(file), "2026-10-31", time.Now().UTC().Format("2006-01-02")))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("tenant_id", "2"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "sample-live-cafe.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(file); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	a.importFile(r, req)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"ok":1`) {
		t.Fatalf("live import failed: %d %s", r.Code, r.Body.String())
	}
	if after := readOutcome(); after != before+1 {
		t.Fatalf("outcome did not update: %.0f -> %.0f", before, after)
	}
}
