package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	gx "github.com/grid-x/modbus"
	"github.com/simonvetter/modbus"
)

const unitID = 1

// startServer starts the PoC server on a free loopback port.
func startServer(t *testing.T) (*Memory, *modbus.ModbusServer, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	mem := NewMemory(unitID, 16, 16, 16, 32)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL: "tcp://" + addr, Timeout: 5 * time.Second, MaxClients: 64,
	}, mem)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return mem, srv, addr
}

func gridxClient(t *testing.T, addr string, unit byte) gx.Client {
	t.Helper()
	h := gx.NewTCPClientHandler(addr)
	h.SlaveID = unit
	h.Timeout = 2 * time.Second
	if err := h.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return gx.NewClient(h)
}

// rawRequest sends one MBAP-framed PDU and returns the response PDU
// (function code + data), or io.EOF if the server closed the connection.
func rawRequest(t *testing.T, addr string, unit byte, pdu []byte) ([]byte, error) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	adu := make([]byte, 7, 7+len(pdu))
	binary.BigEndian.PutUint16(adu[0:], 0x1234)             // transaction id
	binary.BigEndian.PutUint16(adu[2:], 0)                  // protocol id
	binary.BigEndian.PutUint16(adu[4:], uint16(len(pdu)+1)) // length
	adu[6] = unit
	adu = append(adu, pdu...)
	if _, err := c.Write(adu); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 7)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}
	if tid := binary.BigEndian.Uint16(hdr[0:]); tid != 0x1234 {
		t.Fatalf("transaction id not echoed: %#x", tid)
	}
	if hdr[6] != unit {
		t.Fatalf("unit id not echoed: %d", hdr[6])
	}
	body := make([]byte, int(binary.BigEndian.Uint16(hdr[4:]))-1)
	_, err = io.ReadFull(c, body)
	return body, err
}

func pdu(fc byte, a, b uint16) []byte {
	p := []byte{fc, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(p[1:], a)
	binary.BigEndian.PutUint16(p[3:], b)
	return p
}

// --- Happy path FC01..FC06 with an independent client library ---

func TestFC01toFC06WithGridXClient(t *testing.T) {
	mem, _, addr := startServer(t)
	mem.SetInputs(0, []uint16{2, 455, 1234})
	mem.SetDiscrete(3, true)
	c := gridxClient(t, addr, unitID)
	ctx := context.Background()

	// FC04
	b, err := c.ReadInputRegisters(ctx, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := []uint16{binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:]), binary.BigEndian.Uint16(b[4:])}; got[0] != 2 || got[1] != 455 || got[2] != 1234 {
		t.Fatalf("FC04 got %v", got)
	}
	// FC06 + FC03
	if _, err := c.WriteSingleRegister(ctx, 5, 800); err != nil {
		t.Fatal(err)
	}
	if b, err = c.ReadHoldingRegisters(ctx, 5, 1); err != nil || binary.BigEndian.Uint16(b) != 800 {
		t.Fatalf("FC03 got %v %v", b, err)
	}
	// FC05 + FC01 (coil 9 -> bit 1 of byte 1)
	if _, err := c.WriteSingleCoil(ctx, 9, 0xFF00); err != nil {
		t.Fatal(err)
	}
	if b, err = c.ReadCoils(ctx, 0, 16); err != nil || b[1] != 0x02 || b[0] != 0 {
		t.Fatalf("FC01 got %08b %v", b, err)
	}
	// FC02
	if b, err = c.ReadDiscreteInputs(ctx, 0, 8); err != nil || b[0] != 0x08 {
		t.Fatalf("FC02 got %08b %v", b, err)
	}
}

// --- 32-bit encoding: high word first, big-endian bytes ("ABCD") ---

func TestThirtyTwoBitWordOrder(t *testing.T) {
	mem, _, addr := startServer(t)
	mem.SetInputs(16, U32ToRegs(123456))
	mem.SetInputs(20, F32ToRegs(3.14159))
	c := gridxClient(t, addr, unitID)

	b, err := c.ReadInputRegisters(context.Background(), 16, 6)
	if err != nil {
		t.Fatal(err)
	}
	if v := binary.BigEndian.Uint32(b[0:4]); v != 123456 {
		t.Fatalf("UINT32 ABCD decode = %d", v)
	}
	if f := math.Float32frombits(binary.BigEndian.Uint32(b[8:12])); math.Abs(float64(f)-3.14159) > 1e-6 {
		t.Fatalf("FLOAT32 ABCD decode = %v", f)
	}
	// Raw byte layout for 123456 = 0x0001E240 -> regs [0x0001, 0xE240]
	want := []byte{0x00, 0x01, 0xE2, 0x40}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("byte layout %x, want %x", b[:4], want)
		}
	}
}

