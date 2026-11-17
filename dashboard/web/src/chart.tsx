import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart, BarChart, HeatmapChart, CustomChart } from 'echarts/charts'
import {
  GridComponent, TooltipComponent, MarkLineComponent, MarkAreaComponent, DataZoomComponent,
  VisualMapComponent, LegendComponent, AxisPointerComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'

echarts.use([LineChart, BarChart, HeatmapChart, CustomChart, GridComponent, TooltipComponent, MarkLineComponent,
  MarkAreaComponent, DataZoomComponent, VisualMapComponent, LegendComponent, AxisPointerComponent, CanvasRenderer])

export { echarts }

/** Reads a CSS custom property (tokens live on :root). */
export const css = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim()

export const SERIES = (i: number) => css(`--series-${(i % 8) + 1}`)

/** Shared chrome so every chart reads as one system. */
export function baseOption(): EChartsCoreOption {
  const muted = css('--text-muted'), grid = css('--grid'), axis = css('--axis')
  return {
    animationDuration: 700,
    animationDurationUpdate: 400,
    animationEasing: 'cubicOut',
    textStyle: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif', color: css('--text-secondary') },
    grid: { left: 8, right: 14, top: 30, bottom: 8, containLabel: true },
    tooltip: {
      trigger: 'axis', confine: true,
      backgroundColor: css('--surface-1'), borderColor: css('--border-strong'), borderWidth: 1,
      textStyle: { color: css('--text-primary'), fontSize: 12 },
      axisPointer: { type: 'line', lineStyle: { color: muted, width: 1 } },
      extraCssText: 'border-radius:10px;box-shadow:0 8px 24px rgba(0,0,0,.25);',
    },
    xAxis: {
      type: 'time', axisLine: { lineStyle: { color: axis } }, axisTick: { show: false },
      axisLabel: { color: muted, fontSize: 11, hideOverlap: true }, splitLine: { show: false },
    },
    yAxis: {
      type: 'value', scale: true, axisLine: { show: false }, axisTick: { show: false },
      axisLabel: { color: muted, fontSize: 11 }, splitLine: { lineStyle: { color: grid } },
    },
  }
}

/** Tooltip row: value first (strong), series name after, keyed by a short line. */
export function tipRow(color: string, name: string, value: string) {
  const esc = (s: string) => s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]!))
  return `<div style="display:flex;align-items:center;gap:8px;line-height:1.7">
    <span style="width:12px;height:2px;background:${color};display:inline-block;border-radius:1px"></span>
    <b style="font-variant-numeric:tabular-nums">${esc(value)}</b><span style="opacity:.7">${esc(name)}</span></div>`
}

interface Props {
  option: EChartsCoreOption
  height?: number | string
  group?: string
  className?: string
  notMerge?: boolean
  onClick?: (p: any) => void
  themeKey?: string
}

export function Chart({ option, height = 230, group, className, notMerge = false, onClick, themeKey }: Props) {
  const el = useRef<HTMLDivElement>(null)
  const inst = useRef<echarts.ECharts | null>(null)

  useEffect(() => {
    if (!el.current) return
    const c = echarts.init(el.current, undefined, { renderer: 'canvas' })
    inst.current = c
    const ro = new ResizeObserver(() => c.resize())
    ro.observe(el.current)
    return () => { ro.disconnect(); c.dispose(); inst.current = null }
  }, [themeKey])

  useEffect(() => {
    const c = inst.current
    if (!c) return
    if (group) { c.group = group; echarts.connect(group) }
  }, [group, themeKey])

  useEffect(() => {
    inst.current?.setOption(option, { notMerge, lazyUpdate: true })
  }, [option, notMerge, themeKey])

  useEffect(() => {
    const c = inst.current
    if (!c || !onClick) return
    c.on('click', onClick)
    return () => { c.off('click', onClick) }
  }, [onClick, themeKey])

  return <div ref={el} className={className ?? 'chart'} style={{ height }} />
}
