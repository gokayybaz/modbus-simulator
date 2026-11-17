package modbus

import (
	"bytes"
	"errors"
	"testing"
)

func newStub() *stubHandler {
	s := &stubHandler{}
	s.coils[1], s.coils[8], s.coils[15] = true, true, true
	s.discretes[0], s.discretes[3] = true, true
	s.holding[0], s.holding[1] = 100, 0xABCD
	s.input[0], s.input[1], s.input[2] = 2, 455, 1234
	return s
}

func TestProcessPDU(t *testing.T) {
	tests := []struct {
		name string
		unit uint8
		req  []byte
		want []byte
	}{
		// Normal responses.
		{"FC01 16 coils", 1, pdu(0x01, 0, 16), []byte{0x01, 2, 0b0000_0010, 0b1000_0001}},
		{"FC01 3 coils partial byte", 1, pdu(0x01, 0, 3), []byte{0x01, 1, 0b010}},
		{"FC02 4 inputs", 1, pdu(0x02, 0, 4), []byte{0x02, 1, 0b1001}},
		{"FC03 2 regs", 1, pdu(0x03, 0, 2), []byte{0x03, 4, 0x00, 0x64, 0xAB, 0xCD}},
		{"FC04 3 regs", 1, pdu(0x04, 0, 3), []byte{0x04, 6, 0, 2, 0x01, 0xC7, 0x04, 0xD2}},
		{"FC04 last register", 1, pdu(0x04, 15, 1), []byte{0x04, 2, 0, 0}},
		{"FC05 on echoes", 1, pdu(0x05, 3, 0xFF00), pdu(0x05, 3, 0xFF00)},
		{"FC05 off echoes", 1, pdu(0x05, 1, 0x0000), pdu(0x05, 1, 0x0000)},
		{"FC06 echoes", 1, pdu(0x06, 2, 777), pdu(0x06, 2, 777)},

		// 01 Illegal function.
		{"FC07 serial-only", 1, []byte{0x07}, []byte{0x87, 0x01}},
		{"FC17 read/write multiple", 1, []byte{0x17, 0, 0, 0, 1, 0, 0, 0, 1, 2, 0, 1}, []byte{0x97, 0x01}},

		// FC15 / FC16 normal responses echo address + quantity.
		{"FC0F 10 coils", 1, []byte{0x0F, 0, 2, 0, 10, 2, 0b1010_0101, 0b10}, []byte{0x0F, 0, 2, 0, 10}},
		{"FC10 2 regs", 1, []byte{0x10, 0, 4, 0, 2, 4, 0x12, 0x34, 0xAB, 0xCD}, []byte{0x10, 0, 4, 0, 2}},
		// FC15 / FC16 validation.
		{"FC0F qty 0", 1, []byte{0x0F, 0, 0, 0, 0, 0}, []byte{0x8F, 0x03}},
		{"FC0F qty 1969", 1, []byte{0x0F, 0, 0, 0x07, 0xB1, 247}, []byte{0x8F, 0x03}},
		{"FC0F byte count mismatch", 1, []byte{0x0F, 0, 0, 0, 10, 1, 0xFF}, []byte{0x8F, 0x03}},
		{"FC0F missing data", 1, []byte{0x0F, 0, 0, 0, 10, 2, 0xFF}, []byte{0x8F, 0x03}},
		{"FC0F too short", 1, []byte{0x0F, 0, 0, 0}, []byte{0x8F, 0x03}},
		{"FC0F past end", 1, []byte{0x0F, 0, 15, 0, 2, 1, 3}, []byte{0x8F, 0x02}},
		{"FC10 qty 124", 1, append([]byte{0x10, 0, 0, 0, 124, 248}, make([]byte, 248)...)[:254-1], []byte{0x90, 0x03}},
		{"FC10 byte count odd", 1, []byte{0x10, 0, 0, 0, 1, 1, 0}, []byte{0x90, 0x03}},
		{"FC10 past end", 1, []byte{0x10, 0, 15, 0, 2, 4, 0, 1, 0, 1}, []byte{0x90, 0x02}},
		{"FC10 rejected value", 1, []byte{0x10, 0, 0, 0, 1, 2, 0x13, 0x88}, []byte{0x90, 0x03}},
		{"FC2B MEI", 1, []byte{0x2B, 0x0E, 1, 0}, []byte{0xAB, 0x01}},
		{"exception-bit FC", 1, []byte{0x81, 0, 0, 0, 1}, []byte{0x81, 0x01}},

		// 03 Illegal data value: quantity, coil value, malformed length.
		{"FC01 qty 0", 1, pdu(0x01, 0, 0), []byte{0x81, 0x03}},
		{"FC02 qty 2001", 1, pdu(0x02, 0, 2001), []byte{0x82, 0x03}},
		{"FC03 qty 0", 1, pdu(0x03, 0, 0), []byte{0x83, 0x03}},
		{"FC04 qty 126", 1, pdu(0x04, 0, 126), []byte{0x84, 0x03}},
		{"FC05 value 0x1234", 1, pdu(0x05, 0, 0x1234), []byte{0x85, 0x03}},
		{"FC05 value 0x00FF", 1, pdu(0x05, 0, 0x00FF), []byte{0x85, 0x03}},
		{"FC04 short body", 1, []byte{0x04, 0, 0, 0}, []byte{0x84, 0x03}},
		{"FC03 long body", 1, []byte{0x03, 0, 0, 0, 1, 9}, []byte{0x83, 0x03}},
		{"FC06 value rejected by handler", 1, pdu(0x06, 0, 5000), []byte{0x86, 0x03}},
		// Quantity is checked before address (spec order).
		{"FC04 qty 0 at bad address", 1, pdu(0x04, 999, 0), []byte{0x84, 0x03}},

		// 02 Illegal data address.
		{"FC03 past end", 1, pdu(0x03, 15, 2), []byte{0x83, 0x02}},
		{"FC04 far address", 1, pdu(0x04, 1000, 1), []byte{0x84, 0x02}},
		{"FC04 span past 65535", 1, pdu(0x04, 0xFFFF, 2), []byte{0x84, 0x02}},
		{"FC01 span past 65535", 1, pdu(0x01, 0xFFF0, 100), []byte{0x81, 0x02}},
		{"FC05 bad address", 1, pdu(0x05, 16, 0xFF00), []byte{0x85, 0x02}},
		{"FC06 bad address", 1, pdu(0x06, 100, 1), []byte{0x86, 0x02}},

		// 0B unknown unit.
		{"FC04 unknown unit", 9, pdu(0x04, 0, 1), []byte{0x84, 0x0B}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processPDU(newStub(), tt.unit, tt.req)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("response % X, want % X", got, tt.want)
			}
			if isErr := got[0]&0x80 != 0; isErr != (err != nil) {
				t.Fatalf("err = %v for response % X", err, got)
			}
		})
	}
}

