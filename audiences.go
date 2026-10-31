package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
)

func (a *App) segments(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	src, e := idParam(r.URL.Query().Get("source"))
	if e != nil {
		fail(w, 400, "Укажите источник")
		return
	}
	target, e := idParam(r.URL.Query().Get("target"))
	if e != nil || src == target {
		fail(w, 400, "Укажите другого арендатора для перехода")
		return
	}
	profiles, e := a.segmentProfiles(src, target)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"source": src, "target": target, "count": len(profiles), "profiles": profiles})
}

func (a *App) segmentProfiles(src, target int64) ([]map[string]any, error) {
	rows, e := a.db.Query(`SELECT p.id,p.display_name,COALESCE(p.phone,''),COALESCE(p.email,'') FROM profiles p
        WHERE p.consent=1
          AND EXISTS(SELECT 1 FROM events e WHERE e.profile_id=p.id AND e.tenant_id=? AND e.kind IN ('purchase','visit','usage') AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund'))
          AND NOT EXISTS(SELECT 1 FROM events e WHERE e.profile_id=p.id AND e.tenant_id=? AND e.kind='purchase' AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund'))
        ORDER BY p.id`, src, target)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, phone, email string
		if e = rows.Scan(&id, &name, &phone, &email); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": id, "name": name, "phone": phone, "email": email})
	}
	return out, rows.Err()
}

func (a *App) campaigns(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, e := a.db.Query(`SELECT c.id,c.name,s.name,t.name,c.created_at,c.window_days,
          (SELECT COUNT(*) FROM campaign_members WHERE campaign_id=c.id),
          (SELECT COUNT(DISTINCT e.profile_id) FROM campaign_members cm JOIN events e ON e.profile_id=cm.profile_id AND e.tenant_id=c.target_tenant_id
           WHERE cm.campaign_id=c.id AND e.kind='purchase' AND datetime(e.occurred_at)>=datetime(c.created_at)
             AND datetime(e.occurred_at)<=datetime(c.created_at,'+'||c.window_days||' days')
             AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund')
             AND NOT EXISTS(SELECT 1 FROM events old WHERE old.profile_id=cm.profile_id AND old.tenant_id=c.target_tenant_id AND old.kind='purchase' AND datetime(old.occurred_at)<datetime(c.created_at) AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=old.tenant_id AND rf.related_external_id=old.external_id AND rf.kind='refund')))
          FROM campaigns c JOIN tenants s ON s.id=c.source_tenant_id JOIN tenants t ON t.id=c.target_tenant_id ORDER BY c.id DESC`)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer rows.Close()
		out := []any{}
		for rows.Next() {
			var id, window, members, converted int
			var name, source, target, date string
			if e = rows.Scan(&id, &name, &source, &target, &date, &window, &members, &converted); e != nil {
				fail(w, 500, e.Error())
				return
			}
			out = append(out, map[string]any{"id": id, "name": name, "source": source, "target": target, "date": date, "window_days": window, "members": members, "converted": converted})
		}
		jsonOut(w, 200, out)
		return
	}
	if !method(w, r, "POST") {
		return
	}
	var v struct {
		Name       string `json:"name"`
		Source     int64  `json:"source"`
		Target     int64  `json:"target"`
		WindowDays int    `json:"window_days"`
	}
	if e := decode(r, &v); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if v.Source < 1 || v.Target < 1 || v.Source == v.Target {
		fail(w, 400, "Выберите разных арендаторов")
		return
	}
	if v.WindowDays == 0 {
		v.WindowDays = 14
	}
	if v.WindowDays < 1 || v.WindowDays > 90 {
		fail(w, 400, "Окно должно быть от 1 до 90 дней")
		return
	}
	if strings.TrimSpace(v.Name) == "" {
		v.Name = "Новая аудитория"
	}
	profiles, e := a.segmentProfiles(v.Source, v.Target)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec(`INSERT INTO campaigns(name,source_tenant_id,target_tenant_id,created_at,window_days) VALUES(?,?,?,?,?)`, v.Name, v.Source, v.Target, now(), v.WindowDays)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	id, _ := res.LastInsertId()
	for _, p := range profiles {
		if _, e = tx.Exec(`INSERT INTO campaign_members(campaign_id,profile_id) VALUES(?,?)`, id, p["id"]); e != nil {
			fail(w, 500, e.Error())
			return
		}
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e.Error())
		return
	}
	jsonOut(w, 201, map[string]any{"id": id, "members": len(profiles)})
}

func (a *App) campaignDetail(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	s := strings.TrimPrefix(r.URL.Path, "/api/campaigns/")
	export := strings.HasSuffix(s, "/export.csv")
	s = strings.TrimSuffix(s, "/export.csv")
	id, e := idParam(s)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	var name, source, target, date string
	var window int
	e = a.db.QueryRow(`SELECT c.name,s.name,t.name,c.created_at,c.window_days FROM campaigns c JOIN tenants s ON s.id=c.source_tenant_id JOIN tenants t ON t.id=c.target_tenant_id WHERE c.id=?`, id).Scan(&name, &source, &target, &date, &window)
	if e == sql.ErrNoRows {
		fail(w, 404, "Кампания не найдена")
		return
	}
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	rows, e := a.db.Query(`SELECT p.id,p.display_name,COALESCE(p.phone,''),COALESCE(p.email,''),
        EXISTS(SELECT 1 FROM events e JOIN campaigns c ON c.id=?
          WHERE e.profile_id=p.id AND e.tenant_id=c.target_tenant_id AND e.kind='purchase'
            AND datetime(e.occurred_at)>=datetime(c.created_at)
            AND datetime(e.occurred_at)<=datetime(c.created_at,'+'||c.window_days||' days')
            AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund'))
        FROM campaign_members cm JOIN profiles p ON p.id=cm.profile_id WHERE cm.campaign_id=? ORDER BY p.id`, id, id)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer rows.Close()
	members := []map[string]any{}
	for rows.Next() {
		var pid, converted int
		var n, phone, email string
		if e = rows.Scan(&pid, &n, &phone, &email, &converted); e != nil {
			fail(w, 500, e.Error())
			return
		}
		members = append(members, map[string]any{"id": pid, "name": n, "phone": phone, "email": email, "converted": converted == 1})
	}
	if export {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kaspi-campaign-%d.csv"`, id))
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{"profile_id", "name", "phone", "email"})
		for _, m := range members {
			_ = writer.Write([]string{fmt.Sprint(m["id"]), csvSafe(m["name"].(string)), csvSafe(m["phone"].(string)), csvSafe(m["email"].(string))})
		}
		writer.Flush()
		return
	}
	jsonOut(w, 200, map[string]any{"id": id, "name": name, "source": source, "target": target, "date": date, "window_days": window, "members": members})
}
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}
