// Command simulator runs the Industrial Pipe Extrusion Modbus TCP simulator:
// one or more real-time process simulations of pipe extrusion lines, each
// exposed as a virtual PLC over Modbus TCP according to a YAML register map.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"modbus-simulator/internal/config"
	"modbus-simulator/internal/modbus"
	"modbus-simulator/internal/plant"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr, nil); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "simulator:", err)
		os.Exit(1)
	}
}

// endpoint is one Modbus TCP listener serving one or more lines.
type endpoint struct {
	addr   string
	router *plant.Router
	ln     net.Listener
	srv    *modbus.Server
	lines  []int
}

// run starts the simulator and blocks until ctx is cancelled or a component
// fails. onReady, if non-nil, receives the bound address of every line (in
// line order) once all endpoints are accepting connections. With -validate it
// checks the whole configuration and returns without starting anything.
func run(ctx context.Context, args []string, logOut io.Writer, onReady func(addrs []string)) error {
	cfg, err := config.Parse(args, logOut)
	if err != nil {
		return err
	}
	logger := newLogger(logOut, cfg)

	// Build every line (presets, overrides, register maps, scenarios) before
	// anything starts.
	lines, err := plant.Build(cfg, logger)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	if cfg.ValidateOnly {
		for _, l := range lines {
			logger.Info("line valid", "line", l.Spec.ID, "name", l.Spec.Name, "preset", l.Spec.Preset,
				"addr", l.Spec.Addr, "unit_id", l.Spec.UnitID, "scenario", l.Scenario)
		}
		logger.Info("configuration valid", "lines", len(lines), "file", cfg.File)
		return nil
	}

	// Endpoints: unit isolation shares one per address; port isolation gives
	// every line its own (also with port 0, where the OS picks each port).
	var endpoints []*endpoint
	byAddr := map[string]*endpoint{}
	for _, line := range lines {
		ep := byAddr[line.Spec.Addr]
		if ep == nil || cfg.Isolation == "port" {
			ep = &endpoint{addr: line.Spec.Addr, router: plant.NewRouter()}
			endpoints = append(endpoints, ep)
			byAddr[line.Spec.Addr] = ep
		}
		if cfg.AcceptAnyUnit {
			err = ep.router.SetDefault(line.Device)
		} else {
			err = ep.router.Add(line.Spec.UnitID, line.Device)
		}
		if err != nil {
			return fmt.Errorf("line %d: %w", line.Spec.ID, err)
		}
		ep.lines = append(ep.lines, line.Spec.ID)
	}

	// Bind every endpoint before starting anything.
	for _, ep := range endpoints {
		if ep.ln, err = net.Listen("tcp", ep.addr); err != nil {
			for _, other := range endpoints {
				if other.ln != nil {
					other.ln.Close()
				}
			}
			return fmt.Errorf("listen on %s: %w", ep.addr, err)
		}
		ep.srv = modbus.NewServer(modbus.Config{
			MaxClients:  cfg.MaxClients,
			IdleTimeout: cfg.IdleTimeout,
			Logger:      logger.With("component", "modbus", "endpoint", ep.ln.Addr().String()),
		}, ep.router)
	}

	simCtx, stopSim := context.WithCancel(context.Background())
	defer stopSim()
	var simWG sync.WaitGroup
	failed := make(chan error, len(lines)+len(endpoints))
	for _, l := range lines {
		simWG.Go(func() {
			if err := l.Run(simCtx); err != nil {
				failed <- fmt.Errorf("line %d simulation: %w", l.Spec.ID, err)
			}
		})
	}
	var srvWG sync.WaitGroup
	for _, ep := range endpoints {
		srvWG.Go(func() {
			if err := ep.srv.Serve(ep.ln); !errors.Is(err, modbus.ErrServerClosed) {
				failed <- fmt.Errorf("modbus server %s: %w", ep.ln.Addr(), err)
			}
		})
	}

	addrs := make([]string, 0, len(lines))
	for _, ep := range endpoints {
		logger.Info("modbus server listening", "addr", ep.ln.Addr().String(), "lines", ep.lines,
			"max_clients", cfg.MaxClients)
	}
	for _, line := range lines {
		addr := endpointOf(endpoints, line.Spec.ID).ln.Addr().String()
		addrs = append(addrs, addr)
		logger.Info("line ready", "line", line.Spec.ID, "name", line.Spec.Name, "preset", line.Spec.Preset,
			"addr", addr, "unit_id", line.Spec.UnitID, "accept_any_unit", line.Spec.AcceptAnyUnit,
			"seed", line.Spec.Sim.Seed, "scenario", line.Scenario)
	}
	l := lines[0].Spec.Map.Layout()
	logger.Info("simulator started", "lines", len(lines), "isolation", cfg.Isolation, "config", cfg.File,
		"input_registers", l.InputRegisters, "holding_registers", l.HoldingRegisters,
		"coils", l.Coils, "discrete_inputs", l.DiscreteInputs,
		"sim_period", cfg.SimPeriod, "time_scale", cfg.TimeScale)
	if onReady != nil {
		onReady(addrs)
	}

	var runErr error
	select {
	case runErr = <-failed:
	case <-ctx.Done():
	}

	logger.Info("shutting down", "timeout", cfg.ShutdownTimeout)
	shCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	for _, ep := range endpoints {
		if err := ep.srv.Shutdown(shCtx); err != nil {
			logger.Warn("shutdown incomplete, connections closed forcibly", "endpoint", ep.ln.Addr().String(), "err", err)
		}
	}
	srvWG.Wait()
	stopSim()
	simWG.Wait()
	if runErr != nil {
		return runErr
	}
	logger.Info("shutdown complete")
	return nil
}

func endpointOf(eps []*endpoint, line int) *endpoint {
	for _, ep := range eps {
		for _, id := range ep.lines {
			if id == line {
				return ep
			}
		}
	}
	return nil
}

func newLogger(w io.Writer, cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
