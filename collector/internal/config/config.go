// Package config reads the collector settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ModbusAddr    string        // MODBUS_ADDR, host:port of the machine
	UnitIDs       []byte        // UNIT_IDS, comma separated (one per production line)
	PollInterval  time.Duration // POLL_INTERVAL
	ModbusTimeout time.Duration // MODBUS_TIMEOUT
	DatabaseURL   string        // DATABASE_URL
	LogLevel      string        // LOG_LEVEL: debug|info|warn|error
}

func Load() (Config, error) {
	c := Config{
		ModbusAddr:  env("MODBUS_ADDR", "127.0.0.1:1502"),
		DatabaseURL: env("DATABASE_URL", "postgres://collector:collector@127.0.0.1:5432/machine?sslmode=disable"),
		LogLevel:    env("LOG_LEVEL", "info"),
	}
	var err error
	if c.PollInterval, err = time.ParseDuration(env("POLL_INTERVAL", "1s")); err != nil || c.PollInterval < 100*time.Millisecond {
		return c, fmt.Errorf("POLL_INTERVAL: must be a duration >= 100ms")
	}
	if c.ModbusTimeout, err = time.ParseDuration(env("MODBUS_TIMEOUT", "2s")); err != nil || c.ModbusTimeout <= 0 {
		return c, fmt.Errorf("MODBUS_TIMEOUT: must be a positive duration")
	}
	for _, s := range strings.Split(env("UNIT_IDS", "1"), ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 || n > 247 {
			return c, fmt.Errorf("UNIT_IDS: %q is not a unit id 1..247", s)
		}
		c.UnitIDs = append(c.UnitIDs, byte(n))
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
