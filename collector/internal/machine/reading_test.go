package machine

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestDecode(t *testing.T) {
	ir := make([]byte, inputRegCount*2)
	put := func(b []byte, i int, v uint16) { binary.BigEndian.PutUint16(b[i*2:], v) }
	put(ir, 0, 2)                                 // RUNNING
	put(ir, 1, 1000)                              // 100.0 rpm
	put(ir, 7, 2051)                              // 205.1 °C
	put(ir, 10, 250)                              // 2.50 m/min
	binary.BigEndian.PutUint32(ir[16*2:], 123456) // 12345.6 m (ABCD)
	binary.BigEndian.PutUint32(ir[22*2:], math.Float32bits(251.5))
	binary.BigEndian.PutUint32(ir[24*2:], 70000)
	di := []byte{0b0010_0001, 0b0010_0000} // DI0 running, DI5 motor overload, DI13 e-stop alarm
	hr := make([]byte, holdingRegCount*2)
	put(hr, 0, 1000)
	binary.BigEndian.PutUint32(hr[6*2:], 50000) // 5000.0 m
	put(hr, 8, 3)

	r, err := Decode(1, time.Unix(0, 0), ir, di, hr)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name      string
		got, want float64
	}{
		{"rpm", r.ExtruderRPM, 100}, {"die", r.DieTemp, 205.1}, {"haul", r.HaulOffSpeed, 2.5},
		{"length", r.TotalLength, 12345.6}, {"f32", float64(r.MeltPressureF32), 251.5},
		{"simtime", float64(r.SimTime), 70000}, {"rpm_sp", r.ExtruderRPMSP, 100},
		{"target", r.ProductionTarget, 5000}, {"scenario", float64(r.FaultScenario), 3},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if r.StateName() != "RUNNING" || !r.Running || r.Heating || !r.AlarmMotorOverload || !r.AlarmEmergencyStop || r.AlarmSensorFailure {
		t.Errorf("bits/state wrong: %+v", r)
	}
	if _, err := Decode(1, time.Now(), ir[:10], di, hr); err == nil {
		t.Error("short frame accepted")
	}
}
