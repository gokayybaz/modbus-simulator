// Package catalog is the single source of signal, fault, scenario and
// root-cause knowledge shared by the API, the RCA engine and the web UI
// (served at /api/meta). Values follow docs/registers/convention.md and
// docs/faults/fault-table.md.
package catalog

// Signal is one numeric process value stored in machine_readings.
type Signal struct {
	Key   string  `json:"key"`   // column name in machine_readings
	Label string  `json:"label"` // Turkish UI label
	Unit  string  `json:"unit"`
	Group string  `json:"group"` // extruder | thermal | downstream | quality | energy
	SP    string  `json:"sp,omitempty"`
	Floor float64 `json:"-"` // minimum σ used for z-scores (sensor noise floor)
	// Physical plausibility range: values outside are treated as sensor errors.
	Min, Max float64 `json:"-"`
}

var Signals = []Signal{
	{Key: "extruder_rpm", Label: "Vida devri", Unit: "rpm", Group: "extruder", SP: "extruder_rpm_sp", Floor: 0.5, Min: 0, Max: 300},
	{Key: "motor_current", Label: "Motor akımı", Unit: "A", Group: "extruder", Floor: 1, Min: 0, Max: 2000},
	{Key: "motor_load", Label: "Motor yükü", Unit: "%", Group: "extruder", Floor: 0.8, Min: 0, Max: 200},
	{Key: "melt_pressure", Label: "Eriyik basıncı", Unit: "bar", Group: "extruder", Floor: 2, Min: 0, Max: 1000},
	{Key: "melt_temp", Label: "Eriyik sıcaklığı", Unit: "°C", Group: "thermal", Floor: 0.6, Min: -20, Max: 500},
	{Key: "zone1_temp", Label: "Zon 1", Unit: "°C", Group: "thermal", SP: "zone1_temp_sp", Floor: 0.6, Min: -20, Max: 500},
	{Key: "zone2_temp", Label: "Zon 2", Unit: "°C", Group: "thermal", SP: "zone2_temp_sp", Floor: 0.6, Min: -20, Max: 500},
	{Key: "zone3_temp", Label: "Zon 3", Unit: "°C", Group: "thermal", SP: "zone3_temp_sp", Floor: 0.6, Min: -20, Max: 500},
	{Key: "die_temp", Label: "Kalıp (die)", Unit: "°C", Group: "thermal", SP: "die_temp_sp", Floor: 0.6, Min: -20, Max: 500},
	{Key: "vacuum", Label: "Vakum", Unit: "mbar", Group: "downstream", Floor: 4, Min: 0, Max: 1100},
	{Key: "cooling_water_temp", Label: "Soğutma suyu", Unit: "°C", Group: "downstream", Floor: 0.3, Min: -5, Max: 100},
	{Key: "haul_off_speed", Label: "Çekici hızı", Unit: "m/dk", Group: "downstream", SP: "haul_off_speed_sp", Floor: 0.03, Min: 0, Max: 200},
	{Key: "pipe_outer_diameter", Label: "Boru dış çapı", Unit: "mm", Group: "quality", Floor: 0.05, Min: 0, Max: 2000},
	{Key: "pipe_wall_thickness", Label: "Et kalınlığı", Unit: "mm", Group: "quality", Floor: 0.03, Min: 0, Max: 200},
	{Key: "throughput", Label: "Debi", Unit: "kg/h", Group: "energy", Floor: 1, Min: 0, Max: 5000},
	{Key: "power_consumption", Label: "Güç", Unit: "kW", Group: "energy", Floor: 1, Min: 0, Max: 5000},
}

