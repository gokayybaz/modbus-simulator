// Package api serves the dashboard JSON API on top of the collector's
// TimescaleDB schema (machine_readings, machine_events).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"modbus-dashboard/internal/catalog"
	"modbus-dashboard/internal/rca"
)

type Server struct {
	DB *pgxpool.Pool
	// OnlineAfter: a line counts as offline if its newest reading is older.
	OnlineAfter time.Duration
	PollHint    time.Duration
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("GET /api/units", s.units)
	mux.HandleFunc("GET /api/live", s.live)
	mux.HandleFunc("GET /api/trend", s.trend)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/faults", s.faults)
	mux.HandleFunc("GET /api/rca", s.rca)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("encode", "err", err)
	}
}

func fail(w http.ResponseWriter, code int, err error) {
	if code >= 500 {
		slog.Error("api", "err", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func qUnit(r *http.Request) (int, error) {
	u, err := strconv.Atoi(r.URL.Query().Get("unit"))
	if err != nil || u < 0 || u > 255 {
		return 0, errors.New("unit: 0..255 bekleniyor")
	}
	return u, nil
}

// qRange parses ?range=15m (default def, max 30 days) or ?from=&to= (RFC 3339).
func qRange(r *http.Request, def time.Duration) (time.Time, time.Time, error) {
	q := r.URL.Query()
	to := time.Now().UTC()
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: %w", err)
		}
		to = t
	}
	if v := q.Get("from"); v != "" {
		f, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: %w", err)
		}
		if !f.Before(to) {
			return time.Time{}, time.Time{}, errors.New("from < to olmalı")
		}
		return f, to, nil
	}
	d := def
	if v := q.Get("range"); v != "" {
		x, err := time.ParseDuration(v)
		if err != nil || x <= 0 {
			return time.Time{}, time.Time{}, errors.New("range: ör. 15m, 1h")
		}
		d = x
	}
	d = min(d, 30*24*time.Hour)
	return to.Add(-d), to, nil
}

var allCols = func() []string {
	var c []string
	for _, s := range catalog.Signals {
		c = append(c, s.Key)
	}
	for _, s := range catalog.Setpoints {
		c = append(c, s.Key)
	}
	return c
}()

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

func clean(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return math.Round(f*1000) / 1000
}

// rowMaps scans rows into maps keyed by column name.
func rowMaps(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(vals))
		for i, f := range fields {
			v := vals[i]
			if fv, ok := v.(float64); ok {
				v = clean(fv)
			}
			if fv, ok := v.(float32); ok {
				v = clean(float64(fv))
			}
			m[f.Name] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- handlers ----------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		fail(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"signals": catalog.Signals, "setpoints": catalog.Setpoints, "alarms": catalog.Alarms,
		"limits": catalog.Limits, "faults": catalog.Faults, "scenarios": catalog.Scenarios,
		"causes": catalog.Causes, "states": catalog.StateNames,
		"pollMs": s.PollHint.Milliseconds(),
	})
}

