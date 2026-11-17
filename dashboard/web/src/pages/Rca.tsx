import { useEffect, useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import {
  AlertTriangle, Bell, Check, ChevronDown, CircleDot, FlaskConical, Hand, Info, Minus, Search, ShieldCheck, ShieldX, TrendingDown, TrendingUp, X, Zap,
} from 'lucide-react'
import type { FaultRow, Meta, RcaResult, Hypothesis, ChainNode } from '../api'
import { usePoll, fmt, fmtDateTime, fmtTime, fmtDur, ago } from '../api'
import { Card, Empty, Seg } from '../components'
import { Chart, baseOption, css, tipRow } from '../chart'
import { stateAreas } from './Trends'

const LOOKBACK = [{ v: '2m', l: '2 dk' }, { v: '5m', l: '5 dk' }, { v: '10m', l: '10 dk' }, { v: '30m', l: '30 dk' }, { v: '1h', l: '1 sa' }]

export function RcaPage({ unit, meta, themeKey }: { unit: number; meta: Meta | null; themeKey: string }) {
  const faults = usePoll<FaultRow[]>(`/api/faults?unit=${unit}&range=168h`, 5000)
  const [sel, setSel] = useState<FaultRow | null>(null)
  const [lookback, setLookback] = useState('10m')

  useEffect(() => { setSel(null) }, [unit])
  useEffect(() => {
    if (!sel && faults.data?.length) setSel(faults.data[0])
  }, [faults.data, sel])

  const path = sel ? `/api/rca?unit=${unit}&ts=${encodeURIComponent(sel.ts)}&code=${sel.fault_code}&lookback=${lookback}&post=1m` : null
  const rca = usePoll<RcaResult>(path, 0)

  return (
    <div className="rca-layout">
      <aside>
        <div className="card-h" style={{ marginBottom: 8 }}>
          <AlertTriangle size={15} /> Arızalar <span className="sub">· son 7 gün</span>
          <span className="right badge">{faults.data?.length ?? 0}</span>
        </div>
        <div className="fault-list">
          {faults.data?.length === 0 && <Empty>Bu hatta kayıtlı arıza yok. Simülatörde HR8'e bir sebep yazarak test edebilirsiniz.</Empty>}
          <AnimatePresence initial={false}>
            {faults.data?.map((f, i) => {
              const fd = meta?.faults.find(x => x.code === f.fault_code)
              const sc = meta?.scenarios.find(s => s.id === f.fault_scenario)
              const dur = f.cleared_at ? (new Date(f.cleared_at).getTime() - new Date(f.ts).getTime()) / 1000 : null
              return (
                <motion.button key={f.id} className="fault-item" aria-pressed={sel?.id === f.id} onClick={() => setSel(f)} layout
                  initial={{ opacity: 0, x: -10 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: Math.min(i, 10) * 0.03 }}>
                  <div className="row" style={{ justifyContent: 'space-between' }}>
                    <span className="code" style={{ color: 'var(--critical)' }}>#{f.fault_code}</span>
                    <span className="muted" style={{ fontSize: 11.5 }}>{ago(f.ts)}</span>
                  </div>
                  <div style={{ fontWeight: 600 }}>{fd?.label ?? 'Bilinmeyen'}</div>
                  <div className="row wrap muted" style={{ fontSize: 11.5, gap: 8 }}>
                    <span>{fmtDateTime(f.ts)}</span>
                    <span>{dur !== null ? `arıza süresi ${fmtDur(dur)}` : 'açık'}</span>
                  </div>
                  {sc && sc.id !== 0 && <span className="badge" style={{ justifySelf: 'start', fontSize: 11 }}><FlaskConical size={11} /> HR8: {sc.label}</span>}
                </motion.button>
              )
            })}
          </AnimatePresence>
        </div>
      </aside>

      <div className="grid">
        {!sel && !faults.data?.length && <Card><Empty><Search size={28} /><br />Analiz edilecek arıza seçin.</Empty></Card>}
        {sel && (
          <div className="toolbar" style={{ marginBottom: 0 }}>
            <span className="secondary">Geriye bakış penceresi</span>
            <Seg value={lookback} options={LOOKBACK} onChange={setLookback} />
            {rca.loading && <span className="muted">Analiz ediliyor…</span>}
          </div>
        )}
        {rca.error && <div className="note">Analiz başarısız: {rca.error}</div>}
        {rca.data && sel && <RcaView key={`${sel.id}-${lookback}`} r={rca.data} meta={meta} themeKey={themeKey} />}
      </div>
    </div>
  )
}

function RcaView({ r, meta, themeKey }: { r: RcaResult; meta: Meta | null; themeKey: string }) {
  const hyps = r.hypotheses ?? []
  const top = hyps[0]
  return (
    <>
      <Verdict r={r} top={top} />
      {(r.notes?.length ?? 0) > 0 && r.notes!.map((n, i) => <div key={i} className="note"><Info size={16} style={{ flex: 'none' }} />{n}</div>)}
      <Card title="Olay zinciri" sub="sapma başlangıçlarının zaman sırası — trip anına göre">
        <Chain r={r} />
      </Card>
      <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 460px), 1fr))' }}>
        <Card title="Hipotezler" sub="bilgi tabanı × gözlenen belirtiler">
          {hyps.length === 0 && <Empty>Bu arıza kodu için hipotez yok.</Empty>}
          {hyps.map((h, i) => <HypothesisRow key={h.cause.id} h={h} rank={i} open={i === 0} gt={r.groundTruth?.scenario.cause === h.cause.id} />)}
        </Card>
        <Card title="Balık kılçığı (Ishikawa)" sub="6M — kalınlık = olasılık">
          <Fishbone r={r} meta={meta} />
        </Card>
      </div>
      <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 460px), 1fr))' }}>
        <Card title="5 Neden" sub="otomatik çıkarım">
          <div className="why">
            {r.why.map((w, i) => (
              <motion.div key={i} className={`why-step ${i === r.why.length - 1 && top && top.fit >= 0.25 ? 'root' : ''}`}
                initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.15 + i * 0.12 }}>
                <span className="n">{i === r.why.length - 1 && top && top.fit >= 0.25 ? '!' : i + 1}</span><span>{w}</span>
              </motion.div>
            ))}
          </div>
        </Card>
        <Card title="Önerilen aksiyonlar" sub={top ? top.cause.label : ''}>
          {top ? (
            <ol style={{ margin: 0, paddingLeft: 20, display: 'grid', gap: 8 }}>
              {top.cause.actions.map((a, i) => (
                <motion.li key={i} initial={{ opacity: 0, x: 8 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: 0.2 + i * 0.1 }}>{a}</motion.li>
              ))}
            </ol>
          ) : <Empty>—</Empty>}
          {hyps[1] && hyps[1].fit > 0.25 && (
            <p className="muted" style={{ marginTop: 14, fontSize: 12.5 }}>
              Alternatif: <b className="secondary">{hyps[1].cause.label}</b> (%{Math.round(hyps[1].probability * 100)}) — {hyps[1].cause.actions[0]}
            </p>
          )}
        </Card>
      </div>
      <Card title="Sapma ısı haritası" sub="her sinyalin baseline'a göre z-skoru (σ) — kırmızı yukarı, mavi aşağı; dikey çizgi = trip">
        <Heatmap r={r} themeKey={themeKey} />
      </Card>
      <SignalCharts r={r} themeKey={themeKey} />
      <Card title="Sinyal tablosu" sub="baseline, son değer, sapma ve öncülük">
        <SignalTable r={r} />
      </Card>
    </>
  )
}

