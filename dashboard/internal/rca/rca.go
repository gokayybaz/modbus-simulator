// Package rca performs data-driven root-cause analysis of a machine trip.
//
// Method (all on stored readings, no simulator knowledge required):
//  1. The pre-trip segment is the contiguous run of non-FAULT samples before
//     the trip; its first part is the baseline (mean, σ with a noise floor).
//  2. Every signal gets a z-score series. A signal's onset is the earliest
//     sample from which it stays ≥ 2.5σ away from baseline until the trip
//     (and is ≥ 3σ on the last pre-trip sample) — a sustained precursor.
//  3. Onsets, alarm-bit onsets and setpoint changes form a time-ordered
//     causal chain ending with the trip and its consequences.
//  4. Each knowledge-base cause for the fault code is scored against the
//     observed symptoms (direction match, contradictions, and whether its
//     primary symptom deviated first). Fit = score / max score.
//  5. The simulator's cause register (HR8) is reported separately as ground
//     truth so the inference can be validated, never used for scoring.
package rca

import (
	"math"
	"sort"
	"time"

	"modbus-dashboard/internal/catalog"
)

// Sample is one stored reading.
type Sample struct {
	T             time.Time
	Sim           int64
	State         int
	ActiveFault   int
	FaultScenario int
	AlarmWord     int
	EStop         bool
	V             map[string]float64 // signals and setpoints by column
}

type Input struct {
	UnitID    int
	FaultCode int
	TripTime  time.Time
	Samples   []Sample // ascending by T, covering lookback..post
}

type SignalResult struct {
	Key        string     `json:"key"`
	Label      string     `json:"label"`
	Unit       string     `json:"unit"`
	Group      string     `json:"group"`
	Mean       float64    `json:"mean"`
	Sigma      float64    `json:"sigma"`
	Last       float64    `json:"last"`
	Z          float64    `json:"z"`
	PeakZ      float64    `json:"peakZ"`
	Deviated   bool       `json:"deviated"`
	Dir        string     `json:"dir"` // up | down | flat
	Onset      *time.Time `json:"onset,omitempty"`
	OnsetSim   *int64     `json:"onsetSim,omitempty"`
	LeadS      float64    `json:"leadS"`    // wall seconds before trip
	LeadSimS   float64    `json:"leadSimS"` // simulator seconds before trip
	Corr       *float64   `json:"corr,omitempty"`
	OutOfRange bool       `json:"outOfRange,omitempty"`
	Inferred   bool       `json:"inferred,omitempty"`
}

type ChainNode struct {
	T      time.Time `json:"t"`
	Sim    int64     `json:"sim"`
	Kind   string    `json:"kind"` // action | cause | deviation | alarm | trip | effect
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
	Signal string    `json:"signal,omitempty"`
}

type EvidenceResult struct {
	catalog.Evidence
	Resolved string `json:"resolved"`
	Status   string `json:"status"` // match | contradict | absent
	Observed string `json:"observed"`
}

type Hypothesis struct {
	Cause       catalog.Cause    `json:"cause"`
	Score       float64          `json:"score"`
	Max         float64          `json:"max"`
	Fit         float64          `json:"fit"`         // 0..1
	Probability float64          `json:"probability"` // normalised across hypotheses
	Evidence    []EvidenceResult `json:"evidence"`
	FirstMover  bool             `json:"firstMover"`
}

type Heatmap struct {
	Times   []time.Time `json:"times"`
	Signals []string    `json:"signals"`
	Z       [][]float64 `json:"z"` // [signal][time]
	TripCol int         `json:"tripCol"`
}

type GroundTruth struct {
	Scenario catalog.Scenario `json:"scenario"`
	Injected *time.Time       `json:"injected,omitempty"`
	Match    bool             `json:"match"`
	Rank     int              `json:"rank"` // rank of the true cause in hypotheses, 0 = absent
}

