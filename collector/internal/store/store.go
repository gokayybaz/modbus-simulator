// Package store writes machine readings and events to PostgreSQL.
package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"modbus-collector/internal/machine"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Event struct {
	Time      time.Time
	UnitID    byte
	Type      string
	FromState *string
	ToState   *string
	FaultCode *uint16
	Message   string
}

type Store struct{ pool *pgxpool.Pool }

// Open connects, retrying until ctx is done (the DB container may still be starting).
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	for wait := time.Second; ; wait = min(wait*2, 10*time.Second) {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return &Store{pool: pool}, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect database: %w", err)
		case <-time.After(wait):
		}
	}
}

func (s *Store) Close() { s.pool.Close() }

// Migrate applies the embedded schema files in name order, each once
// (tracked in schema_migrations). Statements run one by one outside an explicit
// transaction because TimescaleDB continuous aggregates require that.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		var done bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`,
			e.Name()).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := s.pool.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("migration %s: %w\n%s", e.Name(), err, stmt)
			}
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, e.Name()); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a migration on ";" at line ends. Migrations must not
// contain function bodies or string literals with such semicolons.
func splitStatements(sql string) []string {
	var out []string
	for _, part := range strings.Split(sql, ";\n") {
		if p := strings.TrimSpace(stripComments(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stripComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if j := strings.Index(l, "--"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

const insertReading = `INSERT INTO machine_readings (
 ts, unit_id, machine_state, machine_state_name, extruder_rpm, motor_current, melt_pressure,
 zone1_temp, zone2_temp, zone3_temp, die_temp, vacuum, cooling_water_temp, haul_off_speed,
 throughput, power_consumption, pipe_outer_diameter, pipe_wall_thickness, active_fault_code,
 total_produced_length, total_produced_weight, motor_load, melt_temp, melt_pressure_f32,
 sim_time_s, alarm_word, active_scenario, last_fault_code,
 running, heating, fault_active, emergency_stop_active, target_reached,
 alarm_motor_overload, alarm_melt_pressure_high, alarm_zone_temp_high, alarm_zone_temp_low,
 alarm_cooling_water_high, alarm_vacuum_low, alarm_haul_off_deviation, alarm_sensor_failure,
 alarm_emergency_stop,
 extruder_rpm_sp, zone1_temp_sp, zone2_temp_sp, zone3_temp_sp, die_temp_sp, haul_off_speed_sp,
 production_target, fault_scenario
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,
 $26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45,$46,$47,$48,$49,$50)
ON CONFLICT (unit_id, ts) DO NOTHING`

const insertEvent = `INSERT INTO machine_events (ts, unit_id, event_type, from_state, to_state, fault_code, message)
VALUES ($1,$2,$3,$4,$5,$6,$7)`

// Write stores one poll cycle (all lines + events) in a single round trip.
func (s *Store) Write(ctx context.Context, readings []machine.Reading, events []Event) error {
	b := &pgx.Batch{}
	for _, r := range readings {
		b.Queue(insertReading,
			r.Time, int16(r.UnitID), int16(r.MachineState), r.StateName(), r.ExtruderRPM, r.MotorCurrent, r.MeltPressure,
			r.Zone1Temp, r.Zone2Temp, r.Zone3Temp, r.DieTemp, r.Vacuum, r.CoolingWaterTemp, r.HaulOffSpeed,
			r.Throughput, r.PowerConsumption, r.PipeOuterDiameter, r.PipeWallThickness, int16(r.ActiveFaultCode),
			r.TotalLength, r.TotalWeight, r.MotorLoad, r.MeltTemp, r.MeltPressureF32,
			int64(r.SimTime), int32(r.AlarmWord), int16(r.ActiveScenario), int16(r.LastFaultCode),
			r.Running, r.Heating, r.FaultActive, r.EStopActive, r.TargetReached,
			r.AlarmMotorOverload, r.AlarmMeltPressureHigh, r.AlarmZoneTempHigh, r.AlarmZoneTempLow,
			r.AlarmCoolingWaterHigh, r.AlarmVacuumLow, r.AlarmHaulOffDeviation, r.AlarmSensorFailure,
			r.AlarmEmergencyStop,
			r.ExtruderRPMSP, r.Zone1TempSP, r.Zone2TempSP, r.Zone3TempSP, r.DieTempSP, r.HaulOffSpeedSP,
			r.ProductionTarget, int16(r.FaultScenario),
		)
	}
	for _, e := range events {
		var fc *int16
		if e.FaultCode != nil {
			v := int16(*e.FaultCode)
			fc = &v
		}
		b.Queue(insertEvent, e.Time, int16(e.UnitID), e.Type, e.FromState, e.ToState, fc, e.Message)
	}
	if b.Len() == 0 {
		return nil
	}
	return s.pool.SendBatch(ctx, b).Close()
}
