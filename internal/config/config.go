// Package config resolves the simulator configuration from defaults, an
// optional YAML file (-config) and command-line flags, in that order of
// precedence (an explicitly given flag wins over the file). It validates
// everything it can without knowing the simulation itself; presets, process
// overrides, register maps and scenarios are validated when the plant is
// built, still before anything starts (see internal/plant).
//
// File reference: docs/configuration.md, example: configs/simulator.example.yaml.
package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// DefaultPresets are the built-in line presets used by -lines N, in order.
var DefaultPresets = []string{"hdpe-110", "hdpe-63", "pvc-110"}

// MaxLines is the number of lines -lines can create (one per preset).
var MaxLines = len(DefaultPresets)

// maxFileLines bounds the number of lines a configuration file may define.
const maxFileLines = 32

// Range is an inclusive engineering-unit range.
type Range struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// Line is the resolved configuration of one production line.
type Line struct {
	ID             int // 1-based
	Name           string
	Preset         string
	UnitID         uint8
	Addr           string // listen address of the line's endpoint
	Seed           uint64
	RegisterMap    string // file path; "" = embedded default
	Scenario       string // built-in name or file path; "" = none
	MinRunTemp     *float64
	Setpoints      map[string]float64
	SetpointLimits map[string]Range
	// Raw override sections, decoded strictly onto the preset by the plant.
	Process, Noise, FaultLimits *yaml.Node
	Source                      string // where the line was defined (for error messages)
}

// Config is the runtime configuration of the simulator.
type Config struct {
	File            string // configuration file ("" = none)
	Addr            string
	UnitID          uint8
	AcceptAnyUnit   bool
	MaxClients      int
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
	LogFormat       string // "text" or "json"
	SimPeriod       time.Duration
	TimeScale       float64 // real-time acceleration factor
	Isolation       string  // "unit": one endpoint, unit id per line; "port": one port per line
	ValidateOnly    bool    // -validate: check everything and exit
	Lines           []Line
}

// Default returns the default configuration (one hdpe-110 line).
func Default() Config {
	return Config{
		Addr:            "127.0.0.1:1502",
		UnitID:          1,
		MaxClients:      32,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 5 * time.Second,
		LogLevel:        slog.LevelInfo,
		LogFormat:       "text",
		SimPeriod:       500 * time.Millisecond,
		TimeScale:       1,
		Isolation:       "unit",
	}
}

// --- file schema -----------------------------------------------------------

type fileSpec struct {
	Version int `yaml:"version"`
	Server  struct {
		Addr            *string        `yaml:"addr"`
		UnitID          *int           `yaml:"unit_id"`
		AcceptAnyUnit   *bool          `yaml:"accept_any_unit"`
		Isolation       *string        `yaml:"isolation"`
		MaxClients      *int           `yaml:"max_clients"`
		IdleTimeout     *time.Duration `yaml:"idle_timeout"`
		ShutdownTimeout *time.Duration `yaml:"shutdown_timeout"`
	} `yaml:"server"`
	Log struct {
		Level  *string `yaml:"level"`
		Format *string `yaml:"format"`
	} `yaml:"log"`
	Simulation struct {
		Period    *time.Duration `yaml:"period"`
		TimeScale *float64       `yaml:"time_scale"`
		Seed      *uint64        `yaml:"seed"`
	} `yaml:"simulation"`
	RegisterMap string     `yaml:"register_map"`
	Scenario    string     `yaml:"scenario"`
	Lines       []lineSpec `yaml:"lines"`
}

type lineSpec struct {
	Name           string             `yaml:"name"`
	Preset         string             `yaml:"preset"`
	UnitID         *int               `yaml:"unit_id"`
	Port           *int               `yaml:"port"`
	Seed           *uint64            `yaml:"seed"`
	RegisterMap    string             `yaml:"register_map"`
	Scenario       string             `yaml:"scenario"`
	MinRunTemp     *float64           `yaml:"min_run_temp"`
	Setpoints      map[string]float64 `yaml:"setpoints"`
	SetpointLimits map[string]Range   `yaml:"setpoint_limits"`
	Process        yaml.Node          `yaml:"process"`
	Noise          yaml.Node          `yaml:"noise"`
	FaultLimits    yaml.Node          `yaml:"fault_limits"`
}