type Result struct {
	UnitID       int                   `json:"unitId"`
	Fault        catalog.Fault         `json:"fault"`
	TripTime     time.Time             `json:"tripTime"`
	TripSim      int64                 `json:"tripSim"`
	WindowStart  time.Time             `json:"windowStart"`
	WindowEnd    time.Time             `json:"windowEnd"`
	BaselineEnd  time.Time             `json:"baselineEnd"`
	PreSamples   int                   `json:"preSamples"`
	SimPerSample float64               `json:"simPerSample"`
	Resolution   string                `json:"resolution"` // good | coarse | poor
	Signals      []SignalResult        `json:"signals"`
	Chain        []ChainNode           `json:"chain"`
	Hypotheses   []Hypothesis          `json:"hypotheses"`
	Heatmap      Heatmap               `json:"heatmap"`
	GroundTruth  *GroundTruth          `json:"groundTruth,omitempty"`
	Why          []string              `json:"why"`
	Series       map[string][]*float64 `json:"series"`
	SeriesTimes  []time.Time           `json:"seriesTimes"`
	States       []int                 `json:"states"`
	Notes        []string              `json:"notes"`
}

const (
	zSustain = 2.5
	zOnset   = 3.0
)

func Analyze(in Input) Result {
	fault, ok := catalog.FaultByCode(in.FaultCode)
	if !ok {
		fault = catalog.Fault{Code: in.FaultCode, Name: "unknown", Label: "Bilinmeyen arıza", Severity: "trip"}
	}
	res := Result{UnitID: in.UnitID, Fault: fault, TripTime: in.TripTime, Series: map[string][]*float64{}}
	s := in.Samples
	if len(s) == 0 {
		res.Notes = append(res.Notes, "Pencerede okuma yok.")
		return res
	}
	res.WindowStart, res.WindowEnd = s[0].T, s[len(s)-1].T

	// Trip index: first sample at/after the trip time.
	trip := sort.Search(len(s), func(i int) bool { return !s[i].T.Before(in.TripTime) })
	if trip < len(s) {
		res.TripSim = s[trip].Sim
	}
	// Pre-trip segment: contiguous non-FAULT samples before the trip.
	segStart := trip
	for segStart > 0 && s[segStart-1].State != 3 {
		segStart--
	}
	// Prefer the run with the same state as the last pre-trip sample when long enough.
	if trip > 0 {
		last := s[trip-1].State
		j := trip
		for j > segStart && s[j-1].State == last {
			j--
		}
		if trip-j >= 8 {
			segStart = j
		}
	}
	pre := s[segStart:trip]
	res.PreSamples = len(pre)
	if len(pre) >= 2 {
		span := float64(pre[len(pre)-1].Sim - pre[0].Sim)
		res.SimPerSample = span / float64(len(pre)-1)
	}
	switch {
	case res.SimPerSample == 0 || res.SimPerSample <= 2:
		res.Resolution = "good"
	case res.SimPerSample <= 10:
		res.Resolution = "coarse"
	default:
		res.Resolution = "poor"
		res.Notes = append(res.Notes,
			"Her okuma arası ≈ "+fmtF(res.SimPerSample, 0)+" s simülasyon zamanı: olay sıralaması kaba. "+
				"Daha ince analiz için POLL_INTERVAL düşürün veya simülatör -time-scale değerini azaltın.")
	}
	if len(pre) < 4 {
		res.Notes = append(res.Notes, "Trip öncesi çalışma segmenti çok kısa ("+itoa(len(pre))+" okuma); bulgular sınırlı.")
	}

	nBase := max(3, len(pre)*3/10)
	if nBase > len(pre)-1 {
		nBase = max(1, len(pre)-1)
	}
	if len(pre) > 0 {
		res.BaselineEnd = pre[min(nBase, len(pre))-1].T
	}

	// Series for charts.
	res.SeriesTimes = make([]time.Time, len(s))
	res.States = make([]int, len(s))
	for i, x := range s {
		res.SeriesTimes[i] = x.T
		res.States[i] = x.State
	}
	all := append(append([]catalog.Signal{}, catalog.Signals...), catalog.Setpoints...)
	for _, sig := range all {
		arr := make([]*float64, len(s))
		for i, x := range s {
			if v, ok := x.V[sig.Key]; ok {
				vv := v
				arr[i] = &vv
			}
		}
		res.Series[sig.Key] = arr
	}

	// Per-signal deviation.
	tripSignal := fault.Signal
	sigRes := map[string]*SignalResult{}
	zs := map[string][]float64{}
	for _, sig := range catalog.Signals {
		r := SignalResult{Key: sig.Key, Label: sig.Label, Unit: sig.Unit, Group: sig.Group, Dir: "flat"}
		if len(pre) == 0 {
			res.Signals = append(res.Signals, r)
			continue
		}
		base := pre[:min(nBase, len(pre))]
		mean, sd := meanStd(base, sig.Key)
		sigma := math.Max(sd, sig.Floor)
		r.Mean, r.Sigma = mean, sigma
		z := make([]float64, len(s))
		for i, x := range s {
			z[i] = (x.V[sig.Key] - mean) / sigma
		}
		zs[sig.Key] = z
		lastI := trip - 1
		r.Last = s[lastI].V[sig.Key]
		r.Z = z[lastI]
		for i := segStart; i < trip; i++ {
			if math.Abs(z[i]) > math.Abs(r.PeakZ) {
				r.PeakZ = z[i]
			}
			if v := s[i].V[sig.Key]; v < sig.Min || v > sig.Max {
				r.OutOfRange = true
			}
		}
		if math.Abs(r.Z) >= zOnset || r.OutOfRange {
			sign := math.Copysign(1, r.Z)
			on := lastI
			for i := lastI; i >= segStart+min(nBase, len(pre)); i-- {
				if z[i]*sign >= zSustain {
					on = i
				} else {
					break
				}
			}
			r.Deviated = true
			if sign > 0 {
				r.Dir = "up"
			} else {
				r.Dir = "down"
			}
			t, sim := s[on].T, s[on].Sim
			r.Onset, r.OnsetSim = &t, &sim
			r.LeadS = in.TripTime.Sub(t).Seconds()
			if trip < len(s) {
				r.LeadSimS = float64(s[trip].Sim - sim)
			}
		}
		sigRes[sig.Key] = &r
		res.Signals = append(res.Signals, r)
	}
	// Correlation with the trip signal over the pre-trip segment.
	if tripSignal != "" && len(pre) >= 5 {
		for i := range res.Signals {
			c, ok := pearson(pre, res.Signals[i].Key, tripSignal)
			if ok {
				res.Signals[i].Corr = &c
			}
		}
	}
	for i := range res.Signals {
		sigRes[res.Signals[i].Key] = &res.Signals[i]
	}
	// The trip itself proves the trip signal crossed its limit. When that
	// happened between two samples (coarse polling), record it as inferred.
	if t := sigRes[tripSignal]; t != nil && !t.Deviated && trip < len(s) {
		if d := impliedDir(fault.Code); d != "" {
			tt, sim := s[trip].T, s[trip].Sim
			t.Deviated, t.Inferred, t.Dir, t.Onset, t.OnsetSim = true, true, d, &tt, &sim
			res.Notes = append(res.Notes, t.Label+" trip limitini iki okuma arasında geçti; sapma arıza kodundan çıkarıldı.")
		}
	}

	// Setpoint changes and ground truth injection inside the displayed window, before the trip.
	var chain []ChainNode
	spChanged := map[string]bool{}
	from := 0
	for i := from + 1; i < trip && i < len(s); i++ {
		for _, sp := range catalog.Setpoints {
			a, b := s[i-1].V[sp.Key], s[i].V[sp.Key]
			if a != b && i >= segStart-1 {
				spChanged[sp.Key] = true
				chain = append(chain, ChainNode{T: s[i].T, Sim: s[i].Sim, Kind: "action", Signal: sp.Key,
					Title: sp.Label + " değişti", Detail: fmtF(a, 1) + " → " + fmtF(b, 1) + " " + sp.Unit})
			}
		}
	}
	// Ground truth from HR8 (the simulator's cause register).
	gtID := 0
	if trip < len(s) {
		gtID = s[trip].FaultScenario
	}
	if gtID == 0 && trip > 0 {
		gtID = s[trip-1].FaultScenario
	}
	var gt *GroundTruth
	if sc, ok := catalog.ScenarioByID(gtID); ok && gtID != 0 {
		gt = &GroundTruth{Scenario: sc}
		for i := trip - 1; i > 0; i-- {
			if s[i-1].FaultScenario != gtID {
				t := s[i].T
				gt.Injected = &t
				chain = append(chain, ChainNode{T: t, Sim: s[i].Sim, Kind: "cause", Title: "Sebep enjekte edildi (HR8)",
					Detail: sc.Label + " — simülatör referansı, analizde kullanılmaz"})
				break
			}
		}
	}

	// Deviation onsets, earliest first.
	devs := []*SignalResult{}
	for i := range res.Signals {
		if res.Signals[i].Deviated {
			devs = append(devs, &res.Signals[i])
		}
	}
	sort.SliceStable(devs, func(a, b int) bool {
		if !devs[a].Onset.Equal(*devs[b].Onset) {
			return devs[a].Onset.Before(*devs[b].Onset)
		}
		return math.Abs(devs[a].Z) > math.Abs(devs[b].Z)
	})
	for i, d := range devs {
		if i >= 7 {
			break
		}
		detail := fmtF(d.Mean, 1) + " → " + fmtF(d.Last, 1) + " " + d.Unit + " (" + fmtSigned(d.Z, 1) + "σ)"
		if d.Inferred {
			detail = "iki okuma arasında limit aşıldı (arıza kodundan çıkarım)"
		}
		if d.OutOfRange {
			detail = "fiziksel aralık dışı okuma: " + fmtF(d.Last, 1) + " " + d.Unit
		}
		chain = append(chain, ChainNode{T: *d.Onset, Sim: *d.OnsetSim, Kind: "deviation", Signal: d.Key,
			Title: d.Label + dirWord(d.Dir), Detail: detail})
	}
	// Alarm onsets (warning bits) before the trip.
	for _, a := range catalog.Alarms {
		if trip == 0 {
			break
		}
		if s[trip-1].AlarmWord&(1<<a.Bit) == 0 {
			continue
		}
		on := trip - 1
		for on > segStart && s[on-1].AlarmWord&(1<<a.Bit) != 0 {
			on--
		}
		chain = append(chain, ChainNode{T: s[on].T, Sim: s[on].Sim, Kind: "alarm", Signal: a.Signal,
			Title: "Uyarı: " + a.Label, Detail: "Alarm biti " + itoa(a.Bit) + " (DI " + itoa(5+a.Bit) + ")"})
	}
	if trip < len(s) {
		chain = append(chain, ChainNode{T: s[trip].T, Sim: s[trip].Sim, Kind: "trip", Signal: tripSignal,
			Title: "TRIP: " + fault.Label, Detail: "Arıza kodu " + itoa(fault.Code) + " — makine FAULT durumuna geçti"})
		// Consequences: drives cut.
		if v, ok := s[trip].V["extruder_rpm"]; ok && trip > 0 && v < s[trip-1].V["extruder_rpm"]*0.5 {
			chain = append(chain, ChainNode{T: s[trip].T, Sim: s[trip].Sim, Kind: "effect", Signal: "extruder_rpm",
				Title: "Tahrikler durduruldu", Detail: "Vida " + fmtF(s[trip-1].V["extruder_rpm"], 1) + " → " + fmtF(v, 1) + " rpm, üretim sayaçları dondu"})
		}
	}
	sort.SliceStable(chain, func(a, b int) bool {
		if chain[a].T.Equal(chain[b].T) {
			return kindOrder[chain[a].Kind] < kindOrder[chain[b].Kind]
		}
		return chain[a].T.Before(chain[b].T)
	})
	res.Chain = chain

	// Hypotheses.
	earliest := map[string]bool{}
	if len(devs) > 0 {
		for _, d := range devs {
			if d.Onset.Equal(*devs[0].Onset) {
				earliest[d.Key] = true
			}
		}
	}
	for _, c := range catalog.Causes {
		if !contains(c.Faults, in.FaultCode) {
			continue
		}
		h := Hypothesis{Cause: c}
		for _, ev := range c.Evidence {
			er := EvidenceResult{Evidence: ev, Status: "absent"}
			keys := resolve(ev.Signal, in.FaultCode)
			er.Resolved = keys[0]
			h.Max += ev.Weight
			switch ev.Dir {
			case "sp", "nosp":
				changed := false
				for _, k := range keys {
					changed = changed || spChanged[k]
				}
				if changed == (ev.Dir == "sp") {
					er.Status = "match"
				}
				er.Observed = map[bool]string{true: "setpoint değişti", false: "setpoint sabit"}[changed]
			case "flag":
				if trip < len(s) && s[trip].EStop {
					er.Status, er.Observed = "match", "aktif"
				}
			case "jump":
				for _, k := range keys {
					if r := sigRes[k]; r != nil && r.OutOfRange {
						er.Status, er.Resolved = "match", k
						er.Observed = r.Label + " " + fmtF(r.Last, 1) + " " + r.Unit
					}
				}
				if er.Status != "match" {
					er.Observed = "tüm okumalar makul"
				}
			default:
				var r *SignalResult
				for _, k := range keys {
					if x := sigRes[k]; x != nil && (r == nil || math.Abs(x.Z) > math.Abs(r.Z)) {
						r = x
					}
				}
				if r == nil {
					break
				}
				er.Resolved = r.Key
				er.Observed = fmtSigned(r.Z, 1) + "σ (" + fmtF(r.Mean, 1) + " → " + fmtF(r.Last, 1) + " " + r.Unit + ")"
				if r.Inferred {
					er.Observed = "okumalar arasında trip limiti aşıldı (arıza kodundan çıkarım)"
				}
				switch {
				case ev.Dir == "flat" && !r.Deviated:
					er.Status = "match"
				case ev.Dir == "flat":
					er.Status = "contradict"
				case ev.Dir == "any" && r.Deviated:
					er.Status = "match"
				case r.Deviated && r.Dir == ev.Dir && !r.OutOfRange:
					er.Status = "match"
				case r.Deviated && r.Dir != ev.Dir:
					er.Status = "contradict"
				}
				if ev.Primary && er.Status == "match" && earliest[r.Key] {
					h.FirstMover = true
				}
			}
			switch er.Status {
			case "match":
				h.Score += ev.Weight
			case "contradict":
				h.Score -= ev.Weight * 0.5
			}
			h.Evidence = append(h.Evidence, er)
		}
		if h.FirstMover {
			h.Score += 1.5
		}
		h.Max += 1.5
		h.Fit = math.Max(0, h.Score) / h.Max
		res.Hypotheses = append(res.Hypotheses, h)
	}
	sort.SliceStable(res.Hypotheses, func(a, b int) bool { return res.Hypotheses[a].Fit > res.Hypotheses[b].Fit })
	var tot float64
	for _, h := range res.Hypotheses {
		tot += h.Fit * h.Fit
	}
	for i := range res.Hypotheses {
		if tot > 0 {
			res.Hypotheses[i].Probability = res.Hypotheses[i].Fit * res.Hypotheses[i].Fit / tot
		}
	}
	if gt != nil {
		for i, h := range res.Hypotheses {
			if h.Cause.ID == gt.Scenario.Cause {
				gt.Rank = i + 1
				gt.Match = i == 0
			}
		}
		res.GroundTruth = gt
	}

	res.Heatmap = heatmap(s, zs, trip, devs, tripSignal)
	res.Why = fiveWhys(res, sigRes, tripSignal)
	return res
}

