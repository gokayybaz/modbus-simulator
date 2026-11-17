package simulation

import (
	"math"
	"testing"
)

// Every preset reaches a clean steady state at its nominal product
// dimensions (mass balance) without any alarm.
func TestPresetsSteadyState(t *testing.T) {
	for _, p := range Presets() {
		t.Run(p.Name, func(t *testing.T) {
			e, err := New(p.Config())
			if err != nil {
				t.Fatal(err)
			}
			e.Step([]Command{Start()})
			stepUntil(t, e, 8000, func(s Snapshot) bool { return s.State == Running })
			var alarms AlarmSet
			s := e.Snapshot()
			for range 3600 { // 30 min
				s, _ = e.Step(nil)
				alarms |= s.Alarms
				if s.State != Running {
					t.Fatalf("left RUNNING: %v fault %d", s.State, s.FaultCode)
				}
			}
			v := s.True
			if math.Abs(v.WallThickness-p.NominalWall)/p.NominalWall > 0.015 {
				t.Errorf("wall %.3f mm, nominal %.1f", v.WallThickness, p.NominalWall)
			}
			if math.Abs(v.PipeOD-p.NominalOD) > 0.1 {
				t.Errorf("OD %.3f mm, nominal %.0f", v.PipeOD, p.NominalOD)
			}
			for i, sp := range s.Setpoints.ZoneTemps {
				if math.Abs(v.ZoneTemps[i]-sp) > 0.5 {
					t.Errorf("zone %d %.2f, SP %.0f", i+1, v.ZoneTemps[i], sp)
				}
			}
			if alarms != 0 {
				t.Errorf("alarms during steady production: %09b (load %.1f %%, P %.1f bar, water %.1f °C)",
					alarms, v.MotorLoad, v.MeltPressure, v.CoolingWaterTemp)
			}
			want := p.Config().Process.Extruder.NominalThroughput()
			if math.Abs(v.Throughput-want) > 0.01 {
				t.Errorf("throughput %.2f, want %.0f", v.Throughput, want)
			}
			t.Logf("%s: %.0f kg/h, %.1f bar, %.1f A (%.0f %%), water %.1f °C, OD %.2f, wall %.3f, %.1f kW",
				p.Name, v.Throughput, v.MeltPressure, v.MotorCurrent, v.MotorLoad, v.CoolingWaterTemp,
				v.PipeOD, v.WallThickness, v.PowerConsumption)
		})
	}
}

func TestPresetByName(t *testing.T) {
	if p, err := PresetByName("pvc-110"); err != nil || p.Config().Process.Pipe.Density != 1400 {
		t.Fatalf("%v %v", p.Name, err)
	}
	if _, err := PresetByName("abs-32"); err == nil {
		t.Fatal("unknown preset accepted")
	}
	// Presets are independent values: mutating one copy affects no other.
	a, b := PresetHDPE63(), PresetHDPE63()
	a.Setpoints.ExtruderRPM = 1
	if b.Setpoints.ExtruderRPM != 80 || DefaultConfig().Setpoints.ExtruderRPM != 100 {
		t.Fatal("presets share state")
	}
}
