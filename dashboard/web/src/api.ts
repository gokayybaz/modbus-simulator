import { useEffect, useRef, useState } from 'react'

export type Num = number | null

export interface SignalDef { key: string; label: string; unit: string; group: string; sp?: string }
export interface AlarmDef { bit: number; column: string; label: string; signal?: string }
export interface LimitDef { signal: string; warn: number; trip: number; dir: 'high' | 'low' | 'band'; relative?: boolean; warnLow?: number; tripLow?: number }
export interface FaultDef { code: number; name: string; label: string; severity: string; signal?: string }
export interface ScenarioDef { id: number; name: string; label: string; faultCode: number; cause: string }
export interface Evidence { signal: string; dir: string; weight: number; primary?: boolean; note: string }
export interface CauseDef { id: string; label: string; category: string; detail: string; faults: number[]; evidence: Evidence[]; actions: string[] }
export interface Meta {
  signals: SignalDef[]; setpoints: SignalDef[]; alarms: AlarmDef[]; limits: LimitDef[]; faults: FaultDef[]
  scenarios: ScenarioDef[]; causes: CauseDef[]; states: Record<string, string>; pollMs: number
}

export type Row = Record<string, any>
export interface UnitSummary extends Row { unit_id: number; ts: string; machine_state: number; online: boolean; ageS: number }
export interface Live { latest: Row | null; recent: Record<string, any[]>; events: EventRow[] }
export interface EventRow { id: number; ts: string; unit_id: number; event_type: string; from_state: string | null; to_state: string | null; fault_code: number | null; message: string }
export interface Trend { from: string; to: string; bucketS: number; series: Record<string, any[]> }
export interface Segment { state: number; start: string; end: string; durationS: number }
export interface Stats {
  from: string; to: string; segments: Segment[]; stateSeconds: Record<string, number>; totalSeconds: number
  faultCount: number; pareto: { code: number; label: string; count: number }[]; energyKWh: number
  availability?: number; mtbfS?: number; mttrS?: number; producedM?: number; producedKg?: number; specificEnergy?: number
}
export interface FaultRow { id: number; ts: string; unit_id: number; fault_code: number; cleared_at: string | null; fault_scenario: number | null }

export interface SignalResult {
  key: string; label: string; unit: string; group: string; mean: number; sigma: number; last: number; z: number; peakZ: number
  deviated: boolean; dir: 'up' | 'down' | 'flat'; onset?: string; onsetSim?: number; leadS: number; leadSimS: number
  corr?: number; outOfRange?: boolean; inferred?: boolean
}
export interface ChainNode { t: string; sim: number; kind: 'action' | 'cause' | 'deviation' | 'alarm' | 'trip' | 'effect'; title: string; detail: string; signal?: string }
export interface EvidenceResult extends Evidence { resolved: string; status: 'match' | 'contradict' | 'absent'; observed: string }
export interface Hypothesis { cause: CauseDef; score: number; max: number; fit: number; probability: number; evidence: EvidenceResult[]; firstMover: boolean }
export interface RcaResult {
  unitId: number; fault: FaultDef; tripTime: string; tripSim: number; windowStart: string; windowEnd: string; baselineEnd: string
  preSamples: number; simPerSample: number; resolution: 'good' | 'coarse' | 'poor'
  signals: SignalResult[]; chain: ChainNode[]; hypotheses: Hypothesis[] | null
  heatmap: { times: string[]; signals: string[]; z: number[][]; tripCol: number }
  groundTruth?: { scenario: ScenarioDef; injected?: string; match: boolean; rank: number }
  why: string[]; series: Record<string, Num[]>; seriesTimes: string[]; states: number[]; notes: string[] | null
}

export async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const r = await fetch(path, { signal })
  if (!r.ok) {
    let msg = `${r.status}`
    try { msg = (await r.json()).error ?? msg } catch { /* ignore */ }
    throw new Error(msg)
  }
  return r.json()
}

/**
 * Polls `path` every `ms` while the tab is visible. Keeps the previous data
 * while refetching (no flash). `path = null` pauses.
 */
export function usePoll<T>(path: string | null, ms: number) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [updated, setUpdated] = useState<number>(0)
  const pathRef = useRef(path)

  useEffect(() => {
    pathRef.current = path
    if (!path) return
    let alive = true
    let timer: number | undefined
    let ctrl: AbortController | undefined
    const tick = async () => {
      if (document.hidden) { timer = window.setTimeout(tick, ms); return }
      ctrl = new AbortController()
      setLoading(true)
      try {
        const d = await getJSON<T>(path, ctrl.signal)
        if (alive && pathRef.current === path) { setData(d); setError(null); setUpdated(Date.now()) }
      } catch (e: any) {
        if (alive && e?.name !== 'AbortError') setError(String(e?.message ?? e))
      } finally {
        if (alive) { setLoading(false); if (ms > 0) timer = window.setTimeout(tick, ms) }
      }
    }
    tick()
    return () => { alive = false; clearTimeout(timer); ctrl?.abort() }
  }, [path, ms])

  return { data, error, loading, updated }
}

export const STATE: Record<number, { label: string; color: string; cls: string }> = {
  0: { label: 'DURDU', color: 'var(--idle)', cls: '' },
  1: { label: 'ISINIYOR', color: 'var(--warning)', cls: 'warning' },
  2: { label: 'ÇALIŞIYOR', color: 'var(--good)', cls: 'good' },
  3: { label: 'ARIZA', color: 'var(--critical)', cls: 'critical' },
}

export const fmt = (v: any, d = 1) => (v === null || v === undefined || Number.isNaN(v) ? '—' : Number(v).toLocaleString('tr-TR', { minimumFractionDigits: d, maximumFractionDigits: d }))
export const fmtTime = (t: string | number | Date, sec = true) => new Date(t).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', ...(sec ? { second: '2-digit' } : {}) })
export const fmtDateTime = (t: string | number | Date) => new Date(t).toLocaleString('tr-TR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
export function fmtDur(s?: number | null) {
  if (s === null || s === undefined || !isFinite(s)) return '—'
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} sn`
  if (s < 3600) return `${Math.floor(s / 60)} dk ${Math.round(s % 60)} sn`
  if (s < 86400) return `${Math.floor(s / 3600)} sa ${Math.round((s % 3600) / 60)} dk`
  return `${Math.floor(s / 86400)} g ${Math.round((s % 86400) / 3600)} sa`
}
export function ago(t: string | number) {
  const s = (Date.now() - new Date(t).getTime()) / 1000
  if (s < 5) return 'şimdi'
  if (s < 60) return `${Math.round(s)} sn önce`
  if (s < 3600) return `${Math.round(s / 60)} dk önce`
  if (s < 86400) return `${Math.round(s / 3600)} sa önce`
  return `${Math.round(s / 86400)} g önce`
}
