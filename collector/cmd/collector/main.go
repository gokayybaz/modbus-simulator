// Command collector polls the extrusion line(s) over Modbus TCP at a fixed
// interval and stores every snapshot in PostgreSQL/TimescaleDB.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grid-x/modbus"

	"modbus-collector/internal/config"
	"modbus-collector/internal/machine"
	"modbus-collector/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("collector stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	slog.Info("database ready")

	handler := modbus.NewTCPClientHandler(cfg.ModbusAddr)
	handler.Timeout = cfg.ModbusTimeout
	handler.IdleTimeout = max(3*cfg.PollInterval, 10*time.Second) // keep the connection between polls
	defer handler.Close()
	client := modbus.NewClient(handler)

	slog.Info("polling", "addr", cfg.ModbusAddr, "units", fmt.Sprint([]int(unitInts(cfg.UnitIDs))), "interval", cfg.PollInterval)

	p := &poller{handler: handler, client: client, db: db, units: cfg.UnitIDs,
		timeout: cfg.ModbusTimeout, last: map[byte]*unitState{}}
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		p.cycle(ctx)
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return nil
		case <-ticker.C:
		}
	}
}

type unitState struct {
	seen      bool
	online    bool
	state     uint16
	faultCode uint16
}

type poller struct {
	handler *modbus.TCPClientHandler
	client  modbus.Client
	db      *store.Store
	units   []byte
	timeout time.Duration
	last    map[byte]*unitState
	written uint64
}

func (p *poller) cycle(ctx context.Context) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	var readings []machine.Reading
	var events []store.Event

	for _, unit := range p.units {
		st := p.last[unit]
		if st == nil {
			st = &unitState{}
			p.last[unit] = st
		}
		p.handler.SetSlave(unit)
		rctx, cancel := context.WithTimeout(ctx, 3*p.timeout)
		r, err := machine.Read(rctx, p.client, unit, now)
		cancel()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			if st.online || !st.seen {
				slog.Warn("modbus read failed", "unit", unit, "err", err)
				events = append(events, store.Event{Time: now, UnitID: unit, Type: "comm_lost", Message: err.Error()})
			}
			st.online, st.seen = false, true
			_ = p.handler.Close() // force a fresh connection next time
			continue
		}
		events = append(events, detect(st, r)...)
		readings = append(readings, r)
	}

	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := p.db.Write(wctx, readings, events); err != nil {
		slog.Error("database write failed", "err", err, "readings", len(readings), "events", len(events))
		return
	}
	for _, e := range events {
		slog.Info("event", "unit", e.UnitID, "type", e.Type, "from", deref(e.FromState), "to", deref(e.ToState),
			"fault_code", derefU(e.FaultCode), "msg", e.Message)
	}
	p.written += uint64(len(readings))
	for _, r := range readings {
		slog.Debug("reading", "unit", r.UnitID, "state", r.StateName(), "rpm", r.ExtruderRPM,
			"melt_pressure", r.MeltPressure, "die_temp", r.DieTemp, "length_m", r.TotalLength)
	}
	if p.written%60 == 0 && len(readings) > 0 {
		slog.Info("progress", "rows_written", p.written)
	}
}

// detect compares a reading with the previous one of the same line.
func detect(st *unitState, r machine.Reading) []store.Event {
	var ev []store.Event
	state := r.StateName()
	if !st.seen || !st.online {
		typ, msg := "collector_start", "first reading"
		if st.seen {
			typ, msg = "comm_restored", "modbus reachable again"
		}
		ev = append(ev, store.Event{Time: r.Time, UnitID: r.UnitID, Type: typ, ToState: &state, Message: msg})
	} else {
		if r.MachineState != st.state {
			from := machine.Reading{MachineState: st.state}.StateName()
			ev = append(ev, store.Event{Time: r.Time, UnitID: r.UnitID, Type: "state_change", FromState: &from, ToState: &state})
		}
		if r.ActiveFaultCode != st.faultCode {
			if r.ActiveFaultCode != 0 {
				code := r.ActiveFaultCode
				ev = append(ev, store.Event{Time: r.Time, UnitID: r.UnitID, Type: "fault", FaultCode: &code,
					Message: fmt.Sprintf("fault code %d", code)})
			} else {
				code := st.faultCode
				ev = append(ev, store.Event{Time: r.Time, UnitID: r.UnitID, Type: "fault_cleared", FaultCode: &code})
			}
		}
	}
	st.seen, st.online, st.state, st.faultCode = true, true, r.MachineState, r.ActiveFaultCode
	return ev
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefU(v *uint16) uint16 {
	if v == nil {
		return 0
	}
	return *v
}

func unitInts(b []byte) []int {
	out := make([]int, len(b))
	for i, v := range b {
		out[i] = int(v)
	}
	return out
}
