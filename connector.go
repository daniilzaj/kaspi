package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ConnectorConfig struct {
	TenantID   int64             `json:"tenant_id"`
	Name       string            `json:"name"`
	URL        string            `json:"url"`
	ArrayPath  string            `json:"array_path"`
	FieldMap   map[string]string `json:"field_map"`
	AuthHeader string            `json:"auth_header"`
	AuthValue  string            `json:"auth_value"`
}

func (c ConnectorConfig) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("укажите полный HTTP(S) URL без логина в адресе")
	}
	if c.TenantID < 1 || strings.TrimSpace(c.Name) == "" {
		return errors.New("выберите проект и укажите название подключения")
	}
	for _, key := range []string{"customer_id", "event_id", "event_type", "occurred_at"} {
		if strings.TrimSpace(c.FieldMap[key]) == "" {
			return fmt.Errorf("укажите путь для поля %s", key)
		}
	}
	if c.FieldMap["name"] == "" && c.FieldMap["phone"] == "" && c.FieldMap["email"] == "" {
		return errors.New("нужен путь к имени, телефону или e-mail")
	}
	for key, value := range c.FieldMap {
		if strings.HasPrefix(key, "type:") && (!validKind(value) || strings.TrimPrefix(key, "type:") == "") {
			return errors.New("некорректное сопоставление типа события")
		}
	}
	if c.AuthHeader != "" && (strings.ContainsAny(c.AuthHeader, " \r\n:") || strings.EqualFold(c.AuthHeader, "Host")) {
		return errors.New("некорректное имя заголовка авторизации")
	}
	return nil
}

func pathValue(v any, path string) any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	for _, part := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[part]
	}
	return v
}

func valueString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}

func mappedEvent(c ConnectorConfig, row any) (EventInput, error) {
	get := func(k string) string { return strings.TrimSpace(valueString(pathValue(row, c.FieldMap[k]))) }
	amount := 0.0
	if s := get("amount"); s != "" {
		var err error
		amount, err = strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		if err != nil {
			return EventInput{}, errors.New("сумма не является числом")
		}
	}
	consent := strings.ToLower(get("consent"))
	kind := strings.ToLower(get("event_type"))
	if mapped := c.FieldMap["type:"+kind]; mapped != "" {
		kind = mapped
	}
	return EventInput{
		TenantID: c.TenantID, CustomerID: get("customer_id"), EventID: get("event_id"),
		Name: get("name"), Phone: get("phone"), Email: get("email"),
		Type: kind, OccurredAt: get("occurred_at"), Amount: amount,
		Consent:        consent == "true" || consent == "1" || consent == "yes",
		RelatedEventID: get("related_event_id"),
	}, nil
}

func fetchConnector(ctx context.Context, c ConnectorConfig) ([]any, error) {
	client := &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("перенаправления API не поддерживаются")
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.AuthHeader != "" && c.AuthValue != "" {
		req.Header.Set(c.AuthHeader, c.AuthValue)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить данные: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("источник вернул HTTP %d", resp.StatusCode)
	}
	var payload any
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("источник вернул некорректный JSON или ответ больше 2 МБ")
	}
	if c.ArrayPath != "" {
		payload = pathValue(payload, c.ArrayPath)
	}
	rows, ok := payload.([]any)
	if !ok {
		return nil, errors.New("по указанному пути не найден массив событий")
	}
	if len(rows) > 1000 {
		return nil, errors.New("за один запрос принимается не более 1000 событий")
	}
	return rows, nil
}

func (a *App) testConnector(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var c ConnectorConfig
	if err := decode(r, &c); err != nil {
		fail(w, 400, "Некорректная настройка: "+err.Error())
		return
	}
	if err := c.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var tenant int
	if err := a.db.QueryRow("SELECT id FROM tenants WHERE id=?", c.TenantID).Scan(&tenant); err != nil {
		fail(w, 400, "проект не найден")
		return
	}
	rows, err := fetchConnector(r.Context(), c)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	preview := []EventInput{}
	issues := []string{}
	for i, row := range rows {
		ev, err := mappedEvent(c, row)
		if err == nil {
			if ev.CustomerID == "" || ev.EventID == "" || !validKind(ev.Type) {
				err = errors.New("нет ID клиента, события или корректного типа")
			} else if _, err = dateTime(ev.OccurredAt); err == nil && ev.Name == "" && ev.Phone == "" && ev.Email == "" {
				err = errors.New("нет имени, телефона или e-mail")
			} else if err == nil && ev.Amount < 0 {
				err = errors.New("сумма не может быть отрицательной")
			} else if err == nil && ev.Type == "refund" && ev.RelatedEventID == "" {
				err = errors.New("для возврата нужен ID исходной покупки")
			}
		}
		if err != nil && len(issues) < 5 {
			issues = append(issues, fmt.Sprintf("Строка %d: %v", i+1, err))
		}
		if err == nil && len(preview) < 3 {
			preview = append(preview, ev)
		}
	}
	jsonOut(w, 200, map[string]any{"total": len(rows), "preview": preview, "issues": issues, "valid": len(issues) == 0})
}

