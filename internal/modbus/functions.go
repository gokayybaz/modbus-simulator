package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Supported function codes: FC01–FC06 (Phase 1), FC15/FC16 (Phase 4).
const (
	fcReadCoils            byte   = 0x01
	fcReadDiscreteInputs   byte   = 0x02
	fcReadHoldingRegisters byte   = 0x03
	fcReadInputRegisters   byte   = 0x04
	fcWriteSingleCoil      byte   = 0x05
	fcWriteSingleRegister  byte   = 0x06
	fcWriteMultipleCoils   byte   = 0x0F
	fcWriteMultipleRegs    byte   = 0x10
	maxReadBits                   = 2000 // FC01/FC02 quantity limit
	maxReadRegisters              = 125  // FC03/FC04 quantity limit
	maxWriteBits                  = 1968 // FC15 quantity limit (0x07B0)
	maxWriteRegisters             = 123  // FC16 quantity limit (0x007B)
	coilOn                 uint16 = 0xFF00
	coilOff                uint16 = 0x0000
)

// Handler provides the data model behind the server. Addresses are zero-based
// PDU addresses. Implementations must be safe for concurrent use: each client
// connection calls the handler from its own goroutine.
//
// Return an ExceptionCode (optionally wrapped) to send that exception to the
// client; any other error is reported as ServerDeviceFailure.
type Handler interface {
	ReadCoils(unit uint8, addr, qty uint16) ([]bool, error)
	ReadDiscreteInputs(unit uint8, addr, qty uint16) ([]bool, error)
	ReadHoldingRegisters(unit uint8, addr, qty uint16) ([]uint16, error)
	ReadInputRegisters(unit uint8, addr, qty uint16) ([]uint16, error)
	WriteCoils(unit uint8, addr uint16, values []bool) error
	WriteHoldingRegisters(unit uint8, addr uint16, values []uint16) error
}

// processPDU executes one request PDU and returns the response PDU. It always
// returns a response; err is non-nil when the response is an exception and is
// meant for logging and metrics only.
//
// Validation follows the order in the Modbus Application Protocol v1.1b3 state
// diagrams: function code (01) → quantity/value (03) → address (02) →
// execution (04).
func processPDU(h Handler, unit uint8, req []byte) (resp []byte, err error) {
	fc := req[0]
	data := req[1:]
	switch fc {
	case fcReadCoils, fcReadDiscreteInputs:
		resp, err = readBits(h, unit, fc, data)
	case fcReadHoldingRegisters, fcReadInputRegisters:
		resp, err = readRegisters(h, unit, fc, data)
	case fcWriteSingleCoil:
		resp, err = writeSingleCoil(h, unit, req)
	case fcWriteSingleRegister:
		resp, err = writeSingleRegister(h, unit, req)
	case fcWriteMultipleCoils:
		resp, err = writeMultipleCoils(h, unit, req)
	case fcWriteMultipleRegs:
		resp, err = writeMultipleRegisters(h, unit, req)
	default:
		err = fmt.Errorf("function code 0x%02X: %w", fc, IllegalFunction)
	}
	if err != nil {
		return exceptionPDU(fc, err), err
	}
	return resp, nil
}

func exceptionPDU(fc byte, err error) []byte {
	code := ServerDeviceFailure
	var ec ExceptionCode
	if errors.As(err, &ec) {
		code = ec
	}
	return []byte{fc | 0x80, byte(code)}
}

// addrQty decodes the common "starting address + quantity/value" request body.
func addrQty(data []byte) (uint16, uint16, error) {
	if len(data) != 4 {
		return 0, 0, fmt.Errorf("request data length %d, want 4: %w", len(data), IllegalDataValue)
	}
	return binary.BigEndian.Uint16(data[0:2]), binary.BigEndian.Uint16(data[2:4]), nil
}

func checkSpan(addr, qty uint16) error {
	if int(addr)+int(qty) > 0x10000 {
		return fmt.Errorf("address %d + quantity %d exceeds 65536: %w", addr, qty, IllegalDataAddress)
	}
	return nil
}

func readBits(h Handler, unit uint8, fc byte, data []byte) ([]byte, error) {
	addr, qty, err := addrQty(data)
	if err != nil {
		return nil, err
	}
	if qty < 1 || qty > maxReadBits {
		return nil, fmt.Errorf("quantity %d not in 1..%d: %w", qty, maxReadBits, IllegalDataValue)
	}
	if err := checkSpan(addr, qty); err != nil {
		return nil, err
	}
	var bits []bool
	if fc == fcReadCoils {
		bits, err = h.ReadCoils(unit, addr, qty)
	} else {
		bits, err = h.ReadDiscreteInputs(unit, addr, qty)
	}
	if err != nil {
		return nil, err
	}
	if len(bits) != int(qty) {
		return nil, fmt.Errorf("handler returned %d bits, want %d: %w", len(bits), qty, ServerDeviceFailure)
	}
	n := (int(qty) + 7) / 8
	resp := make([]byte, 2+n)
	resp[0], resp[1] = fc, byte(n)
	for i, b := range bits {
		if b {
			resp[2+i/8] |= 1 << (i % 8)
		}
	}
	return resp, nil
}