func (s *Server) units(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT DISTINCT ON (unit_id) unit_id, ts, machine_state, machine_state_name, active_fault_code,
		       alarm_word, extruder_rpm, throughput, total_produced_length, fault_scenario
		FROM machine_readings WHERE ts > now() - interval '30 days'
		ORDER BY unit_id, ts DESC`)
	if err != nil {
		fail(w, 500, err)
		return
	}
	list, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	now := time.Now()
	for _, u := range list {
		ts, _ := u["ts"].(time.Time)
		u["online"] = now.Sub(ts) < s.OnlineAfter
		u["ageS"] = math.Round(now.Sub(ts).Seconds()*10) / 10
	}
	if list == nil {
		list = []map[string]any{}
	}
	writeJSON(w, list)
}

// live returns the newest full reading plus the last `window` seconds of
// readings (for sparklines), the latest events and active alarms.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	unit, err := qUnit(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	window := 180
	if v, err := strconv.Atoi(r.URL.Query().Get("window")); err == nil && v >= 10 && v <= 3600 {
		window = v
	}
	ctx := r.Context()
	rows, err := s.DB.Query(ctx, `SELECT * FROM machine_readings WHERE unit_id=$1 ORDER BY ts DESC LIMIT 1`, unit)
	if err != nil {
		fail(w, 500, err)
		return
	}
	latest, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	if len(latest) == 0 {
		writeJSON(w, map[string]any{"latest": nil, "recent": map[string]any{}, "events": []any{}})
		return
	}
	last := latest[0]["ts"].(time.Time)
	rows, err = s.DB.Query(ctx, `SELECT ts, machine_state, alarm_word, `+strings.Join(allCols, ",")+`
		FROM machine_readings WHERE unit_id=$1 AND ts > $2 ORDER BY ts`, unit, last.Add(-time.Duration(window)*time.Second))
	if err != nil {
		fail(w, 500, err)
		return
	}
	rec, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	cols := map[string][]any{}
	for _, m := range rec {
		for k, v := range m {
			cols[k] = append(cols[k], v)
		}
	}
	rows, err = s.DB.Query(ctx, `SELECT id, ts, unit_id, event_type, from_state, to_state, fault_code, message
		FROM machine_events WHERE unit_id=$1 ORDER BY ts DESC, id DESC LIMIT 8`, unit)
	if err != nil {
		fail(w, 500, err)
		return
	}
	ev, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	l := latest[0]
	l["online"] = time.Since(last) < s.OnlineAfter
	l["ageS"] = math.Round(time.Since(last).Seconds()*10) / 10
	writeJSON(w, map[string]any{"latest": l, "recent": cols, "events": nonNil(ev)})
}

func nonNil(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}

// trend returns time-bucketed averages (≤ ~720 points) for every signal.
func (s *Server) trend(w http.ResponseWriter, r *http.Request) {
	unit, err := qUnit(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	from, to, err := qRange(r, 15*time.Minute)
	if err != nil {
		fail(w, 400, err)
		return
	}
	bucket := max(1.0, math.Ceil(to.Sub(from).Seconds()/720))
	var sel []string
	for _, c := range allCols {
		sel = append(sel, fmt.Sprintf("avg(%[1]s) AS %[1]s", c))
	}
	for _, c := range []string{"melt_pressure", "motor_load"} {
		sel = append(sel, fmt.Sprintf("max(%[1]s) AS %[1]s_max", c))
	}
	q := `SELECT time_bucket(make_interval(secs => $4), ts) AS t, max(machine_state) AS machine_state,
		bit_or(alarm_word) AS alarm_word, max(active_fault_code) AS active_fault_code, count(*) AS n, ` +
		strings.Join(sel, ",") + `
		FROM machine_readings WHERE unit_id=$1 AND ts >= $2 AND ts <= $3 GROUP BY 1 ORDER BY 1`
	rows, err := s.DB.Query(r.Context(), q, unit, from, to, bucket)
	if err != nil {
		fail(w, 500, err)
		return
	}
	list, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	cols := map[string][]any{}
	for _, m := range list {
		for k, v := range m {
			cols[k] = append(cols[k], v)
		}
	}
	writeJSON(w, map[string]any{"from": from, "to": to, "bucketS": bucket, "series": cols})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	from, to, err := qRange(r, 24*time.Hour)
	if err != nil {
		fail(w, 400, err)
		return
	}
	args := []any{from, to}
	q := `SELECT id, ts, unit_id, event_type, from_state, to_state, fault_code, message FROM machine_events
		WHERE ts >= $1 AND ts <= $2`
	if r.URL.Query().Get("unit") != "" {
		unit, err := qUnit(r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		q += ` AND unit_id = $3`
		args = append(args, unit)
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT 500`
	rows, err := s.DB.Query(r.Context(), q, args...)
	if err != nil {
		fail(w, 500, err)
		return
	}
	list, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, nonNil(list))
}

