package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrInvalid          = errors.New("invalid reading")
	ErrUnknownEquipment = errors.New("unknown equipment")
	ErrConflict         = errors.New("sample ID conflict")
)

type Equipment struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	NominalVoltage float64 `json:"nominalVoltage"`
}

type Reading struct {
	SampleID    string  `json:"sampleId"`
	EquipmentID string  `json:"equipmentId"`
	RecordedAt  string  `json:"recordedAt"`
	Voltage     float64 `json:"voltage"`
	Current     float64 `json:"current"`
	Temperature float64 `json:"temperature"`
	Efficiency  float64 `json:"efficiency"`
}

type Alert struct {
	ID          int64  `json:"id"`
	EquipmentID string `json:"equipmentId"`
	Kind        string `json:"kind"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
	Active      bool   `json:"active"`
}

type Summary struct {
	TotalReadings int `json:"totalReadings"`
	ActiveAlerts  int `json:"activeAlerts"`
	Equipment     int `json:"equipment"`
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS equipment (
	 id TEXT PRIMARY KEY, name TEXT NOT NULL, nominal_voltage REAL NOT NULL
	);
	CREATE TABLE IF NOT EXISTS readings (
	 seq INTEGER PRIMARY KEY AUTOINCREMENT,
	 sample_id TEXT NOT NULL UNIQUE,
	 equipment_id TEXT NOT NULL REFERENCES equipment(id),
	 recorded_at TEXT NOT NULL,
	 voltage REAL NOT NULL, current REAL NOT NULL,
	 temperature REAL NOT NULL, efficiency REAL NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_readings_equipment_seq ON readings(equipment_id,seq DESC);
	CREATE TABLE IF NOT EXISTS alerts (
	 id INTEGER PRIMARY KEY AUTOINCREMENT,
	 equipment_id TEXT NOT NULL REFERENCES equipment(id),
	 kind TEXT NOT NULL, severity TEXT NOT NULL, message TEXT NOT NULL,
	 first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, active INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_alerts_active ON alerts(active,last_seen DESC);
	INSERT OR IGNORE INTO equipment(id,name,nominal_voltage) VALUES
	 ('lab-unit-01','Lab Power Unit 01',480),
	 ('lab-unit-02','Lab Power Unit 02',480);
	`)
	return err
}