function Verdict({ r, top }: { r: RcaResult; top?: Hypothesis }) {
  const pct = top ? Math.round(top.probability * 100) : 0
  const fit = top ? Math.round(top.fit * 100) : 0
  const C = 2 * Math.PI * 48
  const gt = r.groundTruth
  const resCls = r.resolution === 'good' ? 'good' : r.resolution === 'coarse' ? 'warning' : 'serious'
  const resTxt = r.resolution === 'good' ? 'yüksek çözünürlük' : r.resolution === 'coarse' ? 'orta çözünürlük' : 'düşük çözünürlük'
  return (
    <Card className="verdict">
      <div className="ring-wrap">
        <svg viewBox="0 0 116 116" width="116" height="116">
          <circle cx="58" cy="58" r="48" stroke="var(--surface-3)" strokeWidth="10" fill="none" />
          <motion.circle cx="58" cy="58" r="48" stroke={fit >= 60 ? 'var(--series-1)' : fit >= 35 ? 'var(--warning)' : 'var(--text-muted)'} strokeWidth="10" fill="none"
            strokeLinecap="round" transform="rotate(-90 58 58)" strokeDasharray={C}
            initial={{ strokeDashoffset: C }} animate={{ strokeDashoffset: C * (1 - fit / 100) }} transition={{ duration: 1.1, ease: 'easeOut' }} />
        </svg>
        <div className="val"><div>{fit}%<small>uyum skoru</small></div></div>
      </div>
      <div>
        <div className="row wrap" style={{ gap: 8 }}>
          <span className="badge critical"><AlertTriangle size={13} /> #{r.fault.code} {r.fault.label}</span>
          <span className="badge">{fmtDateTime(r.tripTime)}</span>
          <span className={`badge ${resCls}`} title={`Okuma başına ≈ ${fmt(r.simPerSample, 1)} sn simülasyon zamanı`}>{resTxt} · {r.preSamples} okuma</span>
        </div>
        <div className="muted" style={{ marginTop: 10, fontSize: 12, textTransform: 'uppercase', letterSpacing: .5 }}>En olası kök sebep</div>
        <h2>{top ? top.cause.label : 'Belirlenemedi'}</h2>
        {top && <div className="secondary" style={{ maxWidth: 900 }}>{top.cause.detail}</div>}
        <div className="row wrap" style={{ gap: 8, marginTop: 10 }}>
          {top && <span className="badge">{top.cause.category}</span>}
          {top && <span className="badge">olasılık %{pct}</span>}
          {top?.firstMover && <span className="badge good"><Zap size={12} /> ana belirti ilk sapan sinyal</span>}
          {gt && (gt.match
            ? <motion.span className="badge good" initial={{ scale: .6 }} animate={{ scale: 1 }} transition={{ type: 'spring', delay: 1 }}><ShieldCheck size={13} /> Simülatör referansıyla doğrulandı (HR8: {gt.scenario.label})</motion.span>
            : <span className="badge serious"><ShieldX size={13} /> HR8 referansı: {gt.scenario.label} {gt.rank ? `(sıra ${gt.rank})` : '(bilgi tabanında yok)'}</span>)}
        </div>
      </div>
    </Card>
  )
}

