import { useEffect, useRef, useState, type ReactNode } from 'react'
import { motion } from 'framer-motion'
import { fmt } from './api'

/** Number that tweens to its new value. */
export function AnimatedNumber({ value, digits = 1 }: { value: number | null | undefined; digits?: number }) {
  const [shown, setShown] = useState(value ?? 0)
  const from = useRef(value ?? 0)
  useEffect(() => {
    if (value === null || value === undefined || Number.isNaN(value)) return
    const start = performance.now(), a = from.current, b = value
    let raf = 0
    const step = (t: number) => {
      const k = Math.min(1, (t - start) / 650)
      const e = 1 - Math.pow(1 - k, 3)
      setShown(a + (b - a) * e)
      if (k < 1) raf = requestAnimationFrame(step)
      else from.current = b
    }
    raf = requestAnimationFrame(step)
    return () => { cancelAnimationFrame(raf); from.current = b }
  }, [value])
  if (value === null || value === undefined) return <>—</>
  return <>{fmt(shown, digits)}</>
}

/** Minimal sparkline with an area wash and a live end dot. */
export function Sparkline({ data, color = 'var(--series-1)', height = 36, band }: {
  data: (number | null)[]; color?: string; height?: number; band?: [number, number]
}) {
  const w = 200, h = height
  const vals = data.filter((v): v is number => v !== null && v !== undefined)
  if (vals.length < 2) return <svg width="100%" height={h} />
  let lo = Math.min(...vals), hi = Math.max(...vals)
  if (band) { lo = Math.min(lo, band[0]); hi = Math.max(hi, band[1]) }
  if (hi - lo < 1e-6) { hi += 1; lo -= 1 }
  const pad = (hi - lo) * 0.12
  lo -= pad; hi += pad
  const x = (i: number) => (i / (data.length - 1)) * w
  const y = (v: number) => h - ((v - lo) / (hi - lo)) * h
  let d = ''
  data.forEach((v, i) => { if (v !== null && v !== undefined) d += `${d ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}` })
  const lastI = data.length - 1
  const lastV = data[lastI]
  const id = `g${Math.abs(hash(color))}`
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" width="100%" height={h} style={{ display: 'block', overflow: 'visible' }}>
      <defs>
        <linearGradient id={id} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor={color} stopOpacity=".28" />
          <stop offset="1" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      {band && <rect x={0} width={w} y={y(band[1])} height={Math.max(0, y(band[0]) - y(band[1]))} fill="var(--text-muted)" opacity=".08" />}
      <path d={`${d}L${w},${h}L0,${h}Z`} fill={`url(#${id})`} />
      <path d={d} fill="none" stroke={color} strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      {lastV !== null && lastV !== undefined && <circle cx={x(lastI)} cy={y(lastV)} r={3} fill={color} stroke="var(--surface-1)" strokeWidth={2} vectorEffect="non-scaling-stroke" />}
    </svg>
  )
}
function hash(s: string) { let h = 0; for (const c of s) h = (h * 31 + c.charCodeAt(0)) | 0; return h }

/**
 * Radial gauge with normal / warning / trip bands and a spring-animated needle.
 * dir = 'high' → bands at the top end; 'low' → at the bottom end.
 */
