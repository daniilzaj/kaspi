package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// seedSampleDataset adds a repeatable synthetic dataset to the local database.
// It is opt-in and marks itself complete in the same transaction.
func (a *App) seedSampleDataset() error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var seeded int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM demo_seed_state WHERE version=1`).Scan(&seeded); err != nil {
		return err
	}
	if seeded > 0 {
		return nil
	}

	tenantDefs := []struct{ name, kind, color, slug string }{
		{"Киберклуб Арена", "Развлечения", "#5574d8", "arena"},
		{"Кафе Лист", "Кафе и рестораны", "#df7858", "list"},
		{"Кинотеатр Атлас", "Кино", "#24a58f", "atlas"},
		{"Книжный Буква", "Книги", "#8b6abd", "bukva"},
		{"Фитнес Тонус", "Спорт", "#4d9c70", "tonus"},
	}
	ids := make([]int64, len(tenantDefs))
	files := map[int64]string{}
	counts := map[int64]int{}
	anchor := time.Now().UTC().Truncate(24 * time.Hour)
	for i, tenant := range tenantDefs {
		err := tx.QueryRow(`SELECT id FROM tenants WHERE name=? ORDER BY id LIMIT 1`, tenant.name).Scan(&ids[i])
		if err == sql.ErrNoRows {
			res, e := tx.Exec(`INSERT INTO tenants(name,kind,color) VALUES(?,?,?)`, tenant.name, tenant.kind, tenant.color)
			if e != nil {
				return e
			}
			ids[i], err = res.LastInsertId()
		}
		if err != nil {
			return err
		}
		files[ids[i]] = fmt.Sprintf("%s_%s.csv", tenant.slug, anchor.Format("2006_01"))
	}
	day := func(offset int) string { return anchor.AddDate(0, 0, offset).Format("2006-01-02") }
	addFileEvent := func(event EventInput) error {
		if err := a.insertEvent(tx, event, "file:"+files[event.TenantID]); err != nil {
			return err
		}
		counts[event.TenantID]++
		return nil
	}

	names := []string{
		"Анастасия Федорова", "Максим Кузнецов", "Виктория Новикова", "Артём Васильев",
		"Полина Миронова", "Даниил Соколов", "Екатерина Зайцева", "Кирилл Павлов",
		"Алина Романова", "Михаил Крылов", "Юлия Ковалева", "Николай Савельев",
		"Ксения Тихонова", "Матвей Осипов", "Вероника Егорова", "Роман Беляев",
		"Валерия Попова", "Тимур Волков", "Дарья Макарова", "Андрей Жуков",
		"Милана Алексеева", "Степан Громов", "Марина Сергеева", "Георгий Орлов",
		"Ева Фролова", "Пётр Лазарев", "Светлана Белова", "Лев Ильин",
		"Надежда Тарасова", "Семён Баранов", "Ирина Фомина", "Богдан Мельников",
	}
	for i, name := range names {
		phone := fmt.Sprintf("7900200%04d", i+1)
		email := fmt.Sprintf("demo-%02d@example.test", i+1)
		consent := i%9 != 0
		base := ids[i%len(ids)]
		customerID := func(tenant int64) string { return fmt.Sprintf("sample-%02d-%d", i+1, tenant) }
		kind := "visit"
		amount := 0.0
		if i%4 == 0 {
			kind, amount = "purchase", float64(690+(i%7)*210)
		}
		if err := addFileEvent(EventInput{TenantID: base, CustomerID: customerID(base), EventID: fmt.Sprintf("sample-base-%02d", i+1), Name: name, Phone: phone, Email: email, Consent: consent, Type: kind, OccurredAt: day(-18 - i%8), Amount: amount}); err != nil {
			return err
		}
		if i%2 == 0 {
			next := ids[(i+1)%len(ids)]
			nextKind, nextAmount := "visit", 0.0
			if i%6 == 0 {
				nextKind, nextAmount = "purchase", float64(460+(i%5)*180)
			}
			if err := addFileEvent(EventInput{TenantID: next, CustomerID: customerID(next), EventID: fmt.Sprintf("sample-second-%02d", i+1), Name: name, Phone: phone, Email: email, Consent: consent, Type: nextKind, OccurredAt: day(-11 - i%4), Amount: nextAmount}); err != nil {
				return err
			}
		}
		if i%3 == 0 {
			if err := addFileEvent(EventInput{TenantID: base, CustomerID: customerID(base), EventID: fmt.Sprintf("sample-repeat-%02d", i+1), Name: name, Phone: phone, Email: email, Consent: consent, Type: "usage", OccurredAt: day(-9), Amount: float64(380 + (i%4)*120)}); err != nil {
				return err
			}
		}
		if i%7 == 0 {
			third := ids[(i+2)%len(ids)]
			if err := addFileEvent(EventInput{TenantID: third, CustomerID: customerID(third), EventID: fmt.Sprintf("sample-third-%02d", i+1), Name: name, Phone: phone, Email: email, Consent: consent, Type: "purchase", OccurredAt: day(-8), Amount: float64(790 + i*30)}); err != nil {
				return err
			}
		}
	}

	// The hero story is intentionally easy to find in the demo.
	sofia := EventInput{CustomerID: "sample-sofia", Name: "София Лебедева", Phone: "79009000001", Email: "sofia@example.test", Consent: true}
	for _, event := range []struct {
		tenant   int64
		id, kind string
		offset   int
		amount   float64
	}{
		{ids[0], "sample-sofia-club", "visit", -13, 0},
		{ids[2], "sample-sofia-cinema", "purchase", -10, 820},
	} {
		v := sofia
		v.TenantID, v.EventID, v.Type, v.OccurredAt, v.Amount = event.tenant, event.id, event.kind, day(event.offset), event.amount
		if err := addFileEvent(v); err != nil {
			return err
		}
	}
	// A shared family phone remains two profiles; the surname is never shown as an inference.
	artem := EventInput{TenantID: ids[0], CustomerID: "sample-artem", EventID: "sample-artem-visit", Name: "Артём Лебедев", Phone: sofia.Phone, Email: "artem.lebedev@example.test", Consent: true, Type: "visit", OccurredAt: day(-12)}
	if err := addFileEvent(artem); err != nil {
		return err
	}

	// A refunded sale appears in history but is excluded from purchase-based outcomes.
	refundBuyer := EventInput{TenantID: ids[3], CustomerID: "sample-refund-customer", EventID: "sample-refund-sale", Name: "Оксана Ларина", Phone: "79009000002", Email: "oksana@example.test", Consent: true, Type: "purchase", OccurredAt: day(-10), Amount: 1450}
	if err := addFileEvent(refundBuyer); err != nil {
		return err
	}
	refundBuyer.EventID, refundBuyer.Type, refundBuyer.OccurredAt, refundBuyer.Amount, refundBuyer.RelatedEventID = "sample-refund-return", "refund", day(-9), 1450, "sample-refund-sale"
	if err := addFileEvent(refundBuyer); err != nil {
		return err
	}

	importedAt := anchor.AddDate(0, 0, -7).Add(10 * time.Hour).Format(time.RFC3339)
	for _, tenantID := range ids {
		count := counts[tenantID]
		if _, err := tx.Exec(`INSERT INTO imports(tenant_id,filename,rows_total,rows_ok,rows_failed,errors,created_at) VALUES(?,?,?,?,?,?,?)`, tenantID, files[tenantID], count, count, 0, "", importedAt); err != nil {
			return err
		}
	}

	type route struct {
		name           string
		source, target int64
		conversions    int
		hero           bool
	}
	routes := []route{
		{"Кофе после игры", ids[0], ids[1], 3, true},
		{"Истории продолжаются", ids[2], ids[3], 2, false},
		{"Движение после кофе", ids[1], ids[4], 1, false},
	}
	createdAt := anchor.AddDate(0, 0, -5).Add(8 * time.Hour).Format(time.RFC3339)
	type conversion struct {
		campaignID, target int64
		person             demoPerson
		amount             float64
	}
	conversions := []conversion{}
	for _, route := range routes {
		people, err := demoAudience(tx, route.source, route.target, createdAt)
		if err != nil {
			return err
		}
		if len(people) < route.conversions {
			return fmt.Errorf("недостаточно участников для кампании %s", route.name)
		}
		res, err := tx.Exec(`INSERT INTO campaigns(name,source_tenant_id,target_tenant_id,created_at,window_days) VALUES(?,?,?,?,?)`, route.name, route.source, route.target, createdAt, 14)
		if err != nil {
			return err
		}
		campaignID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, person := range people {
			if _, err := tx.Exec(`INSERT INTO campaign_members(campaign_id,profile_id) VALUES(?,?)`, campaignID, person.id); err != nil {
				return err
			}
		}
		selected := []demoPerson{}
		if route.hero {
			for _, person := range people {
				if person.name == sofia.Name {
					selected = append(selected, person)
					break
				}
			}
			if len(selected) == 0 {
				return errors.New("Клиент не попал в ожидаемую аудиторию")
			}
		}
		for _, person := range people {
			if len(selected) >= route.conversions {
				break
			}
			if route.hero && person.name == sofia.Name {
				continue
			}
			selected = append(selected, person)
		}
		for i, person := range selected {
			conversions = append(conversions, conversion{campaignID, route.target, person, float64(670 + i*390)})
		}
	}
	for _, conversion := range conversions {
		v := EventInput{
			TenantID:   conversion.target,
			CustomerID: fmt.Sprintf("campaign-%d-customer-%d", conversion.campaignID, conversion.person.id),
			EventID:    fmt.Sprintf("sample-conversion-%d-%d", conversion.campaignID, conversion.person.id),
			Name:       conversion.person.name, Phone: conversion.person.phone, Email: conversion.person.email, Consent: true,
			Type: "purchase", OccurredAt: day(-2), Amount: conversion.amount,
		}
		if err := a.insertEvent(tx, v, "api:sample"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO demo_seed_state(version,seeded_at) VALUES(1,?)`, now()); err != nil {
		return err
	}
	return tx.Commit()
}

