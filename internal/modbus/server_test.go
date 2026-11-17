package modbus

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	gx "github.com/grid-x/modbus"
)

// Exceptions must be answered on the same connection, which stays usable.
func TestExceptionsKeepConnectionOpen(t *testing.T) {
	_, addr := startServer(t, newStub(), Config{})
	c := dial(t, addr)

	steps := []struct {
		unit byte
		req  []byte
		want []byte
	}{
		{1, pdu(0x04, 0, 0), []byte{0x84, 0x03}},      // qty 0
		{1, pdu(0x03, 0, 126), []byte{0x83, 0x03}},    // qty too large
		{1, pdu(0x05, 0, 0x1234), []byte{0x85, 0x03}}, // bad coil value
		{1, pdu(0x04, 100, 1), []byte{0x84, 0x02}},    // bad address
		{1, []byte{0x2B, 0x0E, 1, 0}, []byte{0xAB, 0x01}},
		{7, pdu(0x04, 0, 1), []byte{0x84, 0x0B}},
		{1, pdu(0x04, 1, 1), []byte{0x04, 2, 0x01, 0xC7}}, // still works
	}
	for i, s := range steps {
		tid := uint16(100 + i)
		h, got, err := roundTrip(t, c, adu(tid, s.unit, s.req))
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if h.TransactionID != tid || h.UnitID != s.unit || h.ProtocolID != 0 {
			t.Fatalf("step %d: header %+v", i, h)
		}
		if !bytes.Equal(got, s.want) {
			t.Fatalf("step %d: got % X want % X", i, got, s.want)
		}
	}
}

// Several requests sent back-to-back are answered in order.
func TestPipelinedRequests(t *testing.T) {
	_, addr := startServer(t, newStub(), Config{})
	c := dial(t, addr)
	var batch []byte
	for i := range 10 {
		batch = append(batch, adu(uint16(i), 1, pdu(0x04, uint16(i), 1))...)
	}
	if _, err := c.Write(batch); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		h, _, err := readResponse(c)
		if err != nil || h.TransactionID != uint16(i) {
			t.Fatalf("response %d: tid %d err %v", i, h.TransactionID, err)
		}
	}
}

// A frame with a non-Modbus protocol id is consumed and ignored.
func TestNonModbusProtocolIDIgnored(t *testing.T) {
	_, addr := startServer(t, newStub(), Config{})
	c := dial(t, addr)
	bad := adu(1, 1, pdu(0x04, 0, 1))
	binary.BigEndian.PutUint16(bad[2:], 0x1234)
	good := adu(2, 1, pdu(0x04, 0, 1))
	if _, err := c.Write(append(bad, good...)); err != nil {
		t.Fatal(err)
	}
	h, _, err := readResponse(c)
	if err != nil || h.TransactionID != 2 {
		t.Fatalf("got tid %d err %v, want only the response to tid 2", h.TransactionID, err)
	}
}

// An invalid length field cannot be resynchronised: the connection closes.
func TestInvalidLengthClosesConnection(t *testing.T) {
	_, addr := startServer(t, newStub(), Config{})
	for _, frame := range [][]byte{
		{0, 1, 0, 0, 0, 1, 1},          // length 1: no function code
		{0, 1, 0, 0, 0x01, 0x00, 1, 4}, // length 256
	} {
		c := dial(t, addr)
		if _, err := c.Write(frame); err != nil {
			t.Fatal(err)
		}
		// Closing with unread bytes in the receive buffer yields RST instead of FIN.
		_, err := c.Read(make([]byte, 1))
		var ne net.Error
		if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
			t.Fatalf("frame % X: want closed connection, got %v", frame, err)
		}
	}
}

func TestMaxClients(t *testing.T) {
	srv, addr := startServer(t, newStub(), Config{MaxClients: 2})
	c1, c2 := dial(t, addr), dial(t, addr)
	for _, c := range []net.Conn{c1, c2} {
		if _, _, err := roundTrip(t, c, adu(1, 1, pdu(0x04, 0, 1))); err != nil {
			t.Fatal(err)
		}
	}
	c3 := dial(t, addr)
	if _, _, err := roundTrip(t, c3, adu(1, 1, pdu(0x04, 0, 1))); err == nil {
		t.Fatal("third client was served")
	}
	if n := srv.ClientCount(); n != 2 {
		t.Fatalf("ClientCount = %d", n)
	}
	// Freeing a slot admits a new client.
	c1.Close()
	waitFor(t, func() bool { return srv.ClientCount() == 1 })
	if _, _, err := roundTrip(t, dial(t, addr), adu(1, 1, pdu(0x04, 0, 1))); err != nil {
		t.Fatalf("after slot freed: %v", err)
	}
}

func TestIdleTimeout(t *testing.T) {
	srv, addr := startServer(t, newStub(), Config{IdleTimeout: 100 * time.Millisecond})
	c := dial(t, addr)
	if _, _, err := roundTrip(t, c, adu(1, 1, pdu(0x04, 0, 1))); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after idle timeout, got %v", err)
	}
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Fatalf("closed after %v, before idle timeout", d)
	}
	waitFor(t, func() bool { return srv.ClientCount() == 0 })
}