// Setpoints read from holding registers.
var Setpoints = []Signal{
	{Key: "extruder_rpm_sp", Label: "Vida devri SP", Unit: "rpm"},
	{Key: "zone1_temp_sp", Label: "Zon 1 SP", Unit: "°C"},
	{Key: "zone2_temp_sp", Label: "Zon 2 SP", Unit: "°C"},
	{Key: "zone3_temp_sp", Label: "Zon 3 SP", Unit: "°C"},
	{Key: "die_temp_sp", Label: "Kalıp SP", Unit: "°C"},
	{Key: "haul_off_speed_sp", Label: "Çekici hızı SP", Unit: "m/dk"},
	{Key: "production_target", Label: "Üretim hedefi", Unit: "m"},
}

// Alarm is one alarm bit (IR26 bit n = DI 5+n).
type Alarm struct {
	Bit    int    `json:"bit"`
	Column string `json:"column"`
	Label  string `json:"label"`
	Signal string `json:"signal,omitempty"`
}

var Alarms = []Alarm{
	{0, "alarm_motor_overload", "Motor aşırı yük", "motor_load"},
	{1, "alarm_melt_pressure_high", "Eriyik basıncı yüksek", "melt_pressure"},
	{2, "alarm_zone_temp_high", "Zon sıcaklığı yüksek", ""},
	{3, "alarm_zone_temp_low", "Zon sıcaklığı düşük", ""},
	{4, "alarm_cooling_water_high", "Soğutma suyu sıcak", "cooling_water_temp"},
	{5, "alarm_vacuum_low", "Vakum düşük", "vacuum"},
	{6, "alarm_haul_off_deviation", "Çekici hız sapması", "haul_off_speed"},
	{7, "alarm_sensor_failure", "Sensör arızası", ""},
	{8, "alarm_emergency_stop", "Acil stop", ""},
}

// Limit describes default warning/trip thresholds (fault table v1).
// Relative limits are offsets from the signal's setpoint.
type Limit struct {
	Signal   string  `json:"signal"`
	Warn     float64 `json:"warn"`
	Trip     float64 `json:"trip"`
	Dir      string  `json:"dir"` // high | low | band
	Relative bool    `json:"relative,omitempty"`
	WarnLow  float64 `json:"warnLow,omitempty"`
	TripLow  float64 `json:"tripLow,omitempty"`
}

var Limits = []Limit{
	{Signal: "motor_load", Warn: 95, Trip: 110, Dir: "high"},
	{Signal: "melt_pressure", Warn: 300, Trip: 350, Dir: "high"},
	{Signal: "cooling_water_temp", Warn: 25, Trip: 30, Dir: "high"},
	{Signal: "vacuum", Warn: 300, Trip: 200, Dir: "low"},
	{Signal: "zone1_temp", Warn: 15, Trip: 30, WarnLow: -10, TripLow: -20, Dir: "band", Relative: true},
	{Signal: "zone2_temp", Warn: 15, Trip: 30, WarnLow: -10, TripLow: -20, Dir: "band", Relative: true},
	{Signal: "zone3_temp", Warn: 15, Trip: 30, WarnLow: -10, TripLow: -20, Dir: "band", Relative: true},
	{Signal: "die_temp", Warn: 15, Trip: 30, WarnLow: -10, TripLow: -20, Dir: "band", Relative: true},
}

// Fault is one fault code of the simulator.
type Fault struct {
	Code     int    `json:"code"`
	Name     string `json:"name"`
	Label    string `json:"label"`
	Severity string `json:"severity"`
	Signal   string `json:"signal,omitempty"` // primary trip signal
}