func (s *Server) faults(w http.ResponseWriter, r *http.Request) {
	unit, err := qUnit(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	from, to, err := qRange(r, 7*24*time.Hour)
	if err != nil {
		fail(w, 400, err)
		return
	}
	rows, err := s.DB.Query(r.Context(), `
		SELECT e.id, e.ts, e.unit_id, e.fault_code,
		  (SELECT c.ts FROM machine_events c WHERE c.unit_id = e.unit_id AND c.event_type = 'fault_cleared'
		     AND c.ts > e.ts ORDER BY c.ts LIMIT 1) AS cleared_at,
		  (SELECT m.fault_scenario FROM machine_readings m WHERE m.unit_id = e.unit_id AND m.ts >= e.ts
		     ORDER BY m.ts LIMIT 1) AS fault_scenario
		FROM machine_events e
		WHERE e.event_type = 'fault' AND e.unit_id = $1 AND e.ts >= $2 AND e.ts <= $3
		ORDER BY e.ts DESC LIMIT 200`, unit, from, to)
	if err != nil {
		fail(w, 500, err)
		return
	}
	list, err := rowMaps(rows)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, nonNil(list))
}

type segment struct {
	State      int       `json:"state"`
	Start, End time.Time `json:"-"`
	StartJS    time.Time `json:"start"`
	EndJS      time.Time `json:"end"`
	DurationS  float64   `json:"durationS"`
}

// stats returns state segments, availability, MTBF/MTTR, fault Pareto and
// production/energy KPIs over the range.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	unit, err := qUnit(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	from, to, err := qRange(r, time.Hour)
	if err != nil {
		fail(w, 400, err)
		return
	}
	ctx := r.Context()
	// Gaps-and-islands of machine_state; a gap > 10 s (no data) ends an island.
	rows, err := s.DB.Query(ctx, `
		WITH r AS (
		  SELECT ts, machine_state,
		         lead(ts) OVER (ORDER BY ts) AS nt,
		         lead(sim_time_s) OVER (ORDER BY ts) - sim_time_s AS dsim,
		         power_consumption, total_produced_length, total_produced_weight
		  FROM machine_readings WHERE unit_id=$1 AND ts >= $2 AND ts <= $3),
		g AS (
		  SELECT *, least(coalesce(nt, ts) - ts, interval '10 seconds') AS dt,
		    row_number() OVER (ORDER BY ts) - row_number() OVER (PARTITION BY machine_state ORDER BY ts) AS grp
		  FROM r)
		SELECT machine_state, min(ts) AS s, max(ts + dt) AS e, extract(epoch FROM sum(dt))::float8 AS dur,
		       -- energy on simulator time (counters advance in sim time; -time-scale safe)
		       coalesce(sum(power_consumption * least(greatest(dsim, 0), 600) / 3600.0), 0)::float8 AS kwh
		FROM g GROUP BY machine_state, grp ORDER BY s`, unit, from, to)
	if err != nil {
		fail(w, 500, err)
		return
	}
	var segs []segment
	byState := map[int]float64{}
	var kwh, total float64
	for rows.Next() {
		var sg segment
		var st int16
		var e float64
		if err := rows.Scan(&st, &sg.Start, &sg.End, &sg.DurationS, &e); err != nil {
			rows.Close()
			fail(w, 500, err)
			return
		}
		sg.State, sg.StartJS, sg.EndJS = int(st), sg.Start, sg.End
		segs = append(segs, sg)
		byState[sg.State] += sg.DurationS
		total += sg.DurationS
		kwh += e
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fail(w, 500, err)
		return
	}
	// Merge very short segments for the timeline is left to the client.
	var faultSegs []float64
	for _, sg := range segs {
		if sg.State == 3 {
			faultSegs = append(faultSegs, sg.DurationS)
		}
	}

	var lenD, wD *float64
	_ = s.DB.QueryRow(ctx, `SELECT max(total_produced_length) - min(total_produced_length),
		max(total_produced_weight) - min(total_produced_weight)
		FROM machine_readings WHERE unit_id=$1 AND ts >= $2 AND ts <= $3`, unit, from, to).Scan(&lenD, &wD)

	rows, err = s.DB.Query(ctx, `SELECT fault_code, count(*) FROM machine_events
		WHERE event_type='fault' AND unit_id=$1 AND ts >= $2 AND ts <= $3 GROUP BY 1 ORDER BY 2 DESC`, unit, from, to)
	if err != nil {
		fail(w, 500, err)
		return
	}
	type pareto struct {
		Code  int    `json:"code"`
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	var par []pareto
	nFaults := 0
	for rows.Next() {
		var c *int16
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			rows.Close()
			fail(w, 500, err)
			return
		}
		code := 0
		if c != nil {
			code = int(*c)
		}
		f, _ := catalog.FaultByCode(code)
		par = append(par, pareto{Code: code, Label: f.Label, Count: n})
		nFaults += n
	}
	rows.Close()

	running := byState[2]
	out := map[string]any{
		"from": from, "to": to, "segments": segs, "stateSeconds": byState, "totalSeconds": total,
		"faultCount": nFaults, "pareto": par, "energyKWh": clean(kwh),
	}
	if total > 0 {
		out["availability"] = clean(running / total)
	}
	if nFaults > 0 {
		out["mtbfS"] = clean(running / float64(nFaults))
	}
	if len(faultSegs) > 0 {
		var sum float64
		for _, d := range faultSegs {
			sum += d
		}
		out["mttrS"] = clean(sum / float64(len(faultSegs)))
	}
	if lenD != nil {
		out["producedM"] = clean(*lenD)
	}
	if wD != nil {
		out["producedKg"] = clean(*wD)
		if *wD > 0 {
			out["specificEnergy"] = clean(kwh / *wD)
		}
	}
	if out["segments"] == nil || segs == nil {
		out["segments"] = []segment{}
	}
	if par == nil {
		out["pareto"] = []pareto{}
	}
	writeJSON(w, out)
}