const KIND: Record<ChainNode['kind'], { color: string; icon: any; label: string }> = {
  action: { color: 'var(--series-7)', icon: Hand, label: 'Operatör' },
  cause: { color: 'var(--series-5)', icon: FlaskConical, label: 'Enjeksiyon' },
  deviation: { color: 'var(--series-1)', icon: TrendingUp, label: 'Sapma' },
  alarm: { color: 'var(--warning)', icon: Bell, label: 'Uyarı' },
  trip: { color: 'var(--critical)', icon: Zap, label: 'Trip' },
  effect: { color: 'var(--text-muted)', icon: CircleDot, label: 'Sonuç' },
}

function Chain({ r }: { r: RcaResult }) {
  const trip = new Date(r.tripTime).getTime()
  const sig = Object.fromEntries(r.signals.map(s => [s.key, s]))
  if (!r.chain.length) return <Empty>Olay bulunamadı.</Empty>
  return (
    <div className="chain">
      {r.chain.map((n, i) => {
        const k = KIND[n.kind]
        const Icon = n.kind === 'deviation' && n.signal && sig[n.signal]?.dir === 'down' ? TrendingDown : k.icon
        const dt = (new Date(n.t).getTime() - trip) / 1000
        const dsim = n.sim - r.tripSim
        return (
          <motion.div key={i} className="chain-node" initial={{ opacity: 0, y: 14 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.12, type: 'spring', stiffness: 160, damping: 18 }}>
            {i < r.chain.length - 1 && <motion.div className="bar" style={{ left: 26 }} initial={{ scaleX: 0 }} animate={{ scaleX: 1 }} transition={{ delay: i * 0.12 + 0.1, duration: 0.35 }} />}
            <div className="pin" style={{ color: k.color }}><Icon size={15} /></div>
            <div className="when">{dt === 0 ? 'T' : `T${dt < 0 ? '−' : '+'}${fmtDur(Math.abs(dt))}`}{r.tripSim && dsim !== 0 ? ` · sim ${dsim < 0 ? '−' : '+'}${fmtDur(Math.abs(dsim))}` : ''}</div>
            <div className="what" style={{ color: n.kind === 'trip' ? 'var(--critical)' : undefined }}>{n.title}</div>
            <div className="det">{n.detail}</div>
            <div className="muted" style={{ fontSize: 11, marginTop: 2 }}>{k.label} · {fmtTime(n.t)}</div>
          </motion.div>
        )
      })}
    </div>
  )
}

