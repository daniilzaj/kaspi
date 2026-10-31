package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectorPreviewSaveAndSync(t *testing.T) {
	a := testApp(t)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "demo-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"events":[{"client":{"id":"ext-201","name":"Новый Клиент","phone":"+7 900 111-22-33"},"receipt":"sale-201","kind":"sale","when":"2026-10-31","total":1590}]}}`))
	}))
	defer source.Close()
	config := ConnectorConfig{
		TenantID: 2, Name: "Тестовая касса", URL: source.URL, ArrayPath: "data.events",
		AuthHeader: "X-API-Key", AuthValue: "demo-key",
		FieldMap: map[string]string{"customer_id": "client.id", "event_id": "receipt", "event_type": "kind", "occurred_at": "when", "name": "client.name", "phone": "client.phone", "amount": "total", "type:sale": "purchase"},
	}
	body, _ := json.Marshal(config)
	test := httptest.NewRecorder()
	a.testConnector(test, httptest.NewRequest("POST", "/api/connectors/test", strings.NewReader(string(body))))
	if test.Code != 200 || !strings.Contains(test.Body.String(), `"valid":true`) {
		t.Fatalf("preview failed: %d %s", test.Code, test.Body.String())
	}
	var count int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM events WHERE external_id='sale-201'").Scan(&count)
	if count != 0 {
		t.Fatal("preview imported data")
	}
	saved := httptest.NewRecorder()
	a.connectors(saved, httptest.NewRequest("POST", "/api/connectors", strings.NewReader(string(body))))
	if saved.Code != 201 {
		t.Fatalf("save failed: %d %s", saved.Code, saved.Body.String())
	}
	list := httptest.NewRecorder()
	a.connectors(list, httptest.NewRequest("GET", "/api/connectors", nil))
	if strings.Contains(list.Body.String(), "demo-key") {
		t.Fatal("list exposed API key")
	}
	for i := 0; i < 2; i++ {
		synced := httptest.NewRecorder()
		a.connectorAction(synced, httptest.NewRequest("POST", "/api/connectors/1/sync", nil))
		if synced.Code != 200 || !strings.Contains(synced.Body.String(), `"failed":0`) {
			t.Fatalf("sync failed: %d %s", synced.Code, synced.Body.String())
		}
	}
	_ = a.db.QueryRow("SELECT COUNT(*) FROM events WHERE external_id='sale-201'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected one event after repeated sync, got %d", count)
	}
}

func TestTenantFilteredHistory(t *testing.T) {
	a := testApp(t)
	for _, tenant := range []int{1, 2} {
		_, err := a.db.Exec(`INSERT INTO imports(tenant_id,filename,rows_total,rows_ok,rows_failed,errors,created_at) VALUES(?,?,?,?,?,?,?)`, tenant, "report.csv", 1, 1, 0, "", now())
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.db.Exec(`INSERT INTO connectors(tenant_id,name,url,array_path,field_map,created_at) VALUES(?,?,?,?,?,?)`, tenant, "API", "https://example.test/events", "", "{}", now())
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{{"imports", a.imports}, {"connectors", a.connectors}} {
		r := httptest.NewRecorder()
		test.handler(r, httptest.NewRequest("GET", "/api/"+test.name+"?tenant_id=1", nil))
		if r.Code != 200 {
			t.Fatalf("%s status: %d", test.name, r.Code)
		}
		var entries []map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("%s returned %d entries, want 1", test.name, len(entries))
		}
	}
}
