import { useMemo } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Activity, AlertTriangle, CircleCheck, Flame, Gauge as GaugeIcon, Power, PlugZap, Wifi, WifiOff } from 'lucide-react'
import type { Live as LiveT, Meta, EventRow } from '../api'
import { STATE, fmt, fmtTime, fmtDur, ago } from '../api'
import { AnimatedNumber, Card, Empty, Gauge, Sparkline } from '../components'
import { Schematic } from '../Schematic'
import { Chart, baseOption, css } from '../chart'

export function eventView(e: EventRow, meta: Meta | null) {
  const fault = meta?.faults.find(f => f.code === e.fault_code)
  switch (e.event_type) {
    case 'fault': return { icon: <AlertTriangle size={15} color="var(--critical)" />, text: `Arıza ${e.fault_code}: ${fault?.label ?? ''}`, tone: 'critical' }
    case 'fault_cleared': return { icon: <CircleCheck size={15} color="var(--good)" />, text: `Arıza giderildi (${e.fault_code})`, tone: 'good' }
    case 'state_change': return { icon: <Activity size={15} color="var(--series-1)" />, text: `${stateTr(e.from_state)} → ${stateTr(e.to_state)}`, tone: '' }
    case 'comm_lost': return { icon: <WifiOff size={15} color="var(--serious)" />, text: `Haberleşme kesildi`, tone: 'serious' }
    case 'comm_restored': return { icon: <Wifi size={15} color="var(--good)" />, text: 'Haberleşme geri geldi', tone: 'good' }
    case 'collector_start': return { icon: <PlugZap size={15} color="var(--text-muted)" />, text: 'Collector başladı', tone: '' }
  }
  return { icon: <Activity size={15} />, text: e.event_type, tone: '' }
}
const stateTr = (s: string | null) => ({ STOPPED: 'Durdu', HEATING: 'Isınıyor', RUNNING: 'Çalışıyor', FAULT: 'Arıza' } as Record<string, string>)[s ?? ''] ?? s ?? '—'