var kindOrder = map[string]int{"action": 0, "cause": 1, "deviation": 2, "alarm": 3, "trip": 4, "effect": 5}

func heatmap(s []Sample, zs map[string][]float64, trip int, devs []*SignalResult, tripSignal string) Heatmap {
	hm := Heatmap{TripCol: -1}
	// Order: deviating signals by onset, then the rest by catalog order.
	seen := map[string]bool{}
	for _, d := range devs {
		hm.Signals = append(hm.Signals, d.Key)
		seen[d.Key] = true
	}
	for _, sig := range catalog.Signals {
		if !seen[sig.Key] {
			hm.Signals = append(hm.Signals, sig.Key)
		}
	}
	cols := min(len(s), 90)
	if cols == 0 {
		return hm
	}
	step := float64(len(s)) / float64(cols)
	hm.Z = make([][]float64, len(hm.Signals))
	for si, k := range hm.Signals {
		z := zs[k]
		row := make([]float64, cols)
		for c := 0; c < cols; c++ {
			a, b := int(float64(c)*step), int(float64(c+1)*step)
			b = max(b, a+1)
			var sum float64
			for i := a; i < b && i < len(z); i++ {
				sum += z[i]
			}
			v := 0.0
			if z != nil {
				v = sum / float64(b-a)
			}
			row[c] = math.Round(math.Max(-8, math.Min(8, v))*100) / 100
		}
		hm.Z[si] = row
	}
	for c := 0; c < cols; c++ {
		i := int(float64(c) * step)
		hm.Times = append(hm.Times, s[i].T)
		if hm.TripCol < 0 && i >= trip {
			hm.TripCol = c
		}
	}
	_ = tripSignal
	return hm
}