func (a *App) connectors(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		filter := int64(0)
		if raw := r.URL.Query().Get("tenant_id"); raw != "" {
			var e error
			filter, e = idParam(raw)
			if e != nil {
				fail(w, 400, e.Error())
				return
			}
		}
		rows, err := a.db.Query(`SELECT c.id,c.tenant_id,t.name,c.name,c.url,c.array_path,c.field_map,c.auth_header,c.last_sync_at,c.last_result FROM connectors c JOIN tenants t ON t.id=c.tenant_id WHERE (?=0 OR c.tenant_id=?) ORDER BY c.id DESC`, filter, filter)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		defer rows.Close()
		out := []any{}
		for rows.Next() {
			var id, tenant int64
			var tenantName, name, link, arrayPath, mapping, header, synced, result string
			if err := rows.Scan(&id, &tenant, &tenantName, &name, &link, &arrayPath, &mapping, &header, &synced, &result); err != nil {
				fail(w, 500, err.Error())
				return
			}
			var fields map[string]string
			_ = json.Unmarshal([]byte(mapping), &fields)
			out = append(out, map[string]any{"id": id, "tenant_id": tenant, "tenant": tenantName, "name": name, "url": link, "array_path": arrayPath, "field_map": fields, "auth_header": header, "has_auth": header != "", "last_sync_at": synced, "last_result": result})
		}
		jsonOut(w, 200, out)
		return
	}
	if !method(w, r, "POST") {
		return
	}
	var c ConnectorConfig
	if err := decode(r, &c); err != nil {
		fail(w, 400, "Некорректная настройка: "+err.Error())
		return
	}
	if err := c.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var tenant int
	if err := a.db.QueryRow("SELECT id FROM tenants WHERE id=?", c.TenantID).Scan(&tenant); err != nil {
		fail(w, 400, "проект не найден")
		return
	}
	fields, _ := json.Marshal(c.FieldMap)
	res, err := a.db.Exec(`INSERT INTO connectors(tenant_id,name,url,array_path,field_map,auth_header,auth_value,created_at) VALUES(?,?,?,?,?,?,?,?)`, c.TenantID, strings.TrimSpace(c.Name), c.URL, c.ArrayPath, string(fields), c.AuthHeader, c.AuthValue, now())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	jsonOut(w, 201, map[string]any{"id": id, "status": "saved"})
}

func (a *App) connectorAction(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	id, err := idParam(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/connectors/"), "/sync"))
	if err != nil || !strings.HasSuffix(r.URL.Path, "/sync") {
		fail(w, 400, "некорректный адрес подключения")
		return
	}
	var c ConnectorConfig
	var mapping string
	err = a.db.QueryRow(`SELECT tenant_id,name,url,array_path,field_map,auth_header,auth_value FROM connectors WHERE id=?`, id).Scan(&c.TenantID, &c.Name, &c.URL, &c.ArrayPath, &mapping, &c.AuthHeader, &c.AuthValue)
	if err == sql.ErrNoRows {
		fail(w, 404, "подключение не найдено")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := json.Unmarshal([]byte(mapping), &c.FieldMap); err != nil {
		fail(w, 500, "схема подключения повреждена")
		return
	}
	rows, err := fetchConnector(r.Context(), c)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	ok, failed := 0, 0
	issues := []string{}
	for i, row := range rows {
		ev, err := mappedEvent(c, row)
		if err == nil {
			err = a.insertEvent(tx, ev, fmt.Sprintf("connector:%d", id))
		}
		if err != nil {
			failed++
			if len(issues) < 5 {
				issues = append(issues, fmt.Sprintf("Строка %d: %v", i+1, err))
			}
		} else {
			ok++
		}
	}
	result := fmt.Sprintf("%d обработано · %d ошибок", ok, failed)
	if _, err = tx.Exec(`UPDATE connectors SET last_sync_at=?,last_result=? WHERE id=?`, now(), result, id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"total": len(rows), "ok": ok, "failed": failed, "issues": issues})
}