// Shutdown lets an in-flight request finish and its response reach the client.
func TestShutdownCompletesInFlightRequest(t *testing.T) {
	h := newStub()
	h.delay = 200 * time.Millisecond
	srv, addr := startServer(t, h, Config{})
	busy := dial(t, addr)
	idle := dial(t, addr)
	waitFor(t, func() bool { return srv.ClientCount() == 2 })

	if _, err := busy.Write(adu(9, 1, pdu(0x04, 1, 1))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // request is now inside the handler

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	hd, got, err := readResponse(busy)
	if err != nil || hd.TransactionID != 9 || !bytes.Equal(got, []byte{0x04, 2, 0x01, 0xC7}) {
		t.Fatalf("in-flight response: %+v % X %v", hd, got, err)
	}
	for _, c := range []net.Conn{busy, idle} {
		if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("want EOF after shutdown, got %v", err)
		}
	}
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatal("listener still accepting after Shutdown")
	}
}

// When the shutdown context expires, remaining connections are force-closed.
func TestShutdownTimeoutForcesClose(t *testing.T) {
	h := newStub()
	h.delay = time.Second
	srv, addr := startServer(t, h, Config{})
	c := dial(t, addr)
	if _, err := c.Write(adu(1, 1, pdu(0x04, 0, 1))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Shutdown took %v", d)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{}, newStub())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	var conns []net.Conn
	for range 8 {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	waitFor(t, func() bool { return srv.ClientCount() == 8 })
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve: %v", err)
	}
	for _, c := range conns {
		c.Close()
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before })
}

func TestServeAfterShutdown(t *testing.T) {
	srv := NewServer(Config{}, newStub())
	_ = srv.Shutdown(context.Background())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Serve(ln); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve = %v", err)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("listener not closed")
	}
}

// Interop with an independent client implementation (grid-x/modbus).
func TestInteropGridX(t *testing.T) {
	_, addr := startServer(t, newStub(), Config{})
	ctx := context.Background()
	h := gx.NewTCPClientHandler(addr)
	h.SlaveID = 1
	h.Timeout = 2 * time.Second
	if err := h.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	c := gx.NewClient(h)

	if b, err := c.ReadInputRegisters(ctx, 0, 3); err != nil || !bytes.Equal(b, []byte{0, 2, 0x01, 0xC7, 0x04, 0xD2}) {
		t.Fatalf("FC04: % X %v", b, err)
	}
	if _, err := c.WriteSingleRegister(ctx, 5, 800); err != nil {
		t.Fatalf("FC06: %v", err)
	}
	if b, err := c.ReadHoldingRegisters(ctx, 5, 1); err != nil || binary.BigEndian.Uint16(b) != 800 {
		t.Fatalf("FC03: % X %v", b, err)
	}
	if _, err := c.WriteSingleCoil(ctx, 9, 0xFF00); err != nil {
		t.Fatalf("FC05: %v", err)
	}
	if b, err := c.ReadCoils(ctx, 0, 16); err != nil || !bytes.Equal(b, []byte{0x02, 0x83}) {
		t.Fatalf("FC01: % X %v", b, err)
	}
	if b, err := c.ReadDiscreteInputs(ctx, 0, 4); err != nil || !bytes.Equal(b, []byte{0x09}) {
		t.Fatalf("FC02: % X %v", b, err)
	}
	// FC15 / FC16.
	if _, err := c.WriteMultipleCoils(ctx, 10, 3, []byte{0b101}); err != nil {
		t.Fatalf("FC15: %v", err)
	}
	if b, err := c.ReadCoils(ctx, 8, 8); err != nil || b[0] != 0b1001_0111 { // 8, 15 preset; 9 from FC05; 10, 12 from FC15
		t.Fatalf("FC01 after FC15: %08b %v", b, err)
	}
	if _, err := c.WriteMultipleRegisters(ctx, 6, 2, []byte{0x00, 0x01, 0xE2, 0x40}); err != nil {
		t.Fatalf("FC16: %v", err)
	}
	if b, err := c.ReadHoldingRegisters(ctx, 6, 2); err != nil || binary.BigEndian.Uint32(b) != 123456 {
		t.Fatalf("FC03 after FC16: % X %v", b, err)
	}
	// Exceptions surface as client errors and the client keeps working.
	_, err := c.ReadInputRegisters(ctx, 100, 1)
	var mbErr *gx.Error
	if !errors.As(err, &mbErr) || mbErr.ExceptionCode != 0x02 {
		t.Fatalf("want exception 02, got %v", err)
	}
	if _, err := c.WriteSingleRegister(ctx, 0, 5000); !errors.As(err, &mbErr) || mbErr.ExceptionCode != 0x03 {
		t.Fatalf("want exception 03, got %v", err)
	}
	if _, err := c.ReadInputRegisters(ctx, 0, 1); err != nil {
		t.Fatalf("after exceptions: %v", err)
	}
}

func TestConcurrentClients(t *testing.T) {
	_, addr := startServer(t, newStub(), Config{MaxClients: 64})
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := range 32 {
		wg.Go(func() {
			h := gx.NewTCPClientHandler(addr)
			h.SlaveID = 1
			h.Timeout = 5 * time.Second
			if err := h.Connect(context.Background()); err != nil {
				errs <- err
				return
			}
			defer h.Close()
			c := gx.NewClient(h)
			for j := range 100 {
				if i%4 == 0 {
					if _, err := c.WriteSingleRegister(context.Background(), uint16(2+i%8), uint16(j)); err != nil {
						errs <- err
						return
					}
					continue
				}
				b, err := c.ReadInputRegisters(context.Background(), 0, 3)
				if err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(b, []byte{0, 2, 0x01, 0xC7, 0x04, 0xD2}) {
					errs <- errors.New("unexpected data")
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 3s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
