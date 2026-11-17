// Package machine reads one snapshot of an extrusion line over Modbus TCP and
// decodes it according to the simulator register map
// (docs/registers/convention.md, register map v1).
package machine

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Register block sizes (register map v1).
const (
	inputRegCount   = 29 // IR 0..28
	discreteInCount = 14 // DI 0..13
	holdingRegCount = 9  // HR 0..8
	minFrameIRBytes = inputRegCount * 2
	minFrameHRBytes = holdingRegCount * 2
	minFrameDIBytes = (discreteInCount + 7) / 8
)

var StateNames = map[uint16]string{0: "STOPPED", 1: "HEATING", 2: "RUNNING", 3: "FAULT"}

// Reading is one decoded snapshot of a line. Engineering units as in the map.
type Reading struct {
	Time   time.Time
	UnitID byte

	// Input registers (FC04)
	MachineState      uint16  // IR0  enum
	ExtruderRPM       float64 // IR1  rpm      /10
	MotorCurrent      float64 // IR2  A        /10
	MeltPressure      float64 // IR3  bar      /10
	Zone1Temp         float64 // IR4  °C       /10
	Zone2Temp         float64 // IR5  °C       /10
	Zone3Temp         float64 // IR6  °C       /10
	DieTemp           float64 // IR7  °C       /10
	Vacuum            float64 // IR8  mbar     /1
	CoolingWaterTemp  float64 // IR9  °C       /10
	HaulOffSpeed      float64 // IR10 m/min    /100
	Throughput        float64 // IR11 kg/h     /10
	PowerConsumption  float64 // IR12 kW       /10
	PipeOuterDiameter float64 // IR13 mm       /100
	PipeWallThickness float64 // IR14 mm       /100
	ActiveFaultCode   uint16  // IR15
	TotalLength       float64 // IR16-17 UINT32 m  /10
	TotalWeight       float64 // IR18-19 UINT32 kg /10
	MotorLoad         float64 // IR20 %        /10
	MeltTemp          float64 // IR21 °C       /10
	MeltPressureF32   float32 // IR22-23 FLOAT32 bar
	SimTime           uint32  // IR24-25 s
	AlarmWord         uint16  // IR26 bit n = DI 5+n
	ActiveScenario    uint16  // IR27
	LastFaultCode     uint16  // IR28

	// Discrete inputs (FC02)
	Running, Heating, FaultActive, EStopActive, TargetReached     bool
	AlarmMotorOverload, AlarmMeltPressureHigh, AlarmZoneTempHigh  bool
	AlarmZoneTempLow, AlarmCoolingWaterHigh, AlarmVacuumLow       bool
	AlarmHaulOffDeviation, AlarmSensorFailure, AlarmEmergencyStop bool

	// Holding registers (FC03) — setpoints
	ExtruderRPMSP    float64 // HR0 /10
	Zone1TempSP      float64 // HR1 /10
	Zone2TempSP      float64 // HR2 /10
	Zone3TempSP      float64 // HR3 /10
	DieTempSP        float64 // HR4 /10
	HaulOffSpeedSP   float64 // HR5 /100
	ProductionTarget float64 // HR6-7 UINT32 /10
	FaultScenario    uint16  // HR8
}

func (r Reading) StateName() string {
	if s, ok := StateNames[r.MachineState]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", r.MachineState)
}

// Client is the subset of a Modbus client the reader needs.
type Client interface {
	ReadInputRegisters(ctx context.Context, address, quantity uint16) ([]byte, error)
	ReadDiscreteInputs(ctx context.Context, address, quantity uint16) ([]byte, error)
	ReadHoldingRegisters(ctx context.Context, address, quantity uint16) ([]byte, error)
}