export function Gauge({ value, min, max, warn, trip, dir = 'high', label, unit, digits = 0 }: {
  value: number | null | undefined; min: number; max: number; warn: number; trip: number; dir?: 'high' | 'low'
  label: string; unit: string; digits?: number
}) {
  const sweep = 240, start = -120
  const r = 70, cx = 90, cy = 88
  const ang = (v: number) => start + ((Math.min(max, Math.max(min, v)) - min) / (max - min)) * sweep
  const arc = (a0: number, a1: number, rr = r) => {
    const p = (a: number) => [cx + rr * Math.sin((a * Math.PI) / 180), cy - rr * Math.cos((a * Math.PI) / 180)]
    const [x0, y0] = p(a0), [x1, y1] = p(a1)
    return `M${x0},${y0} A${rr},${rr} 0 ${a1 - a0 > 180 ? 1 : 0} 1 ${x1},${y1}`
  }
  const v = value ?? min
  const state = dir === 'high' ? (v >= trip ? 'critical' : v >= warn ? 'warning' : 'good') : (v <= trip ? 'critical' : v <= warn ? 'warning' : 'good')
  const col = `var(--${state})`
  const bands = dir === 'high'
    ? [[min, warn, 'var(--surface-3)'], [warn, trip, 'var(--warning)'], [trip, max, 'var(--critical)']]
    : [[min, trip, 'var(--critical)'], [trip, warn, 'var(--warning)'], [warn, max, 'var(--surface-3)']]
  return (
    <div style={{ textAlign: 'center' }}>
      <svg viewBox="0 0 180 150" width="100%" style={{ maxWidth: 220, display: 'block', margin: '0 auto' }} role="img" aria-label={`${label} ${fmt(value, digits)} ${unit}`}>
        <path d={arc(start, start + sweep)} stroke="var(--surface-3)" strokeWidth={12} fill="none" strokeLinecap="round" />
        {bands.map(([a, b, c], i) => (
          <path key={i} d={arc(ang(a as number), ang(b as number), r + 10)} stroke={c as string} strokeWidth={4} fill="none" opacity={c === 'var(--surface-3)' ? 0 : 0.9} />
        ))}
        <motion.path d={arc(start, Math.max(start + 0.01, ang(v)))} stroke={col} strokeWidth={12} fill="none" strokeLinecap="round"
          initial={false} animate={{ d: arc(start, Math.max(start + 0.01, ang(v))) }} transition={{ type: 'spring', stiffness: 60, damping: 14 }} />
        <motion.g initial={false} animate={{ rotate: ang(v) }} transition={{ type: 'spring', stiffness: 60, damping: 12 }} style={{ originX: `${cx}px`, originY: `${cy}px` }}>
          <line x1={cx} y1={cy + 10} x2={cx} y2={cy - r + 16} stroke="var(--text-primary)" strokeWidth={2.5} strokeLinecap="round" />
        </motion.g>
        <circle cx={cx} cy={cy} r={6} fill="var(--surface-1)" stroke="var(--text-primary)" strokeWidth={2} />
        <text x={cx} y={cy + 38} textAnchor="middle" fontSize="22" fontWeight="700" fill="var(--text-primary)" style={{ fontVariantNumeric: 'tabular-nums' }}>{fmt(value, digits)}</text>
        <text x={cx} y={cy + 54} textAnchor="middle" fontSize="11" fill="var(--text-muted)">{unit}</text>
      </svg>
      <div className="row" style={{ justifyContent: 'center', gap: 6, marginTop: -4 }}>
        <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--text-secondary)' }}>{label}</span>
        {state !== 'good' && <span className={`badge ${state}`}>{state === 'critical' ? 'TRIP' : 'UYARI'}</span>}
      </div>
    </div>
  )
}

export function Card({ title, sub, right, children, className, style }: { title?: ReactNode; sub?: ReactNode; right?: ReactNode; children: ReactNode; className?: string; style?: React.CSSProperties }) {
  return (
    <motion.section className={`card ${className ?? ''}`} style={style as any}
      initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.35, ease: 'easeOut' }}>
      {title && <h3 className="card-h">{title}{sub && <span className="sub">· {sub}</span>}{right && <span className="right">{right}</span>}</h3>}
      {children}
    </motion.section>
  )
}

export function Seg<T extends string | number>({ value, options, onChange }: { value: T; options: { v: T; l: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="seg" role="group">
      {options.map(o => <button key={String(o.v)} aria-pressed={o.v === value} onClick={() => onChange(o.v)}>{o.l}</button>)}
    </div>
  )
}

export function Empty({ children }: { children: ReactNode }) { return <div className="empty">{children}</div> }
