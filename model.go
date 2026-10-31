package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type EventInput struct {
	TenantID       int64   `json:"tenant_id"`
	CustomerID     string  `json:"customer_id"`
	EventID        string  `json:"event_id"`
	Name           string  `json:"name"`
	Phone          string  `json:"phone"`
	Email          string  `json:"email"`
	Consent        bool    `json:"consent"`
	Type           string  `json:"event_type"`
	OccurredAt     string  `json:"occurred_at"`
	Amount         float64 `json:"amount"`
	RelatedEventID string  `json:"related_event_id"`
}

func normalizePhone(s string) string {
	digits := strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
	if len(digits) == 11 && digits[0] == '8' {
		digits = "7" + digits[1:]
	}
	return digits
}
func normalizeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}
func dateTime(s string) (string, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if d, e := time.Parse(layout, s); e == nil {
			return d.UTC().Format(time.RFC3339), nil
		}
	}
	return "", errors.New("дата должна быть в формате YYYY-MM-DD или RFC3339")
}
func validKind(s string) bool {
	switch s {
	case "purchase", "visit", "booking", "topup", "usage", "refund":
		return true
	}
	return false
}

func (a *App) insertEvent(tx *sql.Tx, v EventInput, source string) error {
	v.Name = strings.TrimSpace(v.Name)
	v.Phone = normalizePhone(v.Phone)
	v.Email = strings.ToLower(strings.TrimSpace(v.Email))
	v.CustomerID = strings.TrimSpace(v.CustomerID)
	v.EventID = strings.TrimSpace(v.EventID)
	v.Type = strings.ToLower(strings.TrimSpace(v.Type))
	if v.TenantID < 1 || v.CustomerID == "" || v.EventID == "" {
		return errors.New("нужны tenant_id, customer_id и event_id")
	}
	if !validKind(v.Type) {
		return fmt.Errorf("неизвестный тип события: %s", v.Type)
	}
	if v.Name == "" && v.Phone == "" && v.Email == "" {
		return errors.New("нужно имя, телефон или e-mail")
	}
	if v.Amount < 0 {
		return errors.New("сумма не может быть отрицательной")
	}
	date, e := dateTime(v.OccurredAt)
	if e != nil {
		return e
	}
	var tenant int
	e = tx.QueryRow(`SELECT id FROM tenants WHERE id=?`, v.TenantID).Scan(&tenant)
	if e != nil {
		return errors.New("арендатор не найден")
	}
	var existing int
	var oldKind, oldDate string
	var oldAmount float64
	e = tx.QueryRow(`SELECT id,kind,occurred_at,amount FROM events WHERE tenant_id=? AND external_id=?`, v.TenantID, v.EventID).Scan(&existing, &oldKind, &oldDate, &oldAmount)
	if e == nil {
		date, parseErr := dateTime(v.OccurredAt)
		if parseErr == nil && oldKind == v.Type && oldDate == date && (oldAmount == v.Amount || v.Type == "refund" && v.Amount == 0) {
			return nil
		}
		return errors.New("event_id уже существует с другими данными")
	}
	if e != sql.ErrNoRows {
		return e
	}
	if v.Type == "refund" && v.RelatedEventID == "" {
		return errors.New("возврат должен ссылаться на покупку")
	}
	if v.Type == "refund" {
		var kind string
		var amount float64
		e = tx.QueryRow(`SELECT kind,amount FROM events WHERE tenant_id=? AND external_id=?`, v.TenantID, v.RelatedEventID).Scan(&kind, &amount)
		if e != nil || kind != "purchase" {
			return errors.New("исходная покупка для возврата не найдена")
		}
		if v.Amount == 0 {
			v.Amount = amount
		}
		if v.Amount != amount {
			return errors.New("полный возврат должен совпадать с суммой покупки")
		}
		var prior int
		e = tx.QueryRow(`SELECT COUNT(*) FROM events WHERE tenant_id=? AND kind='refund' AND related_external_id=?`, v.TenantID, v.RelatedEventID).Scan(&prior)
		if e != nil {
			return e
		}
		if prior > 0 {
			return errors.New("покупка уже возвращена")
		}
	}
	pid, e := resolveProfile(tx, v)
	if e != nil {
		return e
	}
	if v.Type == "refund" {
		var purchaseProfile int64
		if e = tx.QueryRow(`SELECT profile_id FROM events WHERE tenant_id=? AND external_id=?`, v.TenantID, v.RelatedEventID).Scan(&purchaseProfile); e != nil {
			return e
		}
		if pid != purchaseProfile {
			return errors.New("возврат относится к другому клиенту")
		}
	}
	_, e = tx.Exec(`INSERT INTO events(tenant_id,profile_id,external_id,kind,occurred_at,amount,related_external_id,source,imported_at) VALUES(?,?,?,?,?,?,?,?,?)`, v.TenantID, pid, v.EventID, v.Type, date, v.Amount, v.RelatedEventID, source, now())
	return e
}