function HypothesisRow({ h, rank, open: initOpen, gt }: { h: Hypothesis; rank: number; open: boolean; gt: boolean }) {
  const [open, setOpen] = useState(initOpen)
  const pct = Math.round(h.probability * 100)
  return (
    <div className="hyp">
      <button className="hyp-head" onClick={() => setOpen(o => !o)} aria-expanded={open}>
        <motion.span animate={{ rotate: open ? 0 : -90 }}><ChevronDown size={16} /></motion.span>
        <span>
          <b>{h.cause.label}</b>
          <span className="muted" style={{ fontSize: 12 }}> · {h.cause.category}{gt ? ' · ' : ''}</span>
          {gt && <span className="badge good" style={{ fontSize: 10.5, padding: '1px 6px' }}>HR8</span>}
        </span>
        <div className="meter" title={`uyum %${Math.round(h.fit * 100)}`}>
          <motion.div style={{ background: rank === 0 ? 'var(--series-1)' : 'var(--steel-3)' }} initial={{ width: 0 }} animate={{ width: `${h.fit * 100}%` }} transition={{ duration: 0.8, delay: rank * 0.08 }} />
        </div>
        <b className="tnum" style={{ textAlign: 'right' }}>%{pct}</b>
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }} style={{ overflow: 'hidden' }}>
            <div className="ev-list">
              {h.evidence.map((e, i) => (
                <div key={i} className="ev">
                  {e.status === 'match' ? <Check size={15} color="var(--good)" /> : e.status === 'contradict' ? <X size={15} color="var(--critical)" /> : <Minus size={15} color="var(--text-muted)" />}
                  <span>{e.note}{e.primary && <span className="badge" style={{ marginLeft: 6, fontSize: 10.5, padding: '0 6px' }}>ana belirti</span>}</span>
                  <span className="obs">{e.observed}</span>
                </div>
              ))}
              {h.firstMover && <div className="ev"><Zap size={15} color="var(--good)" /><span>Ana belirti zincirdeki ilk sapma (+1.5)</span><span className="obs" /></div>}
              <div className="muted" style={{ fontSize: 11.5 }}>Skor {fmt(h.score, 1)} / {fmt(h.max, 1)} · uyum %{Math.round(h.fit * 100)}</div>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

const CATS = ['Makine', 'Malzeme', 'Metot', 'İnsan', 'Ölçüm', 'Çevre']

