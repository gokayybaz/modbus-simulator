import { useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import type { EventRow, Meta, Stats } from '../api'
import { usePoll, fmt, fmtDur, fmtDateTime, STATE } from '../api'
import { AnimatedNumber, Card, Empty, Seg } from '../components'
import { Chart, baseOption, css } from '../chart'
import { eventView } from './Live'

const RANGES = [{ v: '15m', l: '15 dk' }, { v: '1h', l: '1 sa' }, { v: '6h', l: '6 sa' }, { v: '24h', l: '24 sa' }, { v: '168h', l: '7 gün' }]
const STATE_ORDER = [2, 1, 0, 3]
const stateColor = (s: number) => ({ 0: css('--idle'), 1: css('--warning'), 2: css('--good'), 3: css('--critical') } as Record<number, string>)[s]

export function PerformancePage({ unit, meta, themeKey }: { unit: number; meta: Meta | null; themeKey: string }) {
  const [range, setRange] = useState('1h')
  const stats = usePoll<Stats>(`/api/stats?unit=${unit}&range=${range}`, 5000)
  const events = usePoll<EventRow[]>(`/api/events?unit=${unit}&range=${range}`, 5000)
  const [filter, setFilter] = useState('all')
  const s = stats.data

  const timeline = useMemo(() => {
    if (!s?.segments?.length) return null
    const b = baseOption() as any
    return {
      ...b,
      grid: { left: 8, right: 14, top: 6, bottom: 4, containLabel: true },
      tooltip: { ...b.tooltip, trigger: 'item', formatter: (p: any) => `<b>${STATE[p.value[3]].label}</b> · ${fmtDur(p.value[4])}<br/><span style="opacity:.7">${fmtDateTime(p.value[1])} → ${fmtDateTime(p.value[2])}</span>` },
      xAxis: { ...b.xAxis, min: new Date(s.from).getTime(), max: new Date(s.to).getTime() },
      yAxis: { type: 'category', data: ['Durum'], show: false },
      series: [{
        type: 'custom', encode: { x: [1, 2], y: 0 },
        data: s.segments.map(g => [0, new Date(g.start).getTime(), new Date(g.end).getTime(), g.state, g.durationS]),
        renderItem: (_: any, api: any) => {
          const a = api.coord([api.value(1), 0]), e = api.coord([api.value(2), 0])
          const h = api.size([0, 1])[1] * 0.7
          return { type: 'rect', shape: { x: a[0], y: a[1] - h / 2, width: Math.max(1.5, e[0] - a[0] - 1), height: h, r: 3 }, style: { fill: stateColor(api.value(3)) } }
        },
      }],
    }
  }, [s, themeKey])

  const pareto = useMemo(() => {
    if (!s?.pareto?.length) return null
    const b = baseOption() as any
    const p = [...s.pareto].sort((a, c) => a.count - c.count)
    return {
      ...b,
      grid: { left: 8, right: 30, top: 6, bottom: 4, containLabel: true },
      tooltip: { ...b.tooltip, trigger: 'item', formatter: (q: any) => `<b>${q.value}</b> kez · #${p[q.dataIndex].code} ${p[q.dataIndex].label}` },
      xAxis: { type: 'value', axisLabel: { color: css('--text-muted') }, splitLine: { lineStyle: { color: css('--grid') } }, minInterval: 1 },
      yAxis: { type: 'category', data: p.map(x => `#${x.code} ${x.label}`), axisLabel: { color: css('--text-secondary'), fontSize: 11 }, axisLine: { lineStyle: { color: css('--axis') } }, axisTick: { show: false } },
      series: [{ type: 'bar', barMaxWidth: 22, data: p.map(x => x.count), itemStyle: { color: css('--series-1'), borderRadius: [0, 4, 4, 0] }, label: { show: true, position: 'right', color: css('--text-primary'), fontWeight: 600 } }],
    }
  }, [s, themeKey])

  const dist = useMemo(() => {
    if (!s?.totalSeconds) return null
    const b = baseOption() as any
    return {
      ...b,
      grid: { left: 0, right: 0, top: 0, bottom: 0 },
      tooltip: { ...b.tooltip, trigger: 'item', formatter: (q: any) => `<b>%${fmt(q.value, 1)}</b> ${q.seriesName} · ${fmtDur(s.stateSeconds[q.seriesId] ?? 0)}` },
      xAxis: { type: 'value', max: 100, show: false }, yAxis: { type: 'category', data: [''], show: false },
      series: STATE_ORDER.filter(k => s.stateSeconds[k]).map(k => ({
        id: String(k), name: STATE[k].label, type: 'bar', stack: 'a', barWidth: 26, data: [(s.stateSeconds[k] / s.totalSeconds) * 100],
        itemStyle: { color: stateColor(k), borderColor: css('--surface-1'), borderWidth: 2, borderRadius: 4 },
      })),
    }
  }, [s, themeKey])

  const ev = (events.data ?? []).filter(e => filter === 'all' || (filter === 'fault' ? e.event_type.startsWith('fault') : filter === 'state' ? e.event_type === 'state_change' : e.event_type.startsWith('comm') || e.event_type === 'collector_start'))

  const tiles = [
    { l: 'Kullanılabilirlik', v: s?.availability !== undefined ? s.availability * 100 : null, u: '%', d: 1, hint: 'RUNNING süresi / toplam' },
    { l: 'MTBF', txt: fmtDur(s?.mtbfS), hint: 'arızalar arası ort. çalışma süresi' },
    { l: 'MTTR', txt: fmtDur(s?.mttrS), hint: 'ort. FAULT durumunda kalma süresi' },
    { l: 'Arıza sayısı', v: s?.faultCount ?? null, u: '', d: 0 },
    { l: 'Üretilen boru', v: s?.producedM ?? null, u: 'm', d: 1 },
    { l: 'Üretilen kütle', v: s?.producedKg ?? null, u: 'kg', d: 0 },
    { l: 'Enerji', v: s?.energyKWh ?? null, u: 'kWh', d: 1 },
    { l: 'Özgül enerji', v: s?.specificEnergy ?? null, u: 'kWh/kg', d: 3 },
  ]

  return (
    <div className="grid">
      <div className="toolbar" style={{ marginBottom: 0 }}><Seg value={range} options={RANGES} onChange={setRange} /></div>
      <div className="kpis">
        {tiles.map(t => (
          <Card key={t.l} className="kpi">
            <div className="kpi-label">{t.l}</div>
            <div className="kpi-value tnum">{t.txt ?? <><AnimatedNumber value={t.v} digits={t.d} /><small>{t.u}</small></>}</div>
            <div className="kpi-sp">{t.hint ?? ' '}</div>
          </Card>
        ))}
      </div>
      <Card title="Durum zaman çizelgesi" right={<div className="row" style={{ gap: 12, fontSize: 12 }}>{STATE_ORDER.map(k => <span key={k} className="row" style={{ gap: 5 }}><i style={{ width: 10, height: 10, borderRadius: 3, background: STATE[k].color }} />{STATE[k].label}</span>)}</div>}>
        {timeline ? <Chart option={timeline} height={70} themeKey={themeKey} notMerge /> : <Empty>Veri yok</Empty>}
        {dist && <div style={{ marginTop: 10 }}><Chart option={dist} height={26} themeKey={themeKey} notMerge /></div>}
      </Card>
      <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 460px), 1fr))' }}>
        <Card title="Arıza Pareto" sub="koda göre adet">
          {pareto ? <Chart option={pareto} height={Math.max(160, (s?.pareto.length ?? 0) * 34 + 30)} themeKey={themeKey} notMerge /> : <Empty>Bu aralıkta arıza yok 🎉</Empty>}
        </Card>
        <Card title="Olay günlüğü" right={<Seg value={filter} onChange={setFilter} options={[{ v: 'all', l: 'Tümü' }, { v: 'fault', l: 'Arıza' }, { v: 'state', l: 'Durum' }, { v: 'comm', l: 'Haberleşme' }]} />}>
          <div className="feed" style={{ maxHeight: 420 }}>
            <AnimatePresence initial={false}>
              {ev.map(e => {
                const v = eventView(e, meta)
                return (
                  <motion.div key={e.id} className="feed-item" layout initial={{ opacity: 0, y: -6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}>
                    {v.icon}<span>{v.text}{e.message && e.event_type !== 'fault' ? <span className="muted"> · {e.message}</span> : null}</span><span className="t">{fmtDateTime(e.ts)}</span>
                  </motion.div>
                )
              })}
            </AnimatePresence>
            {ev.length === 0 && <Empty>Olay yok</Empty>}
          </div>
        </Card>
      </div>
    </div>
  )
}
