package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "sim.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	cfg, err := Parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:1502" || cfg.Isolation != "unit" || cfg.SimPeriod != 500*time.Millisecond || cfg.TimeScale != 1 {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.Lines) != 1 {
		t.Fatalf("lines %+v", cfg.Lines)
	}
	l := cfg.Lines[0]
	if l.ID != 1 || l.Name != "line-1" || l.Preset != "hdpe-110" || l.UnitID != 1 || l.Addr != "127.0.0.1:1502" || l.Seed != 1 ||
		l.RegisterMap != "" || l.Scenario != "" || l.Process != nil {
		t.Fatalf("line %+v", l)
	}
}

func TestFlags(t *testing.T) {
	cfg, err := Parse([]string{
		"-addr", "0.0.0.0:5020", "-unit-id", "10", "-max-clients", "4", "-idle-timeout", "0",
		"-shutdown-timeout", "2s", "-log-level", "debug", "-log-format", "json", "-register-map", "m.yaml",
		"-seed", "42", "-sim-period", "100ms", "-time-scale", "60", "-lines", "3", "-scenario", "cooling-failure",
		"-validate",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:5020" || cfg.MaxClients != 4 || cfg.IdleTimeout != 0 || cfg.ShutdownTimeout != 2*time.Second ||
		cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "json" || cfg.SimPeriod != 100*time.Millisecond ||
		cfg.TimeScale != 60 || !cfg.ValidateOnly {
		t.Fatalf("%+v", cfg)
	}
	for i, l := range cfg.Lines {
		if l.Preset != DefaultPresets[i] || l.UnitID != uint8(10+i) || l.Seed != uint64(42+i) ||
			l.RegisterMap != "m.yaml" || l.Scenario != "cooling-failure" || l.Addr != "0.0.0.0:5020" {
			t.Fatalf("line %d: %+v", i+1, l)
		}
	}
}

func TestPortIsolation(t *testing.T) {
	cfg, err := Parse([]string{"-lines", "3", "-isolation", "port", "-addr", "0.0.0.0:5020", "-unit-id", "7"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"0.0.0.0:5020", "0.0.0.0:5021", "0.0.0.0:5022"} {
		if l := cfg.Lines[i]; l.Addr != want || l.UnitID != 7 {
			t.Fatalf("line %d: %s unit %d", i+1, l.Addr, l.UnitID)
		}
	}
	// Port 0: every line its own OS-assigned port; accept-any is allowed.
	cfg, err = Parse([]string{"-lines", "2", "-isolation", "port", "-addr", "127.0.0.1:0", "-accept-any-unit"}, io.Discard)
	if err != nil || cfg.Lines[1].Addr != "127.0.0.1:0" {
		t.Fatalf("%+v %v", cfg.Lines, err)
	}
}

const fullFile = `version: 1
server:
  addr: 127.0.0.1:1600
  unit_id: 5
  isolation: unit
  max_clients: 8
  idle_timeout: 30s
  shutdown_timeout: 3s
log: {level: warn, format: json}
simulation: {period: 250ms, time_scale: 10, seed: 100}
register_map: maps/common.yaml
scenario: normal-production
lines:
  - name: a
    preset: hdpe-110
  - name: b
    preset: pvc-110
    unit_id: 20
    seed: 7
    register_map: /abs/pvc.yaml
    scenario: scenarios/pvc.yaml
    min_run_temp: 150
    setpoints: {extruder_rpm: 25}
    setpoint_limits: {zone1_temp: {min: 0, max: 210}}
    process:
      extruder: {output_per_rpm: 11}
    noise:
      temperature: {sigma: 0.1}
    fault_limits:
      melt_pressure_high: {warning: 280, trip: 320, delay: 2s}
`

func TestFile(t *testing.T) {
	p := writeFile(t, fullFile)
	dir := filepath.Dir(p)
	cfg, err := Parse([]string{"-config", p}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.File != p || cfg.Addr != "127.0.0.1:1600" || cfg.MaxClients != 8 || cfg.IdleTimeout != 30*time.Second ||
		cfg.ShutdownTimeout != 3*time.Second || cfg.LogLevel != slog.LevelWarn || cfg.LogFormat != "json" ||
		cfg.SimPeriod != 250*time.Millisecond || cfg.TimeScale != 10 {
		t.Fatalf("%+v", cfg)
	}
	a, b := cfg.Lines[0], cfg.Lines[1]
	if a.Name != "a" || a.UnitID != 5 || a.Seed != 100 || a.RegisterMap != filepath.Join(dir, "maps/common.yaml") ||
		a.Scenario != "normal-production" || a.Process != nil {
		t.Fatalf("line a: %+v", a)
	}
	if b.UnitID != 20 || b.Seed != 7 || b.RegisterMap != "/abs/pvc.yaml" || b.Scenario != filepath.Join(dir, "scenarios/pvc.yaml") ||
		*b.MinRunTemp != 150 || b.Setpoints["extruder_rpm"] != 25 || b.SetpointLimits["zone1_temp"].Max != 210 ||
		b.Process == nil || b.Noise == nil || b.FaultLimits == nil || !strings.Contains(b.Source, "lines[1]") {
		t.Fatalf("line b: %+v", b)
	}
}

// Explicit flags win over the file.
func TestFlagsOverrideFile(t *testing.T) {
	p := writeFile(t, fullFile)
	cfg, err := Parse([]string{"-config", p, "-addr", "127.0.0.1:1700", "-time-scale", "100", "-log-level", "debug",
		"-register-map", "flag.yaml", "-scenario", "emergency-stop", "-seed", "1"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:1700" || cfg.TimeScale != 100 || cfg.LogLevel != slog.LevelDebug || cfg.SimPeriod != 250*time.Millisecond {
		t.Fatalf("%+v", cfg)
	}
	for _, l := range cfg.Lines {
		if l.RegisterMap != "flag.yaml" || l.Scenario != "emergency-stop" || l.Addr != "127.0.0.1:1700" {
			t.Fatalf("line %s: %+v", l.Name, l)
		}
	}
	if cfg.Lines[0].Seed != 1 || cfg.Lines[1].Seed != 7 { // explicit per-line seed still wins
		t.Fatalf("seeds %d %d", cfg.Lines[0].Seed, cfg.Lines[1].Seed)
	}
}

func TestFileWithoutLinesUsesPresets(t *testing.T) {
	p := writeFile(t, "version: 1\nserver: {isolation: port, addr: '127.0.0.1:1800'}\n")
	cfg, err := Parse([]string{"-config", p, "-lines", "2"}, io.Discard)
	if err != nil || len(cfg.Lines) != 2 || cfg.Lines[1].Addr != "127.0.0.1:1801" || cfg.Lines[1].Preset != "hdpe-63" {
		t.Fatalf("%+v %v", cfg.Lines, err)
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name string
		file string // "" = no file
		args []string
		want string
	}{
		{"unit id flag", "", []string{"-unit-id", "256"}, "unit id 256 out of range"},
		{"addr", "", []string{"-addr", "1502"}, `addr "1502"`},
		{"addr port", "", []string{"-addr", "h:http"}, "must be a number"},
		{"max clients", "", []string{"-max-clients", "0"}, "max clients"},
		{"idle", "", []string{"-idle-timeout", "-1s"}, "idle timeout"},
		{"shutdown", "", []string{"-shutdown-timeout", "0"}, "shutdown timeout"},
		{"log level", "", []string{"-log-level", "loud"}, "log level"},
		{"log format", "", []string{"-log-format", "xml"}, "log format"},
		{"period", "", []string{"-sim-period", "1m"}, "sim period"},
		{"time scale", "", []string{"-time-scale", "0.5"}, "time scale"},
		{"lines", "", []string{"-lines", "4"}, "-lines must be 1..3"},
		{"isolation", "", []string{"-isolation", "vlan"}, "isolation must be"},
		{"unit overflow", "", []string{"-lines", "3", "-unit-id", "254"}, "unit id 256 out of range"},
		{"accept any shared", "", []string{"-lines", "2", "-accept-any-unit"}, "accept_any_unit needs one line per endpoint"},
		{"port overflow", "", []string{"-lines", "3", "-isolation", "port", "-addr", "127.0.0.1:65534"}, "port 65536 out of range"},
		{"extra args", "", []string{"x"}, "unexpected arguments"},
		{"unknown flag", "", []string{"-nope"}, "not defined"},
		{"missing file", "", []string{"-config", "/nonexistent/sim.yaml"}, "no such file"},
		{"empty file", " ", nil, "empty document"},
		{"version", "version: 2", nil, "unsupported version 2"},
		{"unknown key", "version: 1\nserver: {adress: x}", nil, "field adress not found"},
		{"bad duration", "version: 1\nserver: {idle_timeout: soon}", nil, "cannot unmarshal"},
		{"lines and -lines", "version: 1\nlines: [{preset: hdpe-110}]", []string{"-lines", "2"}, "-lines cannot be combined"},
		{"no preset", "version: 1\nlines: [{name: a}]", nil, "preset is required"},
		{"duplicate name", "version: 1\nlines: [{name: a, preset: x}, {name: a, preset: x}]", nil, `name "a" already used`},
		{"duplicate unit", "version: 1\nlines: [{preset: x, unit_id: 3}, {preset: x, unit_id: 3}]", nil, "unit id 3 on 127.0.0.1:1502 already used"},
		{"line unit range", "version: 1\nlines: [{preset: x, unit_id: 300}]", nil, "unit id 300 out of range"},
		{"port in unit mode", "version: 1\nlines: [{preset: x, port: 1600}]", nil, "port is only valid with isolation: port"},
		{"line port range", "version: 1\nserver: {isolation: port}\nlines: [{preset: x, port: 70000}]", nil, "port 70000 out of range"},
		{"min run temp", "version: 1\nlines: [{preset: x, min_run_temp: -5}]", nil, "min_run_temp must be ≥ 0"},
		{"limit order", "version: 1\nlines: [{preset: x, setpoint_limits: {zone1_temp: {min: 9, max: 1}}}]", nil, "min 9 > max 1"},
		{"too many lines", "version: 1\nlines: [" + strings.Repeat("{preset: x},", 33) + "]", nil, "at most 32"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args
			if tt.file != "" {
				args = append([]string{"-config", writeFile(t, tt.file)}, args...)
			}
			_, err := Parse(args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v\nwant mention of %q", err, tt.want)
			}
		})
	}
	// All problems at once.
	_, err := Parse([]string{"-max-clients", "0", "-time-scale", "0", "-log-format", "xml"}, io.Discard)
	if err == nil || strings.Count(err.Error(), "\n") < 2 {
		t.Fatalf("joined errors: %v", err)
	}
	// Errors name the file and the line entry.
	p := writeFile(t, "version: 1\nlines: [{name: ok, preset: x}, {name: bad}]")
	if _, err := Parse([]string{"-config", p}, io.Discard); err == nil || !strings.Contains(err.Error(), p+": lines[1] (bad)") {
		t.Fatalf("location: %v", err)
	}
}

func TestHelp(t *testing.T) {
	var out strings.Builder
	if _, err := Parse([]string{"-h"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v", err)
	}
	for _, f := range []string{"-config", "-scenario", "-validate", "-lines", "-isolation"} {
		if !strings.Contains(out.String(), f) {
			t.Fatalf("usage missing %s", f)
		}
	}
}