function Fishbone({ r, meta }: { r: RcaResult; meta: Meta | null }) {
  const W = 640, H = 330, spineY = 165, headX = 520
  const prob = Object.fromEntries((r.hypotheses ?? []).map(h => [h.cause.id, h.probability]))
  const topId = r.hypotheses?.[0]?.cause.id
  const causes = (meta?.causes ?? []).filter(c => c.faults.includes(r.fault.code))
  const xs = [140, 290, 440]
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" role="img" aria-label="Ishikawa diyagramı">
      <motion.path d={`M20,${spineY} L${headX},${spineY}`} stroke="var(--text-secondary)" strokeWidth={3}
        pathLength={1} strokeDasharray="1 1" initial={{ strokeDashoffset: 1 }} animate={{ strokeDashoffset: 0 }} transition={{ duration: 0.6 }} />
      <motion.g initial={{ opacity: 0, x: 10 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: 0.5 }}>
        <path d={`M${headX},${spineY - 38} L${W - 8},${spineY - 30} L${W - 8},${spineY + 30} L${headX},${spineY + 38} Z`} fill="color-mix(in srgb, var(--critical) 16%, var(--surface-2))" stroke="var(--critical)" />
        <text x={(headX + W) / 2} y={spineY - 6} textAnchor="middle" fontSize="13" fontWeight="700" fill="var(--critical)">#{r.fault.code}</text>
        <text x={(headX + W) / 2} y={spineY + 12} textAnchor="middle" fontSize="10.5" fill="var(--text-primary)">{r.fault.label.length > 18 ? r.fault.label.slice(0, 17) + '…' : r.fault.label}</text>
      </motion.g>
      {CATS.map((cat, ci) => {
        const top = ci < 3
        const x = xs[ci % 3]
        const y0 = top ? 30 : H - 30
        const list = causes.filter(c => c.category === cat)
        const hot = list.some(c => c.id === topId)
        return (
          <g key={cat}>
            <motion.path d={`M${x - 60},${y0} L${x},${spineY}`} stroke={hot ? 'var(--critical)' : 'var(--axis)'} strokeWidth={hot ? 3 : 2}
              pathLength={1} strokeDasharray="1 1" initial={{ strokeDashoffset: 1 }} animate={{ strokeDashoffset: 0 }} transition={{ delay: 0.3 + ci * 0.08, duration: 0.5 }} />
            <text x={x - 64} y={top ? y0 - 10 : y0 + 20} textAnchor="middle" fontSize="12" fontWeight="700" fill={hot ? 'var(--critical)' : 'var(--text-secondary)'}>{cat}</text>
            {list.map((c, i) => {
              const k = (i + 1) / (list.length + 1)
              const bx = x - 60 + 60 * k, by = y0 + (spineY - y0) * k
              const p = prob[c.id] ?? 0
              const isTop = c.id === topId
              return (
                <motion.g key={c.id} initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: 0.8 + i * 0.1 }}>
                  <line x1={bx - 90} y1={by} x2={bx} y2={by} stroke={isTop ? 'var(--critical)' : 'var(--axis)'} strokeWidth={1 + p * 4} />
                  {isTop && <circle cx={bx} cy={by} r={9} fill="var(--critical)" opacity={0.25}><animate attributeName="r" values="5;12;5" dur="1.6s" repeatCount="indefinite" /></circle>}
                  <circle cx={bx} cy={by} r={4} fill={isTop ? 'var(--critical)' : p > 0.05 ? 'var(--series-1)' : 'var(--text-muted)'} />
                  <text x={bx - 92} y={by - 5} fontSize="10.5" fill={isTop ? 'var(--text-primary)' : 'var(--text-secondary)'} fontWeight={isTop ? 700 : 400}>
                    {c.label.length > 26 ? c.label.slice(0, 25) + '…' : c.label}
                  </text>
                  <text x={bx - 92} y={by + 13} fontSize="10" fill="var(--text-muted)">%{Math.round(p * 100)}</text>
                  <title>{c.label} — {c.detail}</title>
                </motion.g>
              )
            })}
          </g>
        )
      })}
    </svg>
  )
}

