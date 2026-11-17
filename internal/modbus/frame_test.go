package modbus

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeHeader(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    mbapHeader
		wantErr error
	}{
		{"valid FC04 request", []byte{0x12, 0x34, 0, 0, 0, 6, 1}, mbapHeader{0x1234, 0, 6, 1}, nil},
		{"min length 2", []byte{0, 1, 0, 0, 0, 2, 9}, mbapHeader{1, 0, 2, 9}, nil},
		{"max length 254", []byte{0, 1, 0, 0, 0, 254, 1}, mbapHeader{1, 0, 254, 1}, nil},
		{"non-zero protocol id still decodes", []byte{0, 1, 0, 7, 0, 6, 1}, mbapHeader{1, 7, 6, 1}, nil},
		{"length 0", []byte{0, 1, 0, 0, 0, 0, 1}, mbapHeader{1, 0, 0, 1}, errBadLength},
		{"length 1 (no function code)", []byte{0, 1, 0, 0, 0, 1, 1}, mbapHeader{1, 0, 1, 1}, errBadLength},
		{"length 255", []byte{0, 1, 0, 0, 0, 255, 1}, mbapHeader{1, 0, 255, 1}, errBadLength},
		{"length 0xFFFF", []byte{0, 1, 0, 0, 0xFF, 0xFF, 1}, mbapHeader{1, 0, 0xFFFF, 1}, errBadLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeHeader(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("header = %+v, want %+v", got, tt.want)
			}
		})
	}
	if _, err := decodeHeader([]byte{1, 2, 3}); err == nil {
		t.Fatal("short header: want error")
	}
}

func TestAppendADU(t *testing.T) {
	got := appendADU(nil, mbapHeader{TransactionID: 0xBEEF, ProtocolID: 0, UnitID: 7}, []byte{0x04, 0x02, 0x01, 0xC8})
	want := []byte{0xBE, 0xEF, 0, 0, 0, 5, 7, 0x04, 0x02, 0x01, 0xC8}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % X, want % X", got, want)
	}
}

func FuzzDecodeHeader(f *testing.F) {
	f.Add([]byte{0, 1, 0, 0, 0, 6, 1})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := decodeHeader(b)
		if err == nil && (h.pduLen() < 1 || h.pduLen() > maxPDULen) {
			t.Fatalf("accepted header with PDU length %d", h.pduLen())
		}
	})
}