func fiveWhys(r Result, sig map[string]*SignalResult, tripSignal string) []string {
	var w []string
	if r.Fault.Code == 1 {
		return []string{"Makine durdu çünkü acil stop devresi açıldı.", "Acil stop butonuna basıldı veya güvenlik devresi tetiklendi."}
	}
	if t := sig[tripSignal]; t != nil {
		if t.Inferred {
			w = append(w, "Makine "+r.Fault.Label+" nedeniyle trip etti: "+t.Label+" iki okuma arasında trip limitini aştı (normal ≈ "+
				fmtF(t.Mean, 1)+" "+t.Unit+").")
		} else {
			w = append(w, "Makine "+r.Fault.Label+" nedeniyle trip etti: "+t.Label+" "+fmtF(t.Last, 1)+" "+t.Unit+
				" (normal ≈ "+fmtF(t.Mean, 1)+").")
		}
	} else {
		w = append(w, "Makine "+r.Fault.Label+" nedeniyle trip etti.")
	}
	if len(r.Hypotheses) == 0 || r.Hypotheses[0].Fit < 0.25 {
		w = append(w, "Bilgi tabanındaki hiçbir sebep verilerle güçlü biçimde eşleşmedi; trend ve ısı haritasını inceleyin.")
		return w
	}
	top := r.Hypotheses[0]
	for _, e := range top.Evidence {
		if e.Status == "match" && e.Primary {
			w = append(w, "Çünkü "+lower(e.Note)+" — gözlenen: "+e.Observed+".")
		}
	}
	for _, e := range top.Evidence {
		if e.Status == "match" && !e.Primary {
			w = append(w, "Destekleyen bulgu: "+lower(e.Note)+".")
			break
		}
	}
	w = append(w, "Kök sebep (en olası): "+top.Cause.Label+" ["+top.Cause.Category+"] — "+top.Cause.Detail)
	return w
}