func validate(r Reading) error {
	if len(r.SampleID) < 8 || len(r.SampleID) > 128 {
		return fmt.Errorf("%w: sampleId must be 8-128 characters", ErrInvalid)
	}
	recordedAt, err := time.Parse(time.RFC3339, r.RecordedAt)
	if err != nil {
		return fmt.Errorf("%w: recordedAt must be RFC3339", ErrInvalid)
	}
	if recordedAt.After(time.Now().Add(2*time.Minute)) || recordedAt.Before(time.Now().Add(-24*time.Hour)) {
		return fmt.Errorf("%w: recordedAt must be within the last 24 hours", ErrInvalid)
	}
	if !finiteRange(r.Voltage, 0, 1000) || !finiteRange(r.Current, 0, 1000) || !finiteRange(r.Temperature, -40, 180) || !finiteRange(r.Efficiency, 0, 100) {
		return fmt.Errorf("%w: measurement out of range", ErrInvalid)
	}
	return nil
}
func finiteRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func (s *Store) Ingest(ctx context.Context, r Reading) (bool, []Alert, error) {
	if err := validate(r); err != nil {
		return false, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	var nominal float64
	if err := tx.QueryRowContext(ctx, `SELECT nominal_voltage FROM equipment WHERE id=?`, r.EquipmentID).Scan(&nominal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil, ErrUnknownEquipment
		}
		return false, nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO readings(sample_id,equipment_id,recorded_at,voltage,current,temperature,efficiency)
	VALUES(?,?,?,?,?,?,?)`, r.SampleID, r.EquipmentID, r.RecordedAt, r.Voltage, r.Current, r.Temperature, r.Efficiency)
	if err != nil {
		return false, nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, nil, err
	}
	if count == 0 {
		var original Reading
		err := tx.QueryRowContext(ctx, `SELECT sample_id,equipment_id,recorded_at,voltage,current,temperature,efficiency FROM readings WHERE sample_id=?`, r.SampleID).Scan(&original.SampleID, &original.EquipmentID, &original.RecordedAt, &original.Voltage, &original.Current, &original.Temperature, &original.Efficiency)
		if err != nil {
			return false, nil, err
		}
		if original != r {
			return false, nil, ErrConflict
		}
		return false, []Alert{}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT voltage,temperature,efficiency FROM readings WHERE equipment_id=? ORDER BY seq DESC LIMIT 3`, r.EquipmentID)
	if err != nil {
		return false, nil, err
	}
	type measure struct{ voltage, temperature, efficiency float64 }
	recent := []measure{}
	for rows.Next() {
		var m measure
		if err := rows.Scan(&m.voltage, &m.temperature, &m.efficiency); err != nil {
			rows.Close()
			return false, nil, err
		}
		recent = append(recent, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, nil, err
	}
	rows.Close()
	checks := []struct {
		kind, severity, message string
		abnormal                func(measure) bool
	}{
		{"over_temperature", "critical", "Temperature above 85 C for three consecutive readings", func(m measure) bool { return m.temperature > 85 }},
		{"voltage_deviation", "warning", "Voltage outside 10% of nominal for three consecutive readings", func(m measure) bool { return math.Abs(m.voltage-nominal) > nominal*.1 }},
		{"low_efficiency", "warning", "Efficiency below 92% for three consecutive readings", func(m measure) bool { return m.efficiency < 92 }},
	}
	changed := []Alert{}
	for _, check := range checks {
		abnormalNow := check.abnormal(recent[0])
		persistent := len(recent) == 3 && abnormalNow && check.abnormal(recent[1]) && check.abnormal(recent[2])
		var activeID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM alerts WHERE equipment_id=? AND kind=? AND active=1 ORDER BY id DESC LIMIT 1`, r.EquipmentID, check.kind).Scan(&activeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, nil, err
		}
		if persistent && errors.Is(err, sql.ErrNoRows) {
			result, err := tx.ExecContext(ctx, `INSERT INTO alerts(equipment_id,kind,severity,message,first_seen,last_seen,active) VALUES(?,?,?,?,?,?,1)`, r.EquipmentID, check.kind, check.severity, check.message, r.RecordedAt, r.RecordedAt)
			if err != nil {
				return false, nil, err
			}
			id, err := result.LastInsertId()
			if err != nil {
				return false, nil, err
			}
			changed = append(changed, Alert{id, r.EquipmentID, check.kind, check.severity, check.message, r.RecordedAt, r.RecordedAt, true})
		} else if !abnormalNow && err == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE alerts SET active=0,last_seen=? WHERE id=?`, r.RecordedAt, activeID); err != nil {
				return false, nil, err
			}
			changed = append(changed, Alert{activeID, r.EquipmentID, check.kind, check.severity, check.message, "", r.RecordedAt, false})
		} else if persistent && err == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE alerts SET last_seen=? WHERE id=?`, r.RecordedAt, activeID); err != nil {
				return false, nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, nil, err
	}
	return true, changed, nil
}

func (s *Store) Equipment(ctx context.Context) ([]Equipment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,nominal_voltage FROM equipment ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Equipment{}
	for rows.Next() {
		var e Equipment
		if err := rows.Scan(&e.ID, &e.Name, &e.NominalVoltage); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (s *Store) Readings(ctx context.Context, equipmentID string, limit int) ([]Reading, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sample_id,equipment_id,recorded_at,voltage,current,temperature,efficiency
	FROM readings WHERE equipment_id=? ORDER BY seq DESC LIMIT ?`, equipmentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Reading{}
	for rows.Next() {
		var r Reading
		if err := rows.Scan(&r.SampleID, &r.EquipmentID, &r.RecordedAt, &r.Voltage, &r.Current, &r.Temperature, &r.Efficiency); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

func (s *Store) Alerts(ctx context.Context, limit int) ([]Alert, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,equipment_id,kind,severity,message,first_seen,last_seen,active FROM alerts ORDER BY active DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Alert{}
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.EquipmentID, &a.Kind, &a.Severity, &a.Message, &a.FirstSeen, &a.LastSeen, &a.Active); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (s *Store) Summary(ctx context.Context) (Summary, error) {
	var m Summary
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM readings`).Scan(&m.TotalReadings); err != nil {
		return m, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts WHERE active=1`).Scan(&m.ActiveAlerts); err != nil {
		return m, err
	}
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM equipment`).Scan(&m.Equipment)
	return m, err
}