var Faults = []Fault{
	{1, "emergency_stop", "Acil stop", "critical", ""},
	{10, "extruder_motor_overload", "Ekstrüder motoru aşırı yük", "trip", "motor_load"},
	{11, "melt_pressure_high", "Eriyik basıncı yüksek", "trip", "melt_pressure"},
	{21, "zone1_temp_high", "Zon 1 sıcaklığı yüksek", "trip", "zone1_temp"},
	{22, "zone2_temp_high", "Zon 2 sıcaklığı yüksek", "trip", "zone2_temp"},
	{23, "zone3_temp_high", "Zon 3 sıcaklığı yüksek", "trip", "zone3_temp"},
	{24, "die_temp_high", "Kalıp sıcaklığı yüksek", "trip", "die_temp"},
	{31, "zone1_temp_low", "Zon 1 sıcaklığı düşük", "trip", "zone1_temp"},
	{32, "zone2_temp_low", "Zon 2 sıcaklığı düşük", "trip", "zone2_temp"},
	{33, "zone3_temp_low", "Zon 3 sıcaklığı düşük", "trip", "zone3_temp"},
	{34, "die_temp_low", "Kalıp sıcaklığı düşük", "trip", "die_temp"},
	{40, "cooling_water_temp_high", "Soğutma suyu sıcaklığı yüksek", "trip", "cooling_water_temp"},
	{41, "vacuum_low", "Vakum düşük", "trip", "vacuum"},
	{50, "haul_off_speed_deviation", "Çekici hız sapması", "trip", "haul_off_speed"},
	{60, "sensor_failure", "Sensör arızası", "trip", ""},
}

func FaultByCode(code int) (Fault, bool) {
	for _, f := range Faults {
		if f.Code == code {
			return f, true
		}
	}
	return Fault{}, false
}

// Scenario is a simulator cause injected through HR8 (ground truth for RCA).
type Scenario struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	FaultCode int    `json:"faultCode"`
	Cause     string `json:"cause"` // matching cause ID in the knowledge base
}

var Scenarios = []Scenario{
	{0, "none", "Yok", 0, ""},
	{1, "screw_blockage", "Vida tıkanması", 10, "screw_blockage"},
	{2, "screen_pack_clogging", "Elek paketi tıkanması", 11, "screen_pack_clogging"},
	{3, "zone2_heater_stuck_on", "Zon 2 ısıtıcı SSR kaynamış", 22, "heater_stuck_on"},
	{4, "zone1_heater_failure", "Zon 1 ısıtıcı yanmış", 31, "heater_failure"},
	{5, "cooling_water_failure", "Soğutma suyu debi kaybı", 40, "cooling_flow_loss"},
	{6, "vacuum_leak", "Vakum kaçağı", 41, "vacuum_leak"},
	{7, "haul_off_slip", "Çekici paletleri kayıyor", 50, "haul_off_slip"},
	{8, "zone2_thermocouple_open", "Zon 2 termokupl kopuk", 60, "thermocouple_open"},
}

func ScenarioByID(id int) (Scenario, bool) {
	for _, s := range Scenarios {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}

// Evidence is one expected symptom of a cause: the signal moves in Dir
// ("up" | "down" | "jump" = implausible reading | "sp" = setpoint changed).
type Evidence struct {
	Signal  string  `json:"signal"`
	Dir     string  `json:"dir"`
	Weight  float64 `json:"weight"`
	Primary bool    `json:"primary,omitempty"` // expected to deviate first
	Note    string  `json:"note"`
}

// Cause is one root-cause hypothesis with its 6M (Ishikawa) category.
type Cause struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Category string     `json:"category"` // Makine | Malzeme | Metot | İnsan | Ölçüm | Çevre
	Detail   string     `json:"detail"`
	Faults   []int      `json:"faults"`
	Evidence []Evidence `json:"evidence"`
	Actions  []string   `json:"actions"`
}

func zones(n int) []int { return []int{20 + n, 30 + n} }