function Heatmap({ r, themeKey }: { r: RcaResult; themeKey: string }) {
  const lab = Object.fromEntries(r.signals.map(s => [s.key, s.label]))
  const option = useMemo(() => {
    const hm = r.heatmap
    if (!hm.times?.length) return null
    const xs = hm.times.map(t => fmtTime(t))
    const ys = hm.signals.map(k => lab[k] ?? k).reverse()
    const data: any[] = []
    hm.signals.forEach((_, si) => hm.z[si].forEach((z, ti) => data.push([ti, hm.signals.length - 1 - si, z])))
    const b = baseOption() as any
    return {
      ...b,
      grid: { left: 8, right: 12, top: 10, bottom: 52, containLabel: true },
      tooltip: { ...b.tooltip, trigger: 'item', formatter: (p: any) => `<b>${fmt(p.value[2], 1)} σ</b> ${ys[p.value[1]]}<br/><span style="opacity:.7">${new Date(hm.times[p.value[0]]).toLocaleTimeString('tr-TR')}</span>` },
      xAxis: { type: 'category', data: xs, axisLabel: { color: css('--text-muted'), fontSize: 10, hideOverlap: true }, axisLine: { lineStyle: { color: css('--axis') } }, axisTick: { show: false }, splitArea: { show: false } },
      yAxis: { type: 'category', data: ys, axisLabel: { color: css('--text-secondary'), fontSize: 11 }, axisLine: { show: false }, axisTick: { show: false } },
      visualMap: {
        min: -6, max: 6, calculable: true, orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 220, itemWidth: 10,
        text: ['+6σ', '−6σ'], textStyle: { color: css('--text-muted'), fontSize: 11 },
        inRange: { color: [css('--div-neg'), css('--div-mid'), css('--div-pos')] },
      },
      series: [{
        type: 'heatmap', data, itemStyle: { borderColor: css('--surface-1'), borderWidth: 1 }, progressive: 0,
        emphasis: { itemStyle: { borderColor: css('--text-primary'), borderWidth: 1 } },
        markLine: hm.tripCol >= 0 ? { silent: true, symbol: 'none', lineStyle: { color: css('--critical'), width: 2, type: 'solid' }, label: { formatter: 'TRIP', color: css('--critical'), position: 'end' }, data: [{ xAxis: hm.tripCol }] } : undefined,
      }],
    }
  }, [r, themeKey])
  if (!option) return <Empty>Isı haritası için veri yok.</Empty>
  return <Chart option={option} height={Math.max(300, r.heatmap.signals.length * 22 + 80)} themeKey={themeKey} notMerge />
}

function SignalCharts({ r, themeKey }: { r: RcaResult; themeKey: string }) {
  const keys = useMemo(() => {
    const dev = r.signals.filter(s => s.deviated).sort((a, b) => (a.onset ?? '').localeCompare(b.onset ?? '') || Math.abs(b.z) - Math.abs(a.z)).map(s => s.key)
    const set = [...new Set([...(r.fault.signal ? [r.fault.signal] : []), ...dev])]
    for (const s of [...r.signals].sort((a, b) => Math.abs(b.peakZ) - Math.abs(a.peakZ))) { if (set.length >= 6) break; if (!set.includes(s.key)) set.push(s.key) }
    return set.slice(0, 6)
  }, [r])
  const opts = useMemo(() => keys.map(k => {
    const s = r.signals.find(x => x.key === k)!
    const spKey = ({ extruder_rpm: 'extruder_rpm_sp', zone1_temp: 'zone1_temp_sp', zone2_temp: 'zone2_temp_sp', zone3_temp: 'zone3_temp_sp', die_temp: 'die_temp_sp', haul_off_speed: 'haul_off_speed_sp' } as Record<string, string>)[k]
    const t = r.seriesTimes
    const b = baseOption() as any
    const lines: any[] = [{ xAxis: r.tripTime, name: 'TRIP', lineStyle: { color: css('--critical'), width: 2, type: 'solid' }, label: { formatter: 'TRIP', color: css('--critical') } }]
    if (s.onset && s.onset !== r.tripTime) lines.push({ xAxis: s.onset, name: 'başlangıç', lineStyle: { color: css('--series-1'), type: 'dashed', width: 1.5 }, label: { formatter: 'sapma', color: css('--series-1') } })
    // Clip implausible sensor values so the band stays readable.
    const clip = (v: number | null) => (v === null ? null : s.outOfRange ? Math.min(v, Math.max(s.mean + 30 * s.sigma, s.mean * 1.3)) : v)
    const series: any[] = [{
      name: s.label, type: 'line', showSymbol: false, color: css('--series-1'), lineStyle: { width: 2 },
      data: t.map((x, i) => [x, clip(r.series[k]?.[i] ?? null)]),
      markArea: { silent: true, data: [
        [{ yAxis: s.mean - 3 * s.sigma, itemStyle: { color: css('--text-muted'), opacity: 0.12 } }, { yAxis: s.mean + 3 * s.sigma }],
        ...stateAreas(t, r.states).filter(a => a[0].itemStyle.opacity > 0.1),
        [{ xAxis: r.windowStart, itemStyle: { color: css('--series-1'), opacity: 0.05 } }, { xAxis: r.baselineEnd }],
      ] },
      markLine: { silent: true, symbol: 'none', data: lines },
    }]
    if (spKey) series.push({ name: 'Setpoint', type: 'line', step: 'end', showSymbol: false, color: css('--series-2'), lineStyle: { width: 1.5, type: 'dashed' }, data: t.map((x, i) => [x, r.series[spKey]?.[i] ?? null]) })
    return {
      key: k, s,
      option: {
        ...b,
        grid: { ...b.grid, top: spKey ? 28 : 14 },
        legend: spKey ? { top: 0, right: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 3, textStyle: { color: css('--text-secondary'), fontSize: 11 } } : undefined,
        tooltip: { ...b.tooltip, formatter: (ps: any[]) => `<div style="opacity:.7">${new Date(ps[0].value[0]).toLocaleTimeString('tr-TR')}</div>` + ps.map(q => tipRow(q.color, q.seriesName, q.value[1] == null ? '—' : `${fmt(q.value[1], 2)} ${s.unit}`)).join('') },
        series,
      },
    }
  }), [keys, r, themeKey])
  return (
    <div className="chart-grid">
      {opts.map(o => (
        <Card key={o.key} title={o.s.label} sub={`${o.s.unit} · baseline ${fmt(o.s.mean, 1)} ± 3σ (${fmt(3 * o.s.sigma, 2)})`}
          right={o.s.deviated ? <span className={`badge ${Math.abs(o.s.z) > 6 || o.s.outOfRange ? 'critical' : 'warning'}`}>{o.s.dir === 'up' ? <TrendingUp size={12} /> : <TrendingDown size={12} />}{o.s.inferred ? 'çıkarım' : `${fmt(o.s.z, 1)}σ`}</span> : <span className="badge">normal</span>}>
          <Chart option={o.option} group="rca" themeKey={themeKey} notMerge />
        </Card>
      ))}
    </div>
  )
}