func readRegisters(h Handler, unit uint8, fc byte, data []byte) ([]byte, error) {
	addr, qty, err := addrQty(data)
	if err != nil {
		return nil, err
	}
	if qty < 1 || qty > maxReadRegisters {
		return nil, fmt.Errorf("quantity %d not in 1..%d: %w", qty, maxReadRegisters, IllegalDataValue)
	}
	if err := checkSpan(addr, qty); err != nil {
		return nil, err
	}
	var regs []uint16
	if fc == fcReadHoldingRegisters {
		regs, err = h.ReadHoldingRegisters(unit, addr, qty)
	} else {
		regs, err = h.ReadInputRegisters(unit, addr, qty)
	}
	if err != nil {
		return nil, err
	}
	if len(regs) != int(qty) {
		return nil, fmt.Errorf("handler returned %d registers, want %d: %w", len(regs), qty, ServerDeviceFailure)
	}
	resp := make([]byte, 2, 2+2*len(regs))
	resp[0], resp[1] = fc, byte(2*len(regs))
	for _, r := range regs {
		resp = binary.BigEndian.AppendUint16(resp, r)
	}
	return resp, nil
}

// writeSingleCoil handles FC05. The normal response echoes the request.
func writeSingleCoil(h Handler, unit uint8, req []byte) ([]byte, error) {
	addr, value, err := addrQty(req[1:])
	if err != nil {
		return nil, err
	}
	if value != coilOn && value != coilOff {
		return nil, fmt.Errorf("coil value 0x%04X not 0xFF00/0x0000: %w", value, IllegalDataValue)
	}
	if err := h.WriteCoils(unit, addr, []bool{value == coilOn}); err != nil {
		return nil, err
	}
	return append([]byte(nil), req...), nil
}

// writeSingleRegister handles FC06. The normal response echoes the request.
func writeSingleRegister(h Handler, unit uint8, req []byte) ([]byte, error) {
	addr, value, err := addrQty(req[1:])
	if err != nil {
		return nil, err
	}
	if err := h.WriteHoldingRegisters(unit, addr, []uint16{value}); err != nil {
		return nil, err
	}
	return append([]byte(nil), req...), nil
}

// multiHeader decodes "address, quantity, byte count, values" (FC15/FC16)
// and checks quantity limits and byte-count consistency (all → 03).
func multiHeader(req []byte, maxQty int, bytesFor func(qty int) int) (addr, qty uint16, values []byte, err error) {
	if len(req) < 6 {
		return 0, 0, nil, fmt.Errorf("request length %d too short: %w", len(req), IllegalDataValue)
	}
	addr = binary.BigEndian.Uint16(req[1:3])
	qty = binary.BigEndian.Uint16(req[3:5])
	count := int(req[5])
	values = req[6:]
	if qty < 1 || int(qty) > maxQty {
		return 0, 0, nil, fmt.Errorf("quantity %d not in 1..%d: %w", qty, maxQty, IllegalDataValue)
	}
	if count != bytesFor(int(qty)) || len(values) != count {
		return 0, 0, nil, fmt.Errorf("byte count %d / %d data bytes inconsistent with quantity %d: %w",
			count, len(values), qty, IllegalDataValue)
	}
	return addr, qty, values, checkSpan(addr, qty)
}

// writeMultipleCoils handles FC15. The response echoes address and quantity.
func writeMultipleCoils(h Handler, unit uint8, req []byte) ([]byte, error) {
	addr, qty, data, err := multiHeader(req, maxWriteBits, func(q int) int { return (q + 7) / 8 })
	if err != nil {
		return nil, err
	}
	bits := make([]bool, qty)
	for i := range bits {
		bits[i] = data[i/8]&(1<<(i%8)) != 0
	}
	if err := h.WriteCoils(unit, addr, bits); err != nil {
		return nil, err
	}
	return append([]byte(nil), req[:5]...), nil
}

// writeMultipleRegisters handles FC16. The response echoes address and quantity.
func writeMultipleRegisters(h Handler, unit uint8, req []byte) ([]byte, error) {
	addr, qty, data, err := multiHeader(req, maxWriteRegisters, func(q int) int { return 2 * q })
	if err != nil {
		return nil, err
	}
	regs := make([]uint16, qty)
	for i := range regs {
		regs[i] = binary.BigEndian.Uint16(data[2*i:])
	}
	if err := h.WriteHoldingRegisters(unit, addr, regs); err != nil {
		return nil, err
	}
	return append([]byte(nil), req[:5]...), nil
}
