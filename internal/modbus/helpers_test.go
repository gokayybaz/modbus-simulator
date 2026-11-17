package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// stubHandler is a tiny thread-safe data model for protocol tests. Each area
// has 16 entries; unit 1 only; holding register 0 accepts values <= 1000.
type stubHandler struct {
	mu        sync.Mutex
	coils     [16]bool
	discretes [16]bool
	holding   [16]uint16
	input     [16]uint16
	delay     time.Duration // artificial processing time for shutdown tests
	failWith  error         // returned by every call when set
	shortRead bool          // return one value fewer than asked
}

func (s *stubHandler) pre(unit uint8, addr uint16, qty int) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.failWith != nil {
		return s.failWith
	}
	if unit != 1 {
		return GatewayTargetFailed
	}
	if int(addr)+qty > 16 {
		return fmt.Errorf("stub: %w", IllegalDataAddress)
	}
	return nil
}

func stubRead[T any](s *stubHandler, arr []T, unit uint8, addr, qty uint16) ([]T, error) {
	if err := s.pre(unit, addr, int(qty)); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := int(qty)
	if s.shortRead {
		n--
	}
	return append([]T(nil), arr[addr:int(addr)+n]...), nil
}

func (s *stubHandler) ReadCoils(u uint8, a, q uint16) ([]bool, error) {
	return stubRead(s, s.coils[:], u, a, q)
}
func (s *stubHandler) ReadDiscreteInputs(u uint8, a, q uint16) ([]bool, error) {
	return stubRead(s, s.discretes[:], u, a, q)
}
func (s *stubHandler) ReadHoldingRegisters(u uint8, a, q uint16) ([]uint16, error) {
	return stubRead(s, s.holding[:], u, a, q)
}
func (s *stubHandler) ReadInputRegisters(u uint8, a, q uint16) ([]uint16, error) {
	return stubRead(s, s.input[:], u, a, q)
}
func (s *stubHandler) WriteCoils(u uint8, a uint16, v []bool) error {
	if err := s.pre(u, a, len(v)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy(s.coils[a:], v)
	return nil
}
func (s *stubHandler) WriteHoldingRegisters(u uint8, a uint16, v []uint16) error {
	if err := s.pre(u, a, len(v)); err != nil {
		return err
	}
	if a == 0 && v[0] > 1000 {
		return IllegalDataValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy(s.holding[a:], v)
	return nil
}

// pdu builds "fc + uint16 + uint16".
func pdu(fc byte, a, b uint16) []byte {
	p := []byte{fc, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(p[1:], a)
	binary.BigEndian.PutUint16(p[3:], b)
	return p
}

// adu frames a PDU with the given transaction and unit id.
func adu(tid uint16, unit byte, p []byte) []byte {
	return appendADU(nil, mbapHeader{TransactionID: tid, UnitID: unit}, p)
}

// startServer serves h on a loopback port and registers cleanup.
func startServer(t *testing.T, h Handler, cfg Config) (*Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, h)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		srv.Close()
		if err := <-done; !errors.Is(err, ErrServerClosed) {
			t.Errorf("Serve returned %v", err)
		}
	})
	return srv, ln.Addr().String()
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { c.Close() })
	return c
}

// roundTrip writes one ADU and reads one response ADU; it returns the
// response header and PDU.
func roundTrip(t *testing.T, c net.Conn, frame []byte) (mbapHeader, []byte, error) {
	t.Helper()
	if _, err := c.Write(frame); err != nil {
		return mbapHeader{}, nil, err
	}
	return readResponse(c)
}

func readResponse(c net.Conn) (mbapHeader, []byte, error) {
	hb := make([]byte, mbapHeaderLen)
	if _, err := io.ReadFull(c, hb); err != nil {
		return mbapHeader{}, nil, err
	}
	h, err := decodeHeader(hb)
	if err != nil {
		return h, nil, err
	}
	body := make([]byte, h.pduLen())
	_, err = io.ReadFull(c, body)
	return h, body, err
}
