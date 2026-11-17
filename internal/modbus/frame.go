package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Modbus TCP framing limits (Modbus Messaging on TCP/IP Implementation Guide v1.0b).
const (
	mbapHeaderLen = 7   // transaction id (2) + protocol id (2) + length (2) + unit id (1)
	maxPDULen     = 253 // function code + data
	maxADULen     = mbapHeaderLen + maxPDULen
)

var errBadLength = errors.New("invalid MBAP length field")

// mbapHeader is the 7-byte Modbus Application Protocol header.
// Length counts the unit id byte plus the PDU.
type mbapHeader struct {
	TransactionID uint16
	ProtocolID    uint16
	Length        uint16
	UnitID        uint8
}

// pduLen is the number of PDU bytes that follow the header.
func (h mbapHeader) pduLen() int { return int(h.Length) - 1 }

// decodeHeader parses an MBAP header. A length outside [2, 254] (unit id plus
// a PDU of 1..253 bytes) cannot be resynchronised on a stream, so the caller
// must drop the connection.
func decodeHeader(b []byte) (mbapHeader, error) {
	if len(b) < mbapHeaderLen {
		return mbapHeader{}, fmt.Errorf("short MBAP header: %d bytes", len(b))
	}
	h := mbapHeader{
		TransactionID: binary.BigEndian.Uint16(b[0:2]),
		ProtocolID:    binary.BigEndian.Uint16(b[2:4]),
		Length:        binary.BigEndian.Uint16(b[4:6]),
		UnitID:        b[6],
	}
	if h.Length < 2 || h.Length > maxPDULen+1 {
		return h, fmt.Errorf("%w: %d", errBadLength, h.Length)
	}
	return h, nil
}

// appendADU appends a response ADU (header echoing the request's transaction
// and unit id, followed by pdu) to dst.
func appendADU(dst []byte, req mbapHeader, pdu []byte) []byte {
	dst = binary.BigEndian.AppendUint16(dst, req.TransactionID)
	dst = binary.BigEndian.AppendUint16(dst, 0)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(pdu)+1))
	dst = append(dst, req.UnitID)
	return append(dst, pdu...)
}
