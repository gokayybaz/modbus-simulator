import { useMemo, useState } from 'react'
import { Pause, Play } from 'lucide-react'
import type { Trend } from '../api'
import { usePoll, fmt } from '../api'
import { Card, Empty, Seg } from '../components'
import { Chart, baseOption, css, tipRow } from '../chart'

interface Panel { id: string; title: string; unit: string; series: { key: string; label: string; slot: number; dashed?: boolean; step?: boolean }[]; warn?: number; trip?: number; digits?: number }

// One measure (one unit) per chart: never a dual axis.
const PANELS: Panel[] = [
  { id: 'p', title: 'Eriyik basıncı', unit: 'bar', series: [{ key: 'melt_pressure', label: 'Basınç (ort.)', slot: 1 }, { key: 'melt_pressure_max', label: 'Basınç (maks.)', slot: 2 }], warn: 300, trip: 350, digits: 1 },
  { id: 'l', title: 'Motor yükü', unit: '%', series: [{ key: 'motor_load', label: 'Motor yükü (ort.)', slot: 1 }, { key: 'motor_load_max', label: 'Motor yükü (maks.)', slot: 2 }], warn: 95, trip: 110, digits: 1 },
  { id: 't', title: 'Sıcaklıklar', unit: '°C', series: [
    { key: 'zone1_temp', label: 'Zon 1', slot: 1 }, { key: 'zone2_temp', label: 'Zon 2', slot: 2 }, { key: 'zone3_temp', label: 'Zon 3', slot: 3 },
    { key: 'die_temp', label: 'Kalıp', slot: 4 }, { key: 'melt_temp', label: 'Eriyik', slot: 5 }], digits: 1 },
  { id: 'r', title: 'Vida devri', unit: 'rpm', series: [{ key: 'extruder_rpm', label: 'Gerçek', slot: 1 }, { key: 'extruder_rpm_sp', label: 'Setpoint', slot: 2, dashed: true, step: true }], digits: 1 },
  { id: 'h', title: 'Çekici hızı', unit: 'm/dk', series: [{ key: 'haul_off_speed', label: 'Gerçek', slot: 1 }, { key: 'haul_off_speed_sp', label: 'Setpoint', slot: 2, dashed: true, step: true }], digits: 2 },
  { id: 'v', title: 'Vakum', unit: 'mbar', series: [{ key: 'vacuum', label: 'Vakum', slot: 1 }], warn: 300, trip: 200, digits: 0 },
  { id: 'c', title: 'Soğutma suyu', unit: '°C', series: [{ key: 'cooling_water_temp', label: 'Su sıcaklığı', slot: 1 }], warn: 25, trip: 30, digits: 1 },
  { id: 'q', title: 'Debi', unit: 'kg/h', series: [{ key: 'throughput', label: 'Debi', slot: 1 }], digits: 1 },
  { id: 'e', title: 'Güç tüketimi', unit: 'kW', series: [{ key: 'power_consumption', label: 'Güç', slot: 1 }], digits: 1 },
  { id: 'a', title: 'Motor akımı', unit: 'A', series: [{ key: 'motor_current', label: 'Akım', slot: 1 }], digits: 1 },
  { id: 'od', title: 'Boru dış çapı', unit: 'mm', series: [{ key: 'pipe_outer_diameter', label: 'Dış çap', slot: 1 }], digits: 2 },
  { id: 'w', title: 'Et kalınlığı', unit: 'mm', series: [{ key: 'pipe_wall_thickness', label: 'Et kalınlığı', slot: 1 }], digits: 2 },
]

const RANGES = [
  { v: '5m', l: '5 dk', poll: 2000 }, { v: '15m', l: '15 dk', poll: 3000 }, { v: '1h', l: '1 sa', poll: 5000 },
  { v: '6h', l: '6 sa', poll: 15000 }, { v: '24h', l: '24 sa', poll: 30000 }, { v: '168h', l: '7 gün', poll: 60000 },
]

/** Contiguous state ranges → markArea pairs (fault red, stopped/heating subtle). */
export function stateAreas(times: any[], states: any[]) {
  const out: any[] = []
  let i = 0
  while (i < states.length) {
    const s = states[i]
    let j = i
    while (j + 1 < states.length && states[j + 1] === s) j++
    if (s === 3 || s === 0 || s === 1) {
      const color = s === 3 ? css('--critical') : s === 1 ? css('--warning') : css('--text-muted')
      out.push([{ xAxis: times[i], itemStyle: { color, opacity: s === 3 ? 0.16 : 0.06 } }, { xAxis: times[Math.min(j + 1, times.length - 1)] }])
    }
    i = j + 1
  }
  return out
}