// Identity matching is deliberately conservative: a shared phone with different
// names creates separate people. Source IDs and e-mail are stronger evidence.
func resolveProfile(tx *sql.Tx, v EventInput) (int64, error) {
	var pid int64
	matchMethod := "new_profile"
	confidence := 1.0
	e := tx.QueryRow(`SELECT profile_id FROM source_customers WHERE tenant_id=? AND external_id=?`, v.TenantID, v.CustomerID).Scan(&pid)
	if e == nil {
		if v.Consent {
			_, e = tx.Exec(`UPDATE profiles SET consent=1 WHERE id=?`, pid)
			if e != nil {
				return 0, e
			}
		}
		return pid, nil
	}
	if e != sql.ErrNoRows {
		return 0, e
	}
	if v.Email != "" {
		rows, e := tx.Query(`SELECT DISTINCT profile_id,name FROM source_customers WHERE email=?`, v.Email)
		if e != nil {
			return 0, e
		}
		ids := map[int64]bool{}
		for rows.Next() {
			var id int64
			var name sql.NullString
			if e = rows.Scan(&id, &name); e != nil {
				rows.Close()
				return 0, e
			}
			if v.Name == "" || name.String == "" || normalizeName(name.String) == normalizeName(v.Name) {
				ids[id] = true
			}
		}
		rows.Close()
		if len(ids) == 1 {
			for id := range ids {
				pid = id
			}
			matchMethod = "email"
			confidence = 0.95
		}
	}
	if pid == 0 && v.Phone != "" && v.Name != "" {
		rows, e := tx.Query(`SELECT DISTINCT profile_id,name FROM source_customers WHERE phone=?`, v.Phone)
		if e != nil {
			return 0, e
		}
		ids := map[int64]bool{}
		for rows.Next() {
			var id int64
			var name sql.NullString
			if e = rows.Scan(&id, &name); e != nil {
				rows.Close()
				return 0, e
			}
			if normalizeName(name.String) == normalizeName(v.Name) {
				ids[id] = true
			}
		}
		rows.Close()
		if len(ids) == 1 {
			for id := range ids {
				pid = id
			}
			matchMethod = "phone_and_name"
			confidence = 0.85
		}
	}
	if pid == 0 {
		res, e := tx.Exec(`INSERT INTO profiles(display_name,phone,email,consent,created_at) VALUES(?,?,?,?,?)`, fallbackName(v), v.Phone, v.Email, boolInt(v.Consent), now())
		if e != nil {
			return 0, e
		}
		pid, e = res.LastInsertId()
		if e != nil {
			return 0, e
		}
	} else {
		_, e = tx.Exec(`UPDATE profiles SET phone=CASE WHEN phone='' THEN ? ELSE phone END,email=CASE WHEN email='' THEN ? ELSE email END,consent=MAX(consent,?) WHERE id=?`, v.Phone, v.Email, boolInt(v.Consent), pid)
		if e != nil {
			return 0, e
		}
	}
	res, e := tx.Exec(`INSERT INTO source_customers(tenant_id,external_id,profile_id,name,phone,email) VALUES(?,?,?,?,?,?)`, v.TenantID, v.CustomerID, pid, v.Name, v.Phone, v.Email)
	if e != nil {
		return 0, e
	}
	sourceID, e := res.LastInsertId()
	if e != nil {
		return 0, e
	}
	_, e = tx.Exec(`INSERT INTO identity_decisions(source_customer_id,profile_id,method,confidence,created_at) VALUES(?,?,?,?,?)`, sourceID, pid, matchMethod, confidence, now())
	return pid, e
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func fallbackName(v EventInput) string {
	if v.Name != "" {
		return v.Name
	}
	if v.Email != "" {
		return v.Email
	}
	return "Клиент " + v.Phone
}
