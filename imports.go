package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

func (a *App) imports(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	filter := int64(0)
	if raw := r.URL.Query().Get("tenant_id"); raw != "" {
		var e error
		filter, e = idParam(raw)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
	}
	rows, e := a.db.Query(`SELECT i.id,t.name,i.filename,i.rows_total,i.rows_ok,i.rows_failed,i.errors,i.created_at FROM imports i JOIN tenants t ON t.id=i.tenant_id WHERE (?=0 OR i.tenant_id=?) ORDER BY i.id DESC LIMIT 30`, filter, filter)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, total, ok, failed int
		var tenant, file, errs, date string
		if e = rows.Scan(&id, &tenant, &file, &total, &ok, &failed, &errs, &date); e != nil {
			fail(w, 500, e.Error())
			return
		}
		out = append(out, map[string]any{"id": id, "tenant": tenant, "filename": file, "total": total, "ok": ok, "failed": failed, "errors": errs, "date": date})
	}
	jsonOut(w, 200, out)
}

func (a *App) importFile(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if e := r.ParseMultipartForm(10 << 20); e != nil {
		fail(w, 400, "Файл слишком большой или форма некорректна")
		return
	}
	tenant, e := idParam(r.FormValue("tenant_id"))
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	file, header, e := r.FormFile("file")
	if e != nil {
		fail(w, 400, "Выберите CSV или XLSX")
		return
	}
	defer file.Close()
	var records [][]string
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == ".csv" {
		buffered := bufio.NewReader(file)
		preview, _ := buffered.Peek(4096)
		firstLine := strings.SplitN(string(preview), "\n", 2)[0]
		reader := csv.NewReader(buffered)
		if strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
			reader.Comma = ';'
		}
		records, e = reader.ReadAll()
	} else if ext == ".xlsx" {
		var f *excelize.File
		f, e = excelize.OpenReader(file)
		if e == nil {
			defer f.Close()
			sheets := f.GetSheetList()
			if len(sheets) == 0 {
				e = errors.New("Пустая книга")
			} else {
				records, e = f.GetRows(sheets[0])
			}
		}
	} else {
		fail(w, 400, "Поддерживаются только CSV и XLSX")
		return
	}
	if e != nil {
		fail(w, 400, "Не удалось прочитать файл: "+e.Error())
		return
	}
	if len(records) < 2 {
		fail(w, 400, "В файле нет строк с данными")
		return
	}
	headers := map[string]int{}
	for i, h := range records[0] {
		headers[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	for _, needed := range []string{"customer_id", "event_id", "event_type", "occurred_at"} {
		if _, ok := headers[needed]; !ok {
			fail(w, 400, "Не хватает колонки: "+needed)
			return
		}
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer tx.Rollback()
	okCount, failed := 0, 0
	errs := []string{}
	for i, row := range records[1:] {
		get := func(k string) string {
			j, ok := headers[k]
			if !ok || j >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[j])
		}
		amount := 0.0
		if s := get("amount"); s != "" {
			amount, e = strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
			if e != nil {
				failed++
				errs = append(errs, fmt.Sprintf("Строка %d: сумма не число", i+2))
				continue
			}
		}
		consent := strings.EqualFold(get("consent"), "true") || get("consent") == "1" || strings.EqualFold(get("consent"), "yes")
		input := EventInput{TenantID: tenant, CustomerID: get("customer_id"), EventID: get("event_id"), Name: get("name"), Phone: get("phone"), Email: get("email"), Consent: consent, Type: get("event_type"), OccurredAt: get("occurred_at"), Amount: amount, RelatedEventID: get("related_event_id")}
		if e = a.insertEvent(tx, input, "file:"+header.Filename); e != nil {
			failed++
			if len(errs) < 12 {
				errs = append(errs, fmt.Sprintf("Строка %d: %v", i+2, e))
			}
		} else {
			okCount++
		}
	}
	res, e := tx.Exec(`INSERT INTO imports(tenant_id,filename,rows_total,rows_ok,rows_failed,errors,created_at) VALUES(?,?,?,?,?,?,?)`, tenant, filepath.Base(header.Filename), len(records)-1, okCount, failed, strings.Join(errs, "; "), now())
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e.Error())
		return
	}
	id, _ := res.LastInsertId()
	jsonOut(w, 200, map[string]any{"id": id, "total": len(records) - 1, "ok": okCount, "failed": failed, "errors": errs})
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var v EventInput
	if e := decode(r, &v); e != nil {
		fail(w, 400, "Некорректный JSON: "+e.Error())
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer tx.Rollback()
	if e = a.insertEvent(tx, v, "api"); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e.Error())
		return
	}
	jsonOut(w, 201, map[string]string{"status": "created"})
}