export function TrendsPage({ unit, themeKey }: { unit: number; themeKey: string }) {
  const [range, setRange] = useState('15m')
  const [paused, setPaused] = useState(false)
  const rd = RANGES.find(r => r.v === range)!
  const { data, loading, error } = usePoll<Trend>(`/api/trend?unit=${unit}&range=${range}`, paused ? 0 : rd.poll)
  const [hidden, setHidden] = useState<Record<string, boolean>>({})

  const options = useMemo(() => {
    if (!data?.series?.t) return []
    const s = data.series
    const t = s.t
    const areas = stateAreas(t, s.machine_state)
    return PANELS.map(p => {
      const b = baseOption() as any
      const series: any[] = p.series.map((ser, i) => ({
        name: ser.label, type: 'line', showSymbol: false, symbolSize: 8, step: ser.step ? 'end' : undefined,
        sampling: 'lttb', lineStyle: { width: 2, type: ser.dashed ? 'dashed' : 'solid' },
        color: css(`--series-${ser.slot}`), data: t.map((x: any, k: number) => [x, s[ser.key]?.[k]]),
        emphasis: { focus: 'series' },
        ...(i === 0 ? {
          markArea: { silent: true, data: areas },
          markLine: p.warn !== undefined ? {
            silent: true, symbol: 'none', label: { color: css('--text-muted'), fontSize: 10, formatter: (q: any) => q.name },
            data: [
              { yAxis: p.warn, name: 'uyarı', lineStyle: { color: css('--warning'), type: 'dashed', width: 1 } },
              { yAxis: p.trip, name: 'trip', lineStyle: { color: css('--critical'), type: 'dashed', width: 1 } },
            ],
          } : undefined,
        } : {}),
      }))
      return {
        id: p.id, title: p.title, unit: p.unit,
        option: {
          ...b,
          legend: p.series.length > 1 ? { top: 0, right: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 3, textStyle: { color: css('--text-secondary'), fontSize: 11 } } : undefined,
          grid: { ...b.grid, top: p.series.length > 1 ? 30 : 14 },
          tooltip: {
            ...b.tooltip,
            formatter: (ps: any[]) => {
              const head = `<div style="opacity:.7;margin-bottom:2px">${new Date(ps[0].value[0]).toLocaleString('tr-TR')}</div>`
              return head + ps.map(q => tipRow(q.color, q.seriesName, q.value[1] == null ? '—' : `${fmt(q.value[1], p.digits ?? 1)} ${p.unit}`)).join('')
            },
          },
          yAxis: { ...b.yAxis, axisLabel: { ...b.yAxis.axisLabel, formatter: (v: number) => v.toLocaleString('tr-TR', { maximumFractionDigits: 3 }) } },
          dataZoom: [{ type: 'inside', throttle: 50 }],
          series,
        },
      }
    })
  }, [data, themeKey])

  const visible = PANELS.filter(p => !hidden[p.id])

  return (
    <div>
      <div className="toolbar">
        <Seg value={range} options={RANGES} onChange={setRange} />
        <button className="icon-btn" onClick={() => setPaused(p => !p)} title={paused ? 'Canlı güncellemeyi başlat' : 'Duraklat'}>{paused ? <Play size={16} /> : <Pause size={16} />}</button>
        <span className="muted" style={{ fontSize: 12.5 }}>
          {data ? <>Kova: {data.bucketS} sn · {data.series?.t?.length ?? 0} nokta · tüm grafikler senkron (yakınlaştırma: tekerlek / sürükle)</> : 'Yükleniyor…'}
        </span>
        <span className="spacer" />
        <div className="row wrap" style={{ gap: 6 }}>
          {PANELS.map(p => (
            <button key={p.id} className="badge" style={{ cursor: 'pointer', opacity: hidden[p.id] ? 0.45 : 1 }} onClick={() => setHidden(h => ({ ...h, [p.id]: !h[p.id] }))}>{p.title}</button>
          ))}
        </div>
      </div>
      <div className="row wrap" style={{ gap: 14, marginBottom: 10, fontSize: 12 }}>
        <span className="row" style={{ gap: 6 }}><i style={{ width: 14, height: 10, background: 'var(--critical)', opacity: .35, borderRadius: 2 }} />Arıza (FAULT)</span>
        <span className="row" style={{ gap: 6 }}><i style={{ width: 14, height: 10, background: 'var(--warning)', opacity: .25, borderRadius: 2 }} />Isınma</span>
        <span className="row" style={{ gap: 6 }}><i style={{ width: 14, height: 10, background: 'var(--text-muted)', opacity: .25, borderRadius: 2 }} />Durdu</span>
        <span className="row" style={{ gap: 6 }}><i style={{ width: 14, height: 0, borderTop: '1px dashed var(--warning)' }} />Uyarı eşiği</span>
        <span className="row" style={{ gap: 6 }}><i style={{ width: 14, height: 0, borderTop: '1px dashed var(--critical)' }} />Trip eşiği</span>
      </div>
      {error && <div className="note">Veri alınamadı: {error}</div>}
      {data && !data.series?.t && <Empty>Seçili aralıkta veri yok.</Empty>}
      <div className={`chart-grid ${loading ? 'loading' : ''}`}>
        {options.filter(o => visible.some(v => v.id === o.id)).map(o => (
          <Card key={o.id} title={o.title} sub={o.unit}>
            <Chart option={o.option} group="trend" themeKey={themeKey} />
          </Card>
        ))}
      </div>
    </div>
  )
}