func loadFile(path string) (*fileSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f fileSpec
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty document")
		}
		return nil, err
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("version: unsupported version %d (want 1)", f.Version)
	}
	return &f, nil
}

// --- parsing ---------------------------------------------------------------

// Parse resolves the configuration from command-line arguments (without the
// program name). Usage and parse errors are written to output. -h returns
// flag.ErrHelp. All validation problems are reported together.
func Parse(args []string, output io.Writer) (Config, error) {
	d := Default()
	fs := flag.NewFlagSet("simulator", flag.ContinueOnError)
	fs.SetOutput(output)

	var (
		file        = fs.String("config", "", "YAML configuration file (see configs/simulator.example.yaml)")
		addr        = fs.String("addr", d.Addr, "TCP listen address (host:port)")
		unitID      = fs.Uint("unit-id", uint(d.UnitID), "Modbus unit id of line 1 (0-255)")
		acceptAny   = fs.Bool("accept-any-unit", false, "answer requests for any unit id (one line per endpoint)")
		maxClients  = fs.Int("max-clients", d.MaxClients, "maximum concurrent client connections per endpoint")
		idle        = fs.Duration("idle-timeout", d.IdleTimeout, "close idle client connections after this duration (0 = never)")
		shutdown    = fs.Duration("shutdown-timeout", d.ShutdownTimeout, "maximum time to wait for clients on shutdown")
		logLevel    = fs.String("log-level", "info", "log level: debug, info, warn, error")
		logFormat   = fs.String("log-format", d.LogFormat, "log format: text or json")
		regMap      = fs.String("register-map", "", "YAML register map for all lines (default: embedded configs/register-map.yaml)")
		seed        = fs.Uint64("seed", 1, "simulation random seed of line 1 (line i: seed + i - 1)")
		period      = fs.Duration("sim-period", d.SimPeriod, "fixed simulation timestep dt (1ms..10s)")
		timeScale   = fs.Float64("time-scale", d.TimeScale, "run simulated time this many times faster than real time (1..1000)")
		lines       = fs.Int("lines", 1, fmt.Sprintf("number of built-in lines when no config file defines them: 1..%d (%s)", MaxLines, strings.Join(DefaultPresets, ", ")))
		isolation   = fs.String("isolation", d.Isolation, "multi-line addressing: unit (one endpoint, unit id per line) or port (one port per line)")
		scenario    = fs.String("scenario", "", "scenario for every line: built-in name or YAML file (overrides the config file)")
		validateOnl = fs.Bool("validate", false, "validate the configuration, register maps and scenarios, then exit")
	)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	cfg := d
	cfg.ValidateOnly = *validateOnl
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	// 1. Configuration file.
	var f *fileSpec
	baseDir := "."
	levelText := "info"
	unitBase, seedBase := int(d.UnitID), uint64(1)
	var globalMap, globalScenario string
	if *file != "" {
		var err error
		if f, err = loadFile(*file); err != nil {
			return Config{}, fmt.Errorf("%s: %w", *file, err)
		}
		cfg.File, baseDir = *file, filepath.Dir(*file)
		s := f.Server
		setIf(&cfg.Addr, s.Addr)
		setIf(&unitBase, s.UnitID)
		setIf(&cfg.AcceptAnyUnit, s.AcceptAnyUnit)
		setIf(&cfg.Isolation, s.Isolation)
		setIf(&cfg.MaxClients, s.MaxClients)
		setIf(&cfg.IdleTimeout, s.IdleTimeout)
		setIf(&cfg.ShutdownTimeout, s.ShutdownTimeout)
		setIf(&levelText, f.Log.Level)
		setIf(&cfg.LogFormat, f.Log.Format)
		setIf(&cfg.SimPeriod, f.Simulation.Period)
		setIf(&cfg.TimeScale, f.Simulation.TimeScale)
		setIf(&seedBase, f.Simulation.Seed)
		globalMap = resolvePath(baseDir, f.RegisterMap)
		globalScenario = resolveScenario(baseDir, f.Scenario)
	}

	// 2. Explicit flags override the file.
	for name, apply := range map[string]func(){
		"addr": func() { cfg.Addr = *addr }, "unit-id": func() { unitBase = int(*unitID) },
		"accept-any-unit": func() { cfg.AcceptAnyUnit = *acceptAny }, "max-clients": func() { cfg.MaxClients = *maxClients },
		"idle-timeout": func() { cfg.IdleTimeout = *idle }, "shutdown-timeout": func() { cfg.ShutdownTimeout = *shutdown },
		"log-level": func() { levelText = *logLevel }, "log-format": func() { cfg.LogFormat = *logFormat },
		"register-map": func() { globalMap = *regMap }, "seed": func() { seedBase = *seed },
		"sim-period": func() { cfg.SimPeriod = *period }, "time-scale": func() { cfg.TimeScale = *timeScale },
		"isolation": func() { cfg.Isolation = *isolation }, "scenario": func() { globalScenario = *scenario },
	} {
		if set[name] {
			apply()
		}
	}

	// 3. Scalar validation.
	if err := cfg.LogLevel.UnmarshalText([]byte(levelText)); err != nil {
		fail("log level: %v", err)
	}
	if unitBase < 0 || unitBase > 255 {
		fail("unit id %d out of range 0..255", unitBase)
	}
	cfg.UnitID = uint8(max(0, min(255, unitBase)))
	host, port, addrErr := splitAddr(cfg.Addr)
	if addrErr != nil {
		fail("addr %q: %v", cfg.Addr, addrErr)
	}
	if cfg.MaxClients < 1 {
		fail("max clients must be >= 1, got %d", cfg.MaxClients)
	}
	if cfg.IdleTimeout < 0 {
		fail("idle timeout must not be negative")
	}
	if cfg.ShutdownTimeout <= 0 {
		fail("shutdown timeout must be positive")
	}
	if cfg.SimPeriod < time.Millisecond || cfg.SimPeriod > 10*time.Second {
		fail("sim period %v out of range 1ms..10s", cfg.SimPeriod)
	}
	if !(cfg.TimeScale >= 1 && cfg.TimeScale <= 1000) {
		fail("time scale %v out of range 1..1000", cfg.TimeScale)
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		fail("log format must be text or json, got %q", cfg.LogFormat)
	}
	if cfg.Isolation != "unit" && cfg.Isolation != "port" {
		fail("isolation must be unit or port, got %q", cfg.Isolation)
	}

	// 4. Lines.
	var specs []lineSpec
	src := "-lines"
	switch {
	case f != nil && len(f.Lines) > 0:
		if set["lines"] {
			fail("-lines cannot be combined with a configuration file that defines lines")
		}
		if len(f.Lines) > maxFileLines {
			fail("%d lines defined, at most %d supported", len(f.Lines), maxFileLines)
		}
		specs, src = f.Lines, cfg.File
	default:
		if *lines < 1 || *lines > MaxLines {
			fail("-lines must be 1..%d, got %d", MaxLines, *lines)
		} else {
			for _, p := range DefaultPresets[:*lines] {
				specs = append(specs, lineSpec{Preset: p})
			}
		}
	}
	units, addrs, names := map[string]map[int]string{}, map[string]string{}, map[string]string{}
	for i, s := range specs {
		where := fmt.Sprintf("%s: lines[%d]", src, i)
		l := Line{
			ID: i + 1, Name: s.Name, Preset: s.Preset, Source: where,
			RegisterMap: globalMap, Scenario: globalScenario, MinRunTemp: s.MinRunTemp,
			Setpoints: s.Setpoints, SetpointLimits: s.SetpointLimits,
			Process: nodeOrNil(&s.Process), Noise: nodeOrNil(&s.Noise), FaultLimits: nodeOrNil(&s.FaultLimits),
		}
		if l.Name == "" {
			l.Name = fmt.Sprintf("line-%d", i+1)
		}
		if prev, dup := names[l.Name]; dup {
			fail("%s: name %q already used by %s", where, l.Name, prev)
		}
		names[l.Name] = where
		if l.Preset == "" {
			fail("%s (%s): preset is required (one of %s)", where, l.Name, strings.Join(DefaultPresets, ", "))
		}
		if s.RegisterMap != "" && !set["register-map"] {
			l.RegisterMap = resolvePath(baseDir, s.RegisterMap)
		}
		if s.Scenario != "" && !set["scenario"] {
			l.Scenario = resolveScenario(baseDir, s.Scenario)
		}

		// Unit id and endpoint.
		unit := unitBase
		if cfg.Isolation == "unit" {
			unit = unitBase + i
		}
		if s.UnitID != nil {
			unit = *s.UnitID
		}
		if unit < 0 || unit > 255 {
			fail("%s (%s): unit id %d out of range 0..255", where, l.Name, unit)
		}
		l.UnitID = uint8(max(0, min(255, unit)))
		l.Addr = cfg.Addr
		switch {
		case cfg.Isolation == "unit" && s.Port != nil:
			fail("%s (%s): port is only valid with isolation: port", where, l.Name)
		case cfg.Isolation == "port" && addrErr == nil:
			p := port
			if p != 0 {
				p += i
			}
			if s.Port != nil {
				p = *s.Port
			}
			if p < 0 || p > 65535 {
				fail("%s (%s): port %d out of range 0..65535", where, l.Name, p)
			}
			l.Addr = net.JoinHostPort(host, strconv.Itoa(p))
		}
		// Endpoint identity: with port isolation and port 0 every line gets
		// its own OS-assigned port, otherwise lines on one address share it.
		ep := l.Addr
		if cfg.Isolation == "port" && strings.HasSuffix(l.Addr, ":0") {
			ep = fmt.Sprintf("%s#%d", l.Addr, i)
		}
		if units[ep] == nil {
			units[ep] = map[int]string{}
		}
		if prev, dup := units[ep][int(l.UnitID)]; dup {
			fail("%s (%s): unit id %d on %s already used by %s", where, l.Name, l.UnitID, l.Addr, prev)
		}
		units[ep][int(l.UnitID)] = l.Name
		if prev, shared := addrs[ep]; shared && cfg.AcceptAnyUnit {
			fail("%s (%s): accept_any_unit needs one line per endpoint, %s is shared with %s", where, l.Name, l.Addr, prev)
		}
		addrs[ep] = l.Name

		l.Seed = seedBase + uint64(i)
		if s.Seed != nil {
			l.Seed = *s.Seed
		}
		if s.MinRunTemp != nil && (math.IsNaN(*s.MinRunTemp) || *s.MinRunTemp < 0) {
			fail("%s (%s): min_run_temp must be ≥ 0", where, l.Name)
		}
		for name, r := range s.SetpointLimits {
			if !(r.Min <= r.Max) {
				fail("%s (%s): setpoint_limits.%s: min %g > max %g", where, l.Name, name, r.Min, r.Max)
			}
		}
		cfg.Lines = append(cfg.Lines, l)
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// nodeOrNil returns n unless the section was absent from the file.
func nodeOrNil(n *yaml.Node) *yaml.Node {
	if n.Kind == 0 {
		return nil
	}
	return n
}

func splitAddr(addr string) (string, int, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("port %q must be a number 0..65535", p)
	}
	return host, port, nil
}

// resolvePath makes a relative path relative to the configuration file.
func resolvePath(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}

// resolveScenario resolves file references; built-in names stay as they are.
func resolveScenario(baseDir, s string) string {
	if strings.ContainsAny(s, `/\`) || strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml") {
		return resolvePath(baseDir, s)
	}
	return s
}