func (s *Server) rca(w http.ResponseWriter, r *http.Request) {
	unit, err := qUnit(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	q := r.URL.Query()
	tripAt, err := time.Parse(time.RFC3339Nano, q.Get("ts"))
	if err != nil {
		fail(w, 400, fmt.Errorf("ts: %w", err))
		return
	}
	code, _ := strconv.Atoi(q.Get("code"))
	lookback := 10 * time.Minute
	if v, err := time.ParseDuration(q.Get("lookback")); err == nil && v >= 10*time.Second && v <= 6*time.Hour {
		lookback = v
	}
	post := time.Minute
	if v, err := time.ParseDuration(q.Get("post")); err == nil && v >= 0 && v <= time.Hour {
		post = v
	}
	ctx := r.Context()
	if code == 0 {
		var c *int16
		_ = s.DB.QueryRow(ctx, `SELECT active_fault_code FROM machine_readings WHERE unit_id=$1 AND ts >= $2 ORDER BY ts LIMIT 1`,
			unit, tripAt).Scan(&c)
		if c != nil {
			code = int(*c)
		}
	}
	// Cap the window at the previous trip of the same line so earlier faults
	// do not leak into the analysis (the segment logic also guards this).
	rows, err := s.DB.Query(ctx, `SELECT ts, sim_time_s, machine_state, active_fault_code, fault_scenario, alarm_word,
		emergency_stop_active, `+strings.Join(allCols, ",")+`
		FROM machine_readings WHERE unit_id=$1 AND ts >= $2 AND ts <= $3 ORDER BY ts`,
		unit, tripAt.Add(-lookback), tripAt.Add(post))
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	var samples []rca.Sample
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			fail(w, 500, err)
			return
		}
		sm := rca.Sample{
			T: vals[0].(time.Time), Sim: int64(num(vals[1])), State: int(num(vals[2])), ActiveFault: int(num(vals[3])),
			FaultScenario: int(num(vals[4])), AlarmWord: int(num(vals[5])), EStop: num(vals[6]) == 1,
			V: make(map[string]float64, len(allCols)),
		}
		for i, c := range allCols {
			sm.V[c] = num(vals[7+i])
		}
		samples = append(samples, sm)
	}
	if err := rows.Err(); err != nil {
		fail(w, 500, err)
		return
	}
	res := rca.Analyze(rca.Input{UnitID: unit, FaultCode: code, TripTime: tripAt, Samples: samples})
	writeJSON(w, res)
}
