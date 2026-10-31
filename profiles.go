package main

import (
	"database/sql"
	"net/http"
	"strings"
)

func (a *App) tenants(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	rows, e := a.db.Query(`SELECT id,name,kind,color,(SELECT COUNT(DISTINCT profile_id) FROM source_customers WHERE tenant_id=t.id) FROM tenants t ORDER BY id`)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, n int
		var name, kind, color string
		if e = rows.Scan(&id, &name, &kind, &color, &n); e != nil {
			fail(w, 500, e.Error())
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "kind": kind, "color": color, "customers": n})
	}
	jsonOut(w, 200, out)
}

func (a *App) overview(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	q := func(s string) int { var n int; _ = a.db.QueryRow(s).Scan(&n); return n }
	total := q(`SELECT COUNT(*) FROM profiles`)
	events := q(`SELECT COUNT(*) FROM events`)
	tenants := q(`SELECT COUNT(*) FROM tenants`)
	campaigns := q(`SELECT COUNT(*) FROM campaigns`)
	first := q(`SELECT COUNT(DISTINCT cm.profile_id || ':' || c.target_tenant_id)
        FROM campaign_members cm JOIN campaigns c ON c.id=cm.campaign_id
        JOIN events e ON e.profile_id=cm.profile_id AND e.tenant_id=c.target_tenant_id
        WHERE e.kind='purchase' AND datetime(e.occurred_at)>=datetime(c.created_at)
          AND datetime(e.occurred_at)<=datetime(c.created_at,'+'||c.window_days||' days')
          AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund')
          AND NOT EXISTS (SELECT 1 FROM events old WHERE old.profile_id=cm.profile_id AND old.tenant_id=c.target_tenant_id AND old.kind='purchase' AND datetime(old.occurred_at)<datetime(c.created_at) AND NOT EXISTS (SELECT 1 FROM events rf WHERE rf.tenant_id=old.tenant_id AND rf.related_external_id=old.external_id AND rf.kind='refund'))`)
	jsonOut(w, 200, map[string]any{"profiles": total, "events": events, "tenants": tenants, "campaigns": campaigns, "first_purchases": first})
}

func (a *App) customers(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	term := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	rows, e := a.db.Query(`SELECT p.id,p.display_name,COALESCE(p.phone,''),COALESCE(p.email,''),p.consent,(SELECT COUNT(DISTINCT tenant_id) FROM source_customers WHERE profile_id=p.id),(SELECT COUNT(*) FROM events WHERE profile_id=p.id),COALESCE((SELECT SUM(CASE WHEN kind='purchase' THEN amount WHEN kind='refund' THEN -amount ELSE 0 END) FROM events WHERE profile_id=p.id),0) FROM profiles p WHERE p.display_name LIKE ? OR p.phone LIKE ? OR p.email LIKE ? ORDER BY p.id LIMIT 100`, term, term, term)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id, consent, tc, ec int
		var name, phone, email string
		var amount float64
		if e = rows.Scan(&id, &name, &phone, &email, &consent, &tc, &ec, &amount); e != nil {
			fail(w, 500, e.Error())
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "phone": phone, "email": email, "consent": consent == 1, "tenant_count": tc, "event_count": ec, "spend": amount})
	}
	jsonOut(w, 200, out)
}

func (a *App) customer(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	id, e := idParam(strings.TrimPrefix(r.URL.Path, "/api/customers/"))
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	var name, phone, email string
	var consent int
	e = a.db.QueryRow(`SELECT display_name,COALESCE(phone,''),COALESCE(email,''),consent FROM profiles WHERE id=?`, id).Scan(&name, &phone, &email, &consent)
	if e == sql.ErrNoRows {
		fail(w, 404, "Клиент не найден")
		return
	}
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	rows, e := a.db.Query(`SELECT e.id,t.name,t.color,e.kind,e.occurred_at,e.amount,e.source,e.external_id FROM events e JOIN tenants t ON t.id=e.tenant_id WHERE e.profile_id=? ORDER BY e.occurred_at DESC,e.id DESC`, id)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer rows.Close()
	events := []any{}
	for rows.Next() {
		var eid int
		var tenant, color, kind, date, source, external string
		var amount float64
		if e = rows.Scan(&eid, &tenant, &color, &kind, &date, &amount, &source, &external); e != nil {
			fail(w, 500, e.Error())
			return
		}
		events = append(events, map[string]any{"id": eid, "tenant": tenant, "color": color, "kind": kind, "date": date, "amount": amount, "source": source, "external_id": external})
	}
	src, e := a.db.Query(`SELECT t.name,sc.external_id,COALESCE(sc.name,''),COALESCE(sc.phone,''),COALESCE(sc.email,'') FROM source_customers sc JOIN tenants t ON t.id=sc.tenant_id WHERE sc.profile_id=?`, id)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer src.Close()
	sources := []any{}
	for src.Next() {
		var tenant, ext, n, p, em string
		if e = src.Scan(&tenant, &ext, &n, &p, &em); e != nil {
			fail(w, 500, e.Error())
			return
		}
		sources = append(sources, map[string]any{"tenant": tenant, "external_id": ext, "name": n, "phone": p, "email": em})
	}
	jsonOut(w, 200, map[string]any{"id": id, "name": name, "phone": phone, "email": email, "consent": consent == 1, "events": events, "sources": sources})
}
