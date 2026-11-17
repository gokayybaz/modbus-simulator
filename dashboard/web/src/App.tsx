import { useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Activity, BarChart3, Factory, LineChart, Microscope, Moon, Sun } from 'lucide-react'
import type { Live, Meta, UnitSummary } from './api'
import { usePoll, getJSON, STATE } from './api'
import { LivePage } from './pages/Live'
import { TrendsPage } from './pages/Trends'
import { RcaPage } from './pages/Rca'
import { PerformancePage } from './pages/Performance'

type Tab = 'live' | 'trends' | 'rca' | 'perf'
const TABS: { id: Tab; label: string; icon: any }[] = [
  { id: 'live', label: 'Canlı İzleme', icon: Activity },
  { id: 'trends', label: 'Trendler', icon: LineChart },
  { id: 'rca', label: 'Kök Sebep Analizi', icon: Microscope },
  { id: 'perf', label: 'Performans & Olaylar', icon: BarChart3 },
]

function store(k: string, v?: string) {
  try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v) } catch { /* private mode */ }
  return null
}

export default function App() {
  const [tab, setTab] = useState<Tab>(() => (location.hash.slice(1) as Tab) || 'live')
  const [theme, setTheme] = useState<string>(() => store('theme') ?? (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'))
  const [meta, setMeta] = useState<Meta | null>(null)
  const [unit, setUnit] = useState<number | null>(() => Number(store('unit')) || null)

  useEffect(() => { document.documentElement.dataset.theme = theme; store('theme', theme) }, [theme])
  useEffect(() => { history.replaceState(null, '', `#${tab}`); window.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const on = () => { const h = location.hash.slice(1) as Tab; if (TABS.some(t => t.id === h)) setTab(h) }
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  }, [])
  useEffect(() => { getJSON<Meta>('/api/meta').then(setMeta).catch(() => {}) }, [])

  const pollMs = meta?.pollMs && meta.pollMs > 0 ? Math.max(500, meta.pollMs) : 1000
  const units = usePoll<UnitSummary[]>('/api/units', 3000)
  useEffect(() => {
    const list = units.data
    if (list?.length && (unit === null || !list.some(u => u.unit_id === unit))) setUnit(list[0].unit_id)
  }, [units.data, unit])
  useEffect(() => { if (unit !== null) store('unit', String(unit)) }, [unit])

  const live = usePoll<Live>(unit !== null && tab === 'live' ? `/api/live?unit=${unit}&window=180` : null, pollMs)
  const online = units.data?.find(u => u.unit_id === unit)?.online
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [])
  const lastAge = live.updated ? Math.round((now - live.updated) / 1000) : null

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark"><Factory size={17} /></div>
          <div>Ekstrüzyon Kontrol Merkezi<small>Modbus → TimescaleDB · canlı izleme ve kök sebep analizi</small></div>
        </div>
        <nav className="tabs" role="tablist">
          {TABS.map(t => (
            <button key={t.id} role="tab" className="tab" aria-selected={tab === t.id} onClick={() => setTab(t.id)}>
              {tab === t.id && <motion.span layoutId="tabpill" className="tab-pill" transition={{ type: 'spring', stiffness: 400, damping: 34 }} />}
              <t.icon size={15} /><span>{t.label}</span>
            </button>
          ))}
        </nav>
        <span className="spacer" />
        <div className="units" role="group" aria-label="Hat seçimi">
          {units.data?.map(u => {
            const st = STATE[u.machine_state] ?? STATE[0]
            return (
              <button key={u.unit_id} className="unit-chip" aria-pressed={u.unit_id === unit} onClick={() => setUnit(u.unit_id)} title={`${st.label}${u.online ? '' : ' · veri gecikmeli'}`}>
                <span className={`dot ${u.online ? 'live' : ''}`} style={{ background: u.online ? st.color : 'var(--idle)', color: st.color }} />
                Hat {u.unit_id}
              </button>
            )
          })}
        </div>
        <div className="poll" title="Veritabanı polling durumu">
          <span className={`dot ${online ? 'live' : ''}`} style={{ background: online ? 'var(--good)' : 'var(--serious)', color: 'var(--good)' }} />
          {units.error ? 'API erişilemiyor' : online ? `canlı · ${pollMs / 1000}s` : 'veri yok'}
          {tab === 'live' && lastAge !== null && <span className="tnum">· {lastAge}s</span>}
        </div>
        <button className="icon-btn" onClick={() => setTheme(t => (t === 'dark' ? 'light' : 'dark'))} title="Tema" aria-label="Tema değiştir">
          {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
        </button>
      </header>
      <main>
        {units.data && units.data.length === 0 && <div className="note">Veritabanında henüz okuma yok. Collector ve simülatörün çalıştığından emin olun.</div>}
        <AnimatePresence mode="wait">
          <motion.div key={tab + theme} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -6 }} transition={{ duration: 0.22 }}>
            {unit !== null && tab === 'live' && <LivePage live={live.data} meta={meta} themeKey={theme} />}
            {unit !== null && tab === 'trends' && <TrendsPage unit={unit} themeKey={theme} />}
            {unit !== null && tab === 'rca' && <RcaPage unit={unit} meta={meta} themeKey={theme} />}
            {unit !== null && tab === 'perf' && <PerformancePage unit={unit} meta={meta} themeKey={theme} />}
          </motion.div>
        </AnimatePresence>
      </main>
    </div>
  )
}
