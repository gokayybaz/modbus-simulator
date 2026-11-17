package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/simonvetter/modbus"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:1502", "listen address")
	unit := flag.Uint("unit", 1, "Modbus unit id")
	flag.Parse()

	mem := NewMemory(uint8(*unit), 16, 16, 16, 32)
	// Sample data mirroring the planned input-register map.
	mem.SetInputs(0, []uint16{2, 455, 1234}) // state=RUNNING, rpm=45.5, current=123.4
	mem.SetInputs(16, U32ToRegs(123456))     // total length 12345.6 m (scale 10)
	mem.SetInputs(20, F32ToRegs(3.14159))    // float32 sample

	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        "tcp://" + *addr,
		Timeout:    30 * time.Second,
		MaxClients: 16,
	}, mem)
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("PoC Modbus TCP server listening on %s (unit %d)\n", *addr, *unit)

	// Fake "simulation tick": bump IR1 every 500 ms.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	var rpm uint16 = 455
	for {
		select {
		case <-t.C:
			rpm++
			mem.SetInputs(1, []uint16{rpm})
		case <-stop:
			_ = srv.Stop()
			fmt.Println("stopped")
			return
		}
	}
}