export function LivePage({ live, meta, themeKey }: { live: LiveT | null; meta: Meta | null; themeKey: string }) {
  const r = live?.latest
  const rec = live?.recent ?? {}

  const zoneOption = useMemo(() => {
    if (!r) return null
    const zones = [['Zon 1', 'zone1_temp'], ['Zon 2', 'zone2_temp'], ['Zon 3', 'zone3_temp'], ['Kalıp', 'die_temp']]
    const vals = zones.map(([, k]) => r[k] as number)
    const sps = zones.map(([, k]) => r[k + '_sp'] as number)
    const color = (v: number, sp: number) => v - sp > 15 ? css('--critical') : sp - v > 10 ? css('--series-1') : css('--series-2')
    const b = baseOption() as any
    return {
      ...b,
      grid: { left: 8, right: 16, top: 16, bottom: 4, containLabel: true },
      tooltip: { ...b.tooltip, trigger: 'item', formatter: (p: any) => `<b>${fmt(vals[p.dataIndex], 1)} °C</b> ${zones[p.dataIndex][0]}<br/><span style="opacity:.7">SP ${fmt(sps[p.dataIndex], 1)} °C · Δ ${fmt(vals[p.dataIndex] - sps[p.dataIndex], 1)}</span>` },
      xAxis: { type: 'category', data: zones.map(z => z[0]), axisLine: { lineStyle: { color: css('--axis') } }, axisTick: { show: false }, axisLabel: { color: css('--text-secondary') } },
      yAxis: { ...b.yAxis, type: 'value', scale: false, min: (v: any) => Math.max(0, Math.floor((v.min - 40) / 10) * 10), name: '°C', nameTextStyle: { color: css('--text-muted') } },
      series: [{
        type: 'bar', barWidth: '46%', data: vals.map((v, i) => ({ value: Math.min(v, 520), itemStyle: { color: color(v, sps[i]), borderRadius: [4, 4, 0, 0] } })),
        label: { show: true, position: 'top', color: css('--text-primary'), fontWeight: 600, formatter: (p: any) => fmt(vals[p.dataIndex], 1) },
        markLine: undefined,
      }, {
        type: 'custom', data: sps.map((s, i) => [i, s]), z: 5, tooltip: { show: false },
        renderItem: (_: any, api: any) => {
          const p = api.coord([api.value(0), api.value(1)])
          const w = api.size([1, 0])[0] * 0.62
          const hi = api.coord([api.value(0), api.value(1) + 15])[1], lo = api.coord([api.value(0), api.value(1) - 10])[1]
          return { type: 'group', children: [
            { type: 'rect', shape: { x: p[0] - w / 2, y: hi, width: w, height: lo - hi }, style: { fill: css('--text-muted'), opacity: 0.12 } },
            { type: 'line', shape: { x1: p[0] - w / 2, y1: p[1], x2: p[0] + w / 2, y2: p[1] }, style: { stroke: css('--text-primary'), lineWidth: 2, lineDash: [4, 3] } },
          ] }
        },
      }],
    }
  }, [r, themeKey])

  if (!live) return <div className="grid"><div className="skeleton" style={{ height: 120 }} /><div className="skeleton" style={{ height: 300 }} /></div>
  if (!r) return <Empty>Bu hat için henüz veri yok. Collector çalışıyor mu?</Empty>

  const st = STATE[r.machine_state] ?? STATE[0]
  const fault = meta?.faults.find(f => f.code === r.active_fault_code)
  const scen = meta?.scenarios.find(s => s.id === r.fault_scenario)
  const spark = (k: string) => (rec[k] ?? []) as (number | null)[]
  const specE = r.throughput > 1 ? r.power_consumption / r.throughput : null
  const specSeries = (rec.power_consumption ?? []).map((p: number, i: number) => (rec.throughput?.[i] > 1 ? p / rec.throughput[i] : null))

  const kpis: { k: string; label: string; unit: string; d: number; sp?: string; icon?: any; color: string; data?: (number | null)[]; value?: number | null }[] = [
    { k: 'extruder_rpm', label: 'Vida devri', unit: 'rpm', d: 1, sp: 'extruder_rpm_sp', color: 'var(--series-1)' },
    { k: 'throughput', label: 'Debi', unit: 'kg/h', d: 1, color: 'var(--series-3)' },
    { k: 'power_consumption', label: 'Güç', unit: 'kW', d: 1, color: 'var(--series-4)' },
    { k: 'spec', label: 'Özgül enerji', unit: 'kWh/kg', d: 3, color: 'var(--series-7)', data: specSeries, value: specE },
    { k: 'haul_off_speed', label: 'Çekici hızı', unit: 'm/dk', d: 2, sp: 'haul_off_speed_sp', color: 'var(--series-5)' },
    { k: 'pipe_outer_diameter', label: 'Dış çap', unit: 'mm', d: 2, color: 'var(--series-6)' },
    { k: 'pipe_wall_thickness', label: 'Et kalınlığı', unit: 'mm', d: 2, color: 'var(--series-2)' },
    { k: 'melt_temp', label: 'Eriyik sıcaklığı', unit: '°C', d: 1, color: 'var(--series-8)' },
  ]

  return (
    <div className="grid">
      <Card className="hero">
        <motion.div className={`state-orb ${r.machine_state === 3 ? 'fault' : ''}`} style={{ color: st.color }} key={r.machine_state}
          initial={{ scale: 0.6, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={{ type: 'spring', stiffness: 200, damping: 14 }}>
          <span className="ring" />
          {r.machine_state === 3 ? <AlertTriangle size={30} /> : r.machine_state === 1 ? <Flame size={30} /> : r.machine_state === 2 ? <GaugeIcon size={30} /> : <Power size={30} />}
        </motion.div>
        <div>
          <div className="row wrap" style={{ gap: 12 }}>
            <span className="hero-title" style={{ color: st.color }}>{st.label}</span>
            {fault && <span className="badge critical"><AlertTriangle size={13} /> {fault.code} · {fault.label}</span>}
            {!!r.alarm_word && r.machine_state !== 3 && <span className="badge warning"><AlertTriangle size={13} /> Aktif uyarı</span>}
            {scen && scen.id !== 0 && <span className="badge serious" title="Simülatör sebep register'ı (HR8)">HR8 · {scen.label}</span>}
            {!r.online && <span className="badge serious"><WifiOff size={13} /> Veri gecikmeli · {fmtDur(r.ageS)}</span>}
          </div>
          <div className="hero-meta">
            <div><span>Son okuma</span><span>{fmtTime(r.ts)}</span></div>
            <div><span>Simülasyon zamanı</span><span>{fmtDur(r.sim_time_s)}</span></div>
            <div><span>Toplam üretim</span><span><AnimatedNumber value={r.total_produced_length} digits={1} /> m · <AnimatedNumber value={r.total_produced_weight} digits={0} /> kg</span></div>
            <div><span>Hedef</span><span>{r.production_target > 0 ? `${fmt(r.production_target, 0)} m ${r.target_reached ? '✓' : ''}` : '—'}</span></div>
            <div><span>Son arıza</span><span>{r.last_fault_code ? `${r.last_fault_code} · ${meta?.faults.find(f => f.code === r.last_fault_code)?.label ?? ''}` : '—'}</span></div>
          </div>
        </div>
      </Card>

      <Card title="Proses akışı" sub="animasyon hızları canlı değerlere bağlı" className={r.machine_state === 2 ? '' : 'paused-soft'}>
        <Schematic r={r} />
      </Card>

      <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))' }}>
        <Card><Gauge label="Eriyik basıncı" unit="bar" value={r.melt_pressure} min={0} max={400} warn={300} trip={350} /></Card>
        <Card><Gauge label="Motor yükü" unit="%" value={r.motor_load} min={0} max={130} warn={95} trip={110} /></Card>
        <Card><Gauge label="Vakum" unit="mbar" value={r.vacuum} min={0} max={700} warn={300} trip={200} dir="low" /></Card>
        <Card><Gauge label="Soğutma suyu" unit="°C" value={r.cooling_water_temp} min={5} max={40} warn={25} trip={30} digits={1} /></Card>
      </div>

      <div className="kpis">
        {kpis.map(k => {
          const v = k.value !== undefined ? k.value : r[k.k]
          return (
            <Card key={k.k} className="kpi">
              <div className="kpi-label"><span style={{ width: 10, height: 2, background: k.color, borderRadius: 1 }} />{k.label}</div>
              <div className="kpi-value tnum"><AnimatedNumber value={v} digits={k.d} /><small>{k.unit}</small></div>
              <div className="kpi-sp">{k.sp ? <>SP {fmt(r[k.sp], k.d)} {k.unit} · Δ {fmt(v - r[k.sp], k.d)}</> : <>son 3 dk</>}</div>
              <Sparkline data={k.data ?? spark(k.k)} color={k.color} />
            </Card>
          )
        })}
      </div>

      <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 420px), 1fr))' }}>
        <Card title="Sıcaklık profili" sub="kesikli çizgi = setpoint, gri bant = uyarı bandı (−10/+15 °C)">
          {zoneOption && <Chart option={zoneOption} height={250} themeKey={themeKey} />}
        </Card>
        <Card title="Alarm lambaları" sub="IR26 / DI 5–13">
          <div className="alarm-grid">
            {meta?.alarms.map(a => (
              <div key={a.bit} className={`lamp ${r[a.column] ? 'on' : ''}`}><i />{a.label}</div>
            ))}
          </div>
          <h3 className="card-h" style={{ marginTop: 16 }}>Son olaylar</h3>
          <div className="feed">
            <AnimatePresence initial={false}>
              {live.events.map(e => {
                const v = eventView(e, meta)
                return (
                  <motion.div key={e.id} className="feed-item" layout initial={{ opacity: 0, x: -12 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0 }}>
                    {v.icon}<span>{v.text}</span><span className="t" title={new Date(e.ts).toLocaleString('tr-TR')}>{ago(e.ts)}</span>
                  </motion.div>
                )
              })}
            </AnimatePresence>
            {live.events.length === 0 && <span className="muted">Olay yok</span>}
          </div>
        </Card>
      </div>
    </div>
  )
}
