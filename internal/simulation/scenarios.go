package simulation

import (
	"fmt"
	"time"

	"modbus-simulator/internal/simulation/models"
)

// Scenario is an injectable fault cause (holding register 8). A scenario
// changes the physics; the fault itself is then detected automatically by the
// monitors in faults.go. It persists until set back to ScenarioNone ("repair").
// See docs/spikes/007-fault-model.md.
type Scenario uint8

const (
	ScenarioNone             Scenario = iota
	ScenarioScrewBlockage             // screw torque ×1.7 → motor overload
	ScenarioScreenClogging            // die resistance ×1.8 → melt pressure high
	ScenarioHeaterStuckOn             // zone 2 SSR welded → zone 2 temperature high
	ScenarioHeaterFailure             // zone 1 heater burned out → zone 1 temperature low
	ScenarioCoolingFailure            // cooling water flow 15 % → cooling water high
	ScenarioVacuumLeak                // vacuum pump capacity 30 % → vacuum low
	ScenarioHaulOffSlip               // caterpillar slips 15 % → haul-off deviation
	ScenarioThermocoupleOpen          // zone 2 thermocouple open → sensor failure
	scenarioEnd
)

var scenarioNames = [...]string{
	"none", "screw_blockage", "screen_pack_clogging", "zone2_heater_stuck_on", "zone1_heater_failure",
	"cooling_water_failure", "vacuum_leak", "haul_off_slip", "zone2_thermocouple_open",
}

// MaxScenario is the highest valid scenario code.
const MaxScenario = scenarioEnd - 1

func (s Scenario) String() string {
	if s < scenarioEnd {
		return scenarioNames[s]
	}
	return fmt.Sprintf("Scenario(%d)", uint8(s))
}

// SetScenario selects the active fault cause (0 = none / repaired).
func SetScenario(s Scenario) Command { return Command{Kind: CmdSetScenario, Scenario: s} }

// disturbance holds the physical effect of the active scenario. Continuous
// effects develop gradually through first-order lags (onset and repair), so
// warnings appear before trips, as on a real line.
type disturbance struct {
	torque, pressure, coolingFlow, vacuumCap, slip models.FirstOrderLag
	heaterStuck, heaterFailed, sensorFailed        [4]bool
}

func newDisturbance() *disturbance {
	return &disturbance{
		torque:      models.FirstOrderLag{Tau: 20 * time.Second, Value: 1},
		pressure:    models.FirstOrderLag{Tau: 30 * time.Second, Value: 1},
		coolingFlow: models.FirstOrderLag{Tau: 10 * time.Second, Value: 1},
		vacuumCap:   models.FirstOrderLag{Tau: 5 * time.Second, Value: 1},
		slip:        models.FirstOrderLag{Tau: 5 * time.Second},
	}
}

func (d *disturbance) step(s Scenario, dt time.Duration) {
	target := func(active Scenario, faulty, healthy float64) float64 {
		if s == active {
			return faulty
		}
		return healthy
	}
	d.torque.Step(target(ScenarioScrewBlockage, 1.7, 1), dt)
	d.pressure.Step(target(ScenarioScreenClogging, 1.8, 1), dt)
	d.coolingFlow.Step(target(ScenarioCoolingFailure, 0.15, 1), dt)
	d.vacuumCap.Step(target(ScenarioVacuumLeak, 0.3, 1), dt)
	d.slip.Step(target(ScenarioHaulOffSlip, 0.15, 0), dt)
	d.heaterStuck = [4]bool{1: s == ScenarioHeaterStuckOn}
	d.heaterFailed = [4]bool{0: s == ScenarioHeaterFailure}
	d.sensorFailed = [4]bool{1: s == ScenarioThermocoupleOpen}
}

// openThermocoupleReading is what an open (upscale burnout) thermocouple reads.
const openThermocoupleReading = 999.9