function SignalTable({ r }: { r: RcaResult }) {
  const [sort, setSort] = useState<{ k: string; d: number }>({ k: 'onset', d: 1 })
  const rows = useMemo(() => {
    const v = (s: any) => sort.k === 'onset' ? (s.onset ? -s.leadS : 1e12) : sort.k === 'z' ? -Math.abs(s.z) : sort.k === 'corr' ? -Math.abs(s.corr ?? 0) : s.label
    return [...r.signals].sort((a, b) => { const x = v(a), y = v(b); return (x < y ? -1 : x > y ? 1 : 0) * sort.d })
  }, [r, sort])
  const th = (k: string, l: string) => <th onClick={() => setSort(s => ({ k, d: s.k === k ? -s.d : 1 }))}>{l}{sort.k === k ? (sort.d > 0 ? ' ↓' : ' ↑') : ''}</th>
  return (
    <div style={{ overflowX: 'auto' }}>
      <table className="data">
        <thead><tr>{th('label', 'Sinyal')}<th>Baseline</th><th>σ</th><th>Son (trip öncesi)</th>{th('z', 'Sapma (z)')}<th>Yön</th>{th('onset', 'Öncülük')}{th('corr', 'Korelasyon (trip sinyali)')}</tr></thead>
        <tbody>
          {rows.map(s => (
            <tr key={s.key} style={{ opacity: s.deviated ? 1 : 0.65 }}>
              <td><b>{s.label}</b> <span className="muted">{s.unit}</span></td>
              <td>{fmt(s.mean, 2)}</td>
              <td>{fmt(s.sigma, 2)}</td>
              <td>{fmt(s.last, 2)}{s.outOfRange && <span className="badge critical" style={{ marginLeft: 6 }}>aralık dışı</span>}</td>
              <td><span className="zbar" style={{ width: Math.min(80, Math.abs(s.z) * 8), background: s.z > 0 ? 'var(--div-pos)' : 'var(--div-neg)' }} />{s.inferred ? 'çıkarım' : fmt(s.z, 1)}</td>
              <td>{s.dir === 'up' ? <TrendingUp size={14} color="var(--div-pos)" /> : s.dir === 'down' ? <TrendingDown size={14} color="var(--div-neg)" /> : <Minus size={14} color="var(--text-muted)" />}</td>
              <td>{s.onset ? <>{fmtDur(s.leadS)} <span className="muted">(sim {fmtDur(s.leadSimS)})</span></> : '—'}</td>
              <td>{s.corr !== undefined && s.corr !== null ? fmt(s.corr, 2) : '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