func resolve(key string, code int) []string {
	z := catalog.ZoneSignal(code)
	switch key {
	case "$zone":
		return []string{z}
	case "$zone_sp":
		if z == "" {
			return []string{""}
		}
		return []string{z + "_sp"}
	case "$any_temp":
		return catalog.TempSignals
	}
	return []string{key}
}

func meanStd(xs []Sample, k string) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var m float64
	for _, x := range xs {
		m += x.V[k]
	}
	m /= float64(len(xs))
	var v float64
	for _, x := range xs {
		d := x.V[k] - m
		v += d * d
	}
	if len(xs) > 1 {
		v /= float64(len(xs) - 1)
	}
	return m, math.Sqrt(v)
}

func pearson(xs []Sample, a, b string) (float64, bool) {
	ma, sa := meanStd(xs, a)
	mb, sb := meanStd(xs, b)
	if sa < 1e-9 || sb < 1e-9 {
		return 0, false
	}
	var c float64
	for _, x := range xs {
		c += (x.V[a] - ma) * (x.V[b] - mb)
	}
	c /= float64(len(xs)-1) * sa * sb
	return math.Round(c*100) / 100, true
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func dirWord(d string) string {
	switch d {
	case "up":
		return " yükseliyor"
	case "down":
		return " düşüyor"
	}
	return " sapıyor"
}

// impliedDir is the direction a fault code's trip signal must have moved.
func impliedDir(code int) string {
	switch {
	case code == 10 || code == 11 || code == 40 || (code >= 21 && code <= 24):
		return "up"
	case code == 41 || code == 50 || (code >= 31 && code <= 34):
		return "down"
	}
	return ""
}
