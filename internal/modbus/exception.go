// Package modbus implements a minimal Modbus TCP server (MBAP framing and
// function-code dispatch) on top of the Go standard library.
//
// The package knows nothing about the data it serves: register storage is
// delegated to a Handler. See docs/architecture/adr/0001 and 0005.
package modbus

import "fmt"

// ExceptionCode is a Modbus exception code. It implements error so handlers
// can return it directly or wrap it with fmt.Errorf("...: %w", code); the
// server recovers it with errors.As.
type ExceptionCode uint8

// Exception codes defined by the Modbus Application Protocol v1.1b3, §7.
const (
	IllegalFunction     ExceptionCode = 0x01
	IllegalDataAddress  ExceptionCode = 0x02
	IllegalDataValue    ExceptionCode = 0x03
	ServerDeviceFailure ExceptionCode = 0x04
	ServerDeviceBusy    ExceptionCode = 0x06
	GatewayPathUnavail  ExceptionCode = 0x0A
	GatewayTargetFailed ExceptionCode = 0x0B
)

func (e ExceptionCode) Error() string {
	switch e {
	case IllegalFunction:
		return "modbus exception 0x01: illegal function"
	case IllegalDataAddress:
		return "modbus exception 0x02: illegal data address"
	case IllegalDataValue:
		return "modbus exception 0x03: illegal data value"
	case ServerDeviceFailure:
		return "modbus exception 0x04: server device failure"
	case ServerDeviceBusy:
		return "modbus exception 0x06: server device busy"
	case GatewayPathUnavail:
		return "modbus exception 0x0A: gateway path unavailable"
	case GatewayTargetFailed:
		return "modbus exception 0x0B: gateway target device failed to respond"
	default:
		return fmt.Sprintf("modbus exception 0x%02X", uint8(e))
	}
}