// Read fetches all blocks of one line. Each block is a single request so
// 32-bit values come from one simulator tick.
func Read(ctx context.Context, c Client, unit byte, now time.Time) (Reading, error) {
	ir, err := c.ReadInputRegisters(ctx, 0, inputRegCount)
	if err != nil {
		return Reading{}, fmt.Errorf("input registers: %w", err)
	}
	di, err := c.ReadDiscreteInputs(ctx, 0, discreteInCount)
	if err != nil {
		return Reading{}, fmt.Errorf("discrete inputs: %w", err)
	}
	hr, err := c.ReadHoldingRegisters(ctx, 0, holdingRegCount)
	if err != nil {
		return Reading{}, fmt.Errorf("holding registers: %w", err)
	}
	return Decode(unit, now, ir, di, hr)
}

// Decode converts raw response payloads into a Reading.
func Decode(unit byte, now time.Time, ir, di, hr []byte) (Reading, error) {
	if len(ir) < minFrameIRBytes || len(di) < minFrameDIBytes || len(hr) < minFrameHRBytes {
		return Reading{}, fmt.Errorf("short response: ir=%dB di=%dB hr=%dB", len(ir), len(di), len(hr))
	}
	u16 := func(b []byte, i int) uint16 { return binary.BigEndian.Uint16(b[i*2:]) }
	u32 := func(b []byte, i int) uint32 { return binary.BigEndian.Uint32(b[i*2:]) } // ABCD word order
	sc := func(b []byte, i int, s float64) float64 { return float64(u16(b, i)) / s }
	bit := func(i int) bool { return di[i/8]&(1<<(i%8)) != 0 }

	return Reading{
		Time: now, UnitID: unit,

		MachineState:      u16(ir, 0),
		ExtruderRPM:       sc(ir, 1, 10),
		MotorCurrent:      sc(ir, 2, 10),
		MeltPressure:      sc(ir, 3, 10),
		Zone1Temp:         sc(ir, 4, 10),
		Zone2Temp:         sc(ir, 5, 10),
		Zone3Temp:         sc(ir, 6, 10),
		DieTemp:           sc(ir, 7, 10),
		Vacuum:            sc(ir, 8, 1),
		CoolingWaterTemp:  sc(ir, 9, 10),
		HaulOffSpeed:      sc(ir, 10, 100),
		Throughput:        sc(ir, 11, 10),
		PowerConsumption:  sc(ir, 12, 10),
		PipeOuterDiameter: sc(ir, 13, 100),
		PipeWallThickness: sc(ir, 14, 100),
		ActiveFaultCode:   u16(ir, 15),
		TotalLength:       float64(u32(ir, 16)) / 10,
		TotalWeight:       float64(u32(ir, 18)) / 10,
		MotorLoad:         sc(ir, 20, 10),
		MeltTemp:          sc(ir, 21, 10),
		MeltPressureF32:   math.Float32frombits(u32(ir, 22)),
		SimTime:           u32(ir, 24),
		AlarmWord:         u16(ir, 26),
		ActiveScenario:    u16(ir, 27),
		LastFaultCode:     u16(ir, 28),

		Running: bit(0), Heating: bit(1), FaultActive: bit(2), EStopActive: bit(3), TargetReached: bit(4),
		AlarmMotorOverload: bit(5), AlarmMeltPressureHigh: bit(6), AlarmZoneTempHigh: bit(7),
		AlarmZoneTempLow: bit(8), AlarmCoolingWaterHigh: bit(9), AlarmVacuumLow: bit(10),
		AlarmHaulOffDeviation: bit(11), AlarmSensorFailure: bit(12), AlarmEmergencyStop: bit(13),

		ExtruderRPMSP:    sc(hr, 0, 10),
		Zone1TempSP:      sc(hr, 1, 10),
		Zone2TempSP:      sc(hr, 2, 10),
		Zone3TempSP:      sc(hr, 3, 10),
		DieTempSP:        sc(hr, 4, 10),
		HaulOffSpeedSP:   sc(hr, 5, 100),
		ProductionTarget: float64(u32(hr, 6)) / 10,
		FaultScenario:    u16(hr, 8),
	}, nil
}