func TestScaleU16(t *testing.T) {
	cases := []struct {
		v, scale float64
		want     uint16
		ok       bool
	}{
		{45.5, 10, 455, true},
		{45.54, 10, 455, true},
		{45.56, 10, 456, true},
		{-1, 10, 0, false},
		{7000, 10, 65535, false},
		{math.NaN(), 10, 0, false},
		{1.234, 100, 123, true},
	}
	for _, c := range cases {
		got, ok := ScaleU16(c.v, c.scale)
		if got != c.want || ok != c.ok {
			t.Errorf("ScaleU16(%v,%v)=%d,%v want %d,%v", c.v, c.scale, got, ok, c.want, c.ok)
		}
	}
	if I16ToReg(-5) != 0xFFFB {
		t.Error("INT16 two's complement mismatch")
	}
}

// --- Exceptions and edge cases, verified with raw frames ---

func TestExceptionsRawFrames(t *testing.T) {
	_, _, addr := startServer(t)

	cases := []struct {
		name    string
		unit    byte
		pdu     []byte
		wantExc byte // 0 = expect normal response
		wantEOF bool // server closes connection
	}{
		{"FC04 out of range -> 02", unitID, pdu(0x04, 30, 5), 0x02, false},
		{"FC03 out of range -> 02", unitID, pdu(0x03, 16, 1), 0x02, false},
		{"FC06 to unmapped -> 02", unitID, pdu(0x06, 100, 1), 0x02, false},
		{"FC06 value > limit -> 03", unitID, pdu(0x06, 0, 5000), 0x03, false},
		{"unsupported FC 0x2B -> 01", unitID, []byte{0x2B, 0x0E, 0x01, 0x00}, 0x01, false},
		{"FC07 (serial-only) -> 01", unitID, []byte{0x07}, 0x01, false},
		{"wrong unit id -> 0B", 9, pdu(0x04, 0, 1), 0x0B, false},
		// Spec (Modbus App Protocol v1.1b3 §6.x) says these should be exception 03,
		// the library instead treats them as protocol errors and drops the link.
		{"FC04 quantity 0 (spec: 03)", unitID, pdu(0x04, 0, 0), 0, true},
		{"FC03 quantity 126 (spec: 03)", unitID, pdu(0x03, 0, 126), 0, true},
		{"FC05 value 0x1234 (spec: 03)", unitID, pdu(0x05, 0, 0x1234), 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := rawRequest(t, addr, c.unit, c.pdu)
			if c.wantEOF {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("expected connection close, got resp=%x err=%v", resp, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resp[0] != c.pdu[0]|0x80 || resp[1] != c.wantExc {
				t.Fatalf("got %x, want exception fc=%#x code=%#x", resp, c.pdu[0]|0x80, c.wantExc)
			}
		})
	}
}

// --- Concurrency: many clients reading while the "simulation" writes ---

func TestConcurrentClients(t *testing.T) {
	mem, _, addr := startServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Writer publishes a 2-register value whose halves must stay consistent:
	// regs[16]=n, regs[17]=n (a torn read would show different halves).
	go func() {
		for n := uint16(0); ctx.Err() == nil; n++ {
			mem.SetInputs(16, []uint16{n, n})
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := gx.NewTCPClientHandler(addr)
			h.SlaveID = unitID
			h.Timeout = 2 * time.Second
			if err := h.Connect(ctx); err != nil {
				errs <- err
				return
			}
			defer h.Close()
			c := gx.NewClient(h)
			for j := 0; j < 200; j++ {
				b, err := c.ReadInputRegisters(ctx, 16, 2)
				if err != nil {
					errs <- err
					return
				}
				if b[0] != b[2] || b[1] != b[3] {
					errs <- errors.New("torn multi-register read")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// --- Shutdown: Stop() closes client connections; check goroutines settle ---

func TestStopReleasesGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	_, srv, addr := startServer(t)
	conns := make([]net.Conn, 5)
	for i := range conns {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = c
	}
	time.Sleep(50 * time.Millisecond)
	if err := srv.Stop(); err != nil {
		t.Fatal(err)
	}
	// Stop() does not wait for handler goroutines; poll until they exit.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines before=%d after=%d", before, n)
	}
	for _, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatal("expected closed connection after Stop")
		}
		c.Close()
	}
}