var Causes = []Cause{
	{
		ID: "screw_blockage", Label: "Vida tıkanması / yabancı madde", Category: "Makine",
		Detail: "Vida kanalında sıkışan malzeme veya yabancı cisim tork ihtiyacını artırır; motor yükü ve akım devir sabitken yükselir.",
		Faults: []int{10, 11},
		Evidence: []Evidence{
			{Signal: "motor_load", Dir: "up", Weight: 3, Primary: true, Note: "Motor yükü devir sabitken artıyor"},
			{Signal: "motor_current", Dir: "up", Weight: 2, Note: "Motor akımı yükle birlikte artıyor"},
			{Signal: "power_consumption", Dir: "up", Weight: 1, Note: "Güç tüketimi artıyor"},
			{Signal: "extruder_rpm", Dir: "flat", Weight: 1, Note: "Vida devri sabit (operatör değişikliği yok)"},
		},
		Actions: []string{"Vidayı ve kovanı yabancı madde açısından kontrol edin", "Hammadde besleme hattında topak/kontaminasyon arayın", "Motor tork trendini vardiya bazında izleyin"},
	},
	{
		ID: "screen_pack_clogging", Label: "Elek paketi tıkanması", Category: "Makine",
		Detail: "Elek paketinde biriken kirlilik kafa direncini artırır; basınç yavaş ve monoton yükselir, motor yükü sınırlı artar.",
		Faults: []int{11, 10},
		Evidence: []Evidence{
			{Signal: "melt_pressure", Dir: "up", Weight: 3, Primary: true, Note: "Eriyik basıncı monoton artıyor"},
			{Signal: "extruder_rpm", Dir: "flat", Weight: 1, Note: "Vida devri sabit"},
			{Signal: "melt_temp", Dir: "flat", Weight: 1, Note: "Eriyik sıcaklığı normal — viskozite kaynaklı değil"},
			{Signal: "motor_load", Dir: "up", Weight: 0.5, Note: "Motor yükü basınca bağlı hafif artıyor"},
		},
		Actions: []string{"Elek paketini değiştirin (screen changer)", "Hammadde/geri dönüşüm oranındaki kirliliği kontrol edin", "Elek değişim periyodunu basınç trendine göre planlayın"},
	},
	{
		ID: "cold_melt", Label: "Düşük eriyik sıcaklığı (yüksek viskozite)", Category: "Metot",
		Detail: "Zon sıcaklıkları setpoint altında kalınca eriyik viskozitesi artar; basınç ve motor yükü birlikte yükselir.",
		Faults: []int{10, 11},
		Evidence: []Evidence{
			{Signal: "melt_temp", Dir: "down", Weight: 3, Primary: true, Note: "Eriyik sıcaklığı düşüyor"},
			{Signal: "zone1_temp", Dir: "down", Weight: 1, Note: "Zon sıcaklıkları düşük"},
			{Signal: "zone2_temp", Dir: "down", Weight: 1, Note: "Zon sıcaklıkları düşük"},
			{Signal: "melt_pressure", Dir: "up", Weight: 1, Note: "Basınç artıyor"},
		},
		Actions: []string{"Isıtıcı zonlarının setpoint ve gerçek değerlerini doğrulayın", "Isınma (soak) süresini artırın"},
	},
	{
		ID: "rpm_setpoint_change", Label: "Operatör devir artışı", Category: "İnsan",
		Detail: "Vida devri setpoint'i yükseltildi; debi, basınç ve motor yükü yeni çalışma noktasına çıktı.",
		Faults: []int{10, 11, 50},
		Evidence: []Evidence{
			{Signal: "extruder_rpm_sp", Dir: "sp", Weight: 4, Primary: true, Note: "Vida devri setpoint'i değişti"},
			{Signal: "extruder_rpm", Dir: "up", Weight: 2, Note: "Vida devri arttı"},
			{Signal: "throughput", Dir: "up", Weight: 1, Note: "Debi arttı"},
		},
		Actions: []string{"Reçete limitlerini (setpoint_limits) daraltın", "Devir değişikliklerini rampalı yapın", "Operatör yetkilendirmesini gözden geçirin"},
	},
	{
		ID: "heater_stuck_on", Label: "Isıtıcı SSR kaynaması (sürekli açık)", Category: "Makine",
		Detail: "Katı hal rölesi kısa devre kaldığında ısıtıcı tam güçte kalır; zon sıcaklığı setpoint'i aşarak yükselmeye devam eder.",
		Faults: append(append(append(zones(1), zones(2)...), zones(3)...), 24),
		Evidence: []Evidence{
			{Signal: "$zone", Dir: "up", Weight: 3, Primary: true, Note: "Zon sıcaklığı setpoint üstüne çıkıp artmaya devam ediyor"},
			{Signal: "power_consumption", Dir: "up", Weight: 1, Note: "Güç tüketimi artıyor (ısıtıcı tam güç)"},
			{Signal: "$zone_sp", Dir: "nosp", Weight: 1, Note: "Setpoint değişmedi"},
		},
		Actions: []string{"İlgili zonun SSR/kontaktörünü kontrol edin ve değiştirin", "Isıtıcı akımını pens ampermetre ile doğrulayın"},
	},
	{
		ID: "heater_failure", Label: "Isıtıcı rezistansı yanmış", Category: "Makine",
		Detail: "Isıtıcı eleman açık devre olunca zon soğur; kontrolör tam güç ister ama sıcaklık düşmeye devam eder.",
		Faults: append(append(append(zones(1), zones(2)...), zones(3)...), 34),
		Evidence: []Evidence{
			{Signal: "$zone", Dir: "down", Weight: 3, Primary: true, Note: "Zon sıcaklığı setpoint altına düşüyor"},
			{Signal: "power_consumption", Dir: "down", Weight: 1, Note: "Güç tüketimi düştü (ısıtıcı akım çekmiyor)"},
			{Signal: "$zone_sp", Dir: "nosp", Weight: 1, Note: "Setpoint değişmedi"},
		},
		Actions: []string{"Isıtıcı bant rezistansının direncini ölçün", "Klemens ve kabloları kontrol edin", "Yedek ısıtıcıyla değiştirin"},
	},
	{
		ID: "temp_setpoint_change", Label: "Sıcaklık setpoint değişikliği", Category: "İnsan",
		Detail: "Operatör zon setpoint'ini değiştirdi; zon yeni değere geçerken sapma alarmı oluştu.",
		Faults: append(append(append(zones(1), zones(2)...), zones(3)...), 24, 34),
		Evidence: []Evidence{
			{Signal: "$zone_sp", Dir: "sp", Weight: 4, Primary: true, Note: "Zon setpoint'i değişti"},
		},
		Actions: []string{"Setpoint değişikliklerini rampalı yapın", "Değişiklik kaydını operatörle teyit edin"},
	},
	{
		ID: "thermocouple_open", Label: "Termokupl kopuk / sensör arızası", Category: "Ölçüm",
		Detail: "Kopuk termokupl ölçüm aralığı dışında değer (999.9 °C) okur; kontrolör ısıtıcıyı keser.",
		Faults: []int{60, 21, 22, 23, 24},
		Evidence: []Evidence{
			{Signal: "$any_temp", Dir: "jump", Weight: 5, Primary: true, Note: "Sıcaklık okuması fiziksel aralık dışına sıçradı"},
		},
		Actions: []string{"Termokupl bağlantısını ve kablosunu kontrol edin", "Termokuplu değiştirin", "Kompanzasyon kablosunu ölçün"},
	},
	{
		ID: "cooling_flow_loss", Label: "Soğutma suyu debi kaybı", Category: "Çevre",
		Detail: "Soğutma suyu debisi düşünce kalibrasyon tankından çekilen ısı azalır; su sıcaklığı yükselir.",
		Faults: []int{40},
		Evidence: []Evidence{
			{Signal: "cooling_water_temp", Dir: "up", Weight: 3, Primary: true, Note: "Soğutma suyu sıcaklığı artıyor"},
			{Signal: "throughput", Dir: "flat", Weight: 1, Note: "Debi sabit — ısı yükü artmadı"},
		},
		Actions: []string{"Soğutma suyu pompasını, filtresini ve vanalarını kontrol edin", "Chiller çıkış sıcaklığını doğrulayın"},
	},
	{
		ID: "chiller_capacity", Label: "Isı yükü artışı / chiller kapasitesi", Category: "Çevre",
		Detail: "Debi artışı soğutma sistemine giden ısı yükünü artırır.",
		Faults: []int{40},
		Evidence: []Evidence{
			{Signal: "throughput", Dir: "up", Weight: 2, Primary: true, Note: "Debi arttı"},
			{Signal: "cooling_water_temp", Dir: "up", Weight: 1, Note: "Su sıcaklığı arttı"},
		},
		Actions: []string{"Chiller kapasitesini hat debisiyle karşılaştırın"},
	},
	{
		ID: "vacuum_leak", Label: "Vakum tankı kaçağı / pompa kapasite kaybı", Category: "Makine",
		Detail: "Conta, kapak veya hortum kaçağı vakum pompasının çekebileceği seviyeyi düşürür.",
		Faults: []int{41},
		Evidence: []Evidence{
			{Signal: "vacuum", Dir: "down", Weight: 3, Primary: true, Note: "Vakum seviyesi düşüyor"},
			{Signal: "pipe_outer_diameter", Dir: "any", Weight: 1, Note: "Boru çapı kalibrasyonu etkileniyor"},
		},
		Actions: []string{"Vakum tankı kapak contalarını ve hortumları kontrol edin", "Vakum pompası su seviyesini kontrol edin"},
	},
	{
		ID: "haul_off_slip", Label: "Çekici paleti kayması", Category: "Makine",
		Detail: "Palet basıncı/aşınması yetersiz olduğunda boru paletlerde kayar; gerçek hız referansın altında kalır, et kalınlığı artar.",
		Faults: []int{50},
		Evidence: []Evidence{
			{Signal: "haul_off_speed", Dir: "down", Weight: 3, Primary: true, Note: "Çekici gerçek hızı düşüyor"},
			{Signal: "haul_off_speed_sp", Dir: "nosp", Weight: 1, Note: "Hız setpoint'i değişmedi"},
			{Signal: "pipe_wall_thickness", Dir: "up", Weight: 2, Note: "Et kalınlığı artıyor (boru yavaş çekiliyor)"},
			{Signal: "pipe_outer_diameter", Dir: "up", Weight: 1, Note: "Dış çap artıyor"},
		},
		Actions: []string{"Palet baskı basıncını kontrol edin", "Aşınmış palet pabuçlarını değiştirin"},
	},
	{
		ID: "emergency_stop", Label: "Acil stop butonu", Category: "İnsan",
		Detail: "Acil stop devresi açıldı; tüm tahrikler ve ısıtıcılar kesildi.",
		Faults: []int{1},
		Evidence: []Evidence{
			{Signal: "emergency_stop_active", Dir: "flag", Weight: 5, Primary: true, Note: "Acil stop aktif"},
		},
		Actions: []string{"Acil stop basılma nedenini operatörle teyit edin", "Güvenlik devresini kontrol edin"},
	},
}

// ZoneSignal returns the zone/die signal of a temperature fault code (21–24, 31–34).
func ZoneSignal(code int) string {
	switch code % 10 {
	case 1:
		return "zone1_temp"
	case 2:
		return "zone2_temp"
	case 3:
		return "zone3_temp"
	case 4:
		return "die_temp"
	}
	return ""
}

var TempSignals = []string{"zone1_temp", "zone2_temp", "zone3_temp", "die_temp"}

func SignalByKey(key string) (Signal, bool) {
	for _, s := range Signals {
		if s.Key == key {
			return s, true
		}
	}
	for _, s := range Setpoints {
		if s.Key == key {
			return s, true
		}
	}
	return Signal{}, false
}

var StateNames = map[int]string{0: "STOPPED", 1: "HEATING", 2: "RUNNING", 3: "FAULT"}