func TestProcessPDUWritesTakeEffect(t *testing.T) {
	s := newStub()
	processPDU(s, 1, pdu(0x05, 4, 0xFF00))
	processPDU(s, 1, pdu(0x06, 3, 4242))
	if !s.coils[4] || s.holding[3] != 4242 {
		t.Fatalf("coil4=%v hr3=%d", s.coils[4], s.holding[3])
	}
	// FC15: coils 2..11 = 1,0,1,0,0,1,0,1 | 0,1 (LSB first).
	processPDU(s, 1, []byte{0x0F, 0, 2, 0, 10, 2, 0b1010_0101, 0b10})
	want := []bool{true, false, true, false, false, true, false, true, false, true}
	for i, w := range want {
		if s.coils[2+i] != w {
			t.Fatalf("FC15 coil %d = %v", 2+i, s.coils[2+i])
		}
	}
	// FC16 writes both registers.
	processPDU(s, 1, []byte{0x10, 0, 4, 0, 2, 4, 0x12, 0x34, 0xAB, 0xCD})
	if s.holding[4] != 0x1234 || s.holding[5] != 0xABCD {
		t.Fatalf("FC16 %X %X", s.holding[4], s.holding[5])
	}
	// A rejected write must not change memory.
	processPDU(s, 1, pdu(0x06, 0, 5000))
	if s.holding[0] != 100 {
		t.Fatalf("hr0 changed to %d", s.holding[0])
	}
}

func TestProcessPDUHandlerFailures(t *testing.T) {
	s := newStub()
	s.failWith = errors.New("disk on fire")
	if got, err := processPDU(s, 1, pdu(0x03, 0, 1)); !bytes.Equal(got, []byte{0x83, 0x04}) || err == nil {
		t.Fatalf("plain error: got % X, %v", got, err)
	}
	s = newStub()
	s.shortRead = true
	if got, _ := processPDU(s, 1, pdu(0x04, 0, 3)); !bytes.Equal(got, []byte{0x84, 0x04}) {
		t.Fatalf("short read: got % X", got)
	}
	if got, _ := processPDU(s, 1, pdu(0x02, 0, 3)); !bytes.Equal(got, []byte{0x82, 0x04}) {
		t.Fatalf("short bit read: got % X", got)
	}
}

func TestMaxQuantityResponsesFitPDU(t *testing.T) {
	h := &bigHandler{}
	for _, req := range [][]byte{pdu(0x01, 0, 2000), pdu(0x02, 0, 2000), pdu(0x03, 0, 125), pdu(0x04, 0, 125)} {
		got, err := processPDU(h, 1, req)
		if err != nil || len(got) > maxPDULen {
			t.Fatalf("FC%02X: len %d err %v", req[0], len(got), err)
		}
	}
}

// bigHandler serves any address with zero values.
type bigHandler struct{}

func (bigHandler) ReadCoils(_ uint8, _, q uint16) ([]bool, error) { return make([]bool, q), nil }
func (bigHandler) ReadDiscreteInputs(_ uint8, _, q uint16) ([]bool, error) {
	return make([]bool, q), nil
}
func (bigHandler) ReadHoldingRegisters(_ uint8, _, q uint16) ([]uint16, error) {
	return make([]uint16, q), nil
}
func (bigHandler) ReadInputRegisters(_ uint8, _, q uint16) ([]uint16, error) {
	return make([]uint16, q), nil
}
func (bigHandler) WriteCoils(uint8, uint16, []bool) error              { return nil }
func (bigHandler) WriteHoldingRegisters(uint8, uint16, []uint16) error { return nil }

func FuzzProcessPDU(f *testing.F) {
	f.Add(byte(1), []byte{0x04, 0, 0, 0, 3})
	f.Add(byte(1), []byte{0x05, 0, 1, 0xFF, 0})
	f.Add(byte(9), []byte{0x03, 0xFF, 0xFF, 0, 125})
	f.Add(byte(1), []byte{0x2B})
	f.Fuzz(func(t *testing.T, unit byte, req []byte) {
		if len(req) < 1 || len(req) > maxPDULen {
			t.Skip()
		}
		for _, h := range []Handler{newStub(), bigHandler{}} {
			resp, err := processPDU(h, unit, req)
			if len(resp) < 2 || len(resp) > maxPDULen {
				t.Fatalf("response length %d", len(resp))
			}
			if resp[0]&0x7F != req[0]&0x7F {
				t.Fatalf("response FC % X for request FC % X", resp[0], req[0])
			}
			if (resp[0]&0x80 != 0) != (err != nil) {
				t.Fatalf("exception flag/err mismatch: % X %v", resp, err)
			}
		}
	})
}