type demoPerson struct {
	id                 int64
	name, phone, email string
}

func demoAudience(tx *sql.Tx, source, target int64, createdAt string) ([]demoPerson, error) {
	rows, err := tx.Query(`SELECT p.id,p.display_name,COALESCE(p.phone,''),COALESCE(p.email,'') FROM profiles p
		WHERE p.consent=1
		AND EXISTS(SELECT 1 FROM events e WHERE e.profile_id=p.id AND e.tenant_id=? AND e.kind IN ('purchase','visit','usage')
			AND datetime(e.occurred_at)<datetime(?) AND NOT EXISTS(SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund'))
		AND NOT EXISTS(SELECT 1 FROM events e WHERE e.profile_id=p.id AND e.tenant_id=? AND e.kind='purchase'
			AND NOT EXISTS(SELECT 1 FROM events rf WHERE rf.tenant_id=e.tenant_id AND rf.related_external_id=e.external_id AND rf.kind='refund'))
		ORDER BY p.id`, source, createdAt, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := []demoPerson{}
	for rows.Next() {
		var person demoPerson
		if err := rows.Scan(&person.id, &person.name, &person.phone, &person.email); err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	return people, rows.Err()
}

func (a *App) seed() error {
	var n int
	if e := a.db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	tenants := [][3]string{{"Киберклуб Арена", "Развлечения", "#5574d8"}, {"Кафе Лист", "Кафе и рестораны", "#df7858"}, {"Кинотеатр Атлас", "Кино", "#24a58f"}}
	for _, t := range tenants {
		if _, e = tx.Exec(`INSERT INTO tenants(name,kind,color) VALUES(?,?,?)`, t[0], t[1], t[2]); e != nil {
			return e
		}
	}
	today := time.Now().UTC()
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }
	events := []EventInput{
		{1, "club-101", "club-visit-1", "Алексей Морозов", "+7 913 111-22-33", "alex@example.test", true, "visit", day(-21), 0, ""},
		{1, "club-101", "club-use-1", "Алексей Морозов", "+7 913 111-22-33", "alex@example.test", true, "usage", day(-7), 900, ""},
		{1, "club-102", "club-visit-2", "Мария Белова", "+7 913 222-33-44", "maria@example.test", true, "visit", day(-12), 0, ""},
		{1, "club-103", "club-visit-3", "Илья Волков", "+7 913 333-44-55", "ilya@example.test", true, "visit", day(-9), 0, ""},
		{1, "club-104", "club-visit-4", "Елена Соколова", "+7 913 444-55-66", "elena@example.test", true, "visit", day(-5), 0, ""},
		{1, "club-105", "club-visit-5", "Ольга Ким", "+7 913 555-66-77", "olga@example.test", true, "visit", day(-10), 0, ""},
		{2, "cafe-201", "cafe-buy-1", "Алексей Морозов", "+7 913 111-22-33", "alex@example.test", true, "purchase", day(-4), 1250, ""},
		{2, "cafe-202", "cafe-buy-2", "Дарья Орлова", "+7 913 666-77-88", "daria@example.test", true, "purchase", day(-8), 830, ""},
		{3, "cin-301", "cin-buy-1", "Мария Белова", "+7 913 222-33-44", "maria@example.test", true, "purchase", day(-3), 1600, ""},
		{3, "cin-302", "cin-buy-2", "Дмитрий Петров", "+7 913 777-88-99", "dmitry@example.test", true, "purchase", day(-6), 1100, ""},
		{1, "club-106", "club-visit-6", "Анна Смирнова", "+7 913 999-00-11", "anna@example.test", true, "visit", day(-3), 0, ""},
		{2, "cafe-203", "cafe-buy-3", "Павел Смирнов", "+7 913 999-00-11", "pavel@example.test", true, "purchase", day(-2), 700, ""},
		{1, "club-107", "club-topup-1", "Никита Громов", "+7 913 888-99-00", "nikita@example.test", true, "topup", day(-1), 1000, ""},
	}
	for _, v := range events {
		if e = a.insertEvent(tx, v, "sample"); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	return nil
}
