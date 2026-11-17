import type { Row } from './api'
import { fmt } from './api'

/** Colour for a zone temperature relative to its setpoint. */
function zoneColor(t: number, sp: number) {
  if (t > 500 || t < -20) return 'var(--series-7)' // implausible reading
  const d = t - sp
  if (d > 15) return 'var(--critical)'
  if (d < -10) return 'var(--series-1)'
  if (t < 60) return 'var(--steel-2)'
  return 'var(--series-2)'
}

/**
 * Animated process view of one extrusion line:
 * motor → barrel (3 heater zones, rotating screw) → die → vacuum tank →
 * cooling bath → haul-off → cut pipe. Animation speeds follow live values.
 */
export function Schematic({ r }: { r: Row }) {
  const running = r.machine_state === 2
  const rpm = Number(r.extruder_rpm) || 0
  const haul = Number(r.haul_off_speed) || 0
  const screwDur = rpm > 0.5 ? Math.max(0.15, 60 / rpm) : 0
  const pipeDur = haul > 0.01 ? Math.max(0.25, 2.4 / haul) : 0
  const flowDur = running && r.throughput > 1 ? Math.max(0.3, 300 / r.throughput) : 0
  const fault = r.machine_state === 3
  const anim = (dur: number) => (dur > 0 ? { animationDuration: `${dur}s` } : { animationPlayState: 'paused' as const })
  const zones = [
    { x: 160, t: r.zone1_temp, sp: r.zone1_temp_sp, n: 'Zon 1' },
    { x: 270, t: r.zone2_temp, sp: r.zone2_temp_sp, n: 'Zon 2' },
    { x: 380, t: r.zone3_temp, sp: r.zone3_temp_sp, n: 'Zon 3' },
  ]
  const cw = Number(r.cooling_water_temp) || 15
  const water = cw > 25 ? 'var(--serious)' : 'var(--water)'
  const vacLevel = Math.min(1, Math.max(0, (Number(r.vacuum) || 0) / 600))
  const pipeOn = r.pipe_outer_diameter > 0

  const label = (x: number, y: number, title: string, value: string, alarm = false) => (
    <g transform={`translate(${x},${y})`}>
      <text textAnchor="middle" fontSize="11" fill="var(--text-muted)" letterSpacing=".4">{title.toUpperCase()}</text>
      <text y={17} textAnchor="middle" fontSize="15" fontWeight="700" fill={alarm ? 'var(--critical)' : 'var(--text-primary)'} style={{ fontVariantNumeric: 'tabular-nums' }}>{value}</text>
    </g>
  )

  return (
    <div className="schematic">
      <svg viewBox="0 0 1210 290" role="img" aria-label="Ekstrüzyon hattı proses şeması">
        <defs>
          <linearGradient id="steel" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0" stopColor="var(--steel-3)" /><stop offset=".5" stopColor="var(--steel-2)" /><stop offset="1" stopColor="var(--steel-1)" />
          </linearGradient>
          <linearGradient id="pipeG" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0" stopColor="#4a4d55" /><stop offset=".45" stopColor="#2b2d33" /><stop offset="1" stopColor="#18191c" />
          </linearGradient>
          <linearGradient id="melt" x1="0" x2="1">
            <stop offset="0" stopColor="var(--series-4)" /><stop offset="1" stopColor="var(--series-2)" />
          </linearGradient>
          <pattern id="flights" width="28" height="30" patternUnits="userSpaceOnUse">
            <path d="M0,30 L14,0" stroke="var(--steel-3)" strokeWidth="5" />
          </pattern>
          <pattern id="stripes" width="40" height="30" patternUnits="userSpaceOnUse">
            <rect width="40" height="30" fill="url(#pipeG)" />
            <rect x="0" width="2" height="30" fill="rgba(255,255,255,.08)" />
          </pattern>
          <filter id="glow" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="6" /></filter>
          <clipPath id="barrelClip"><rect x="120" y="138" width="380" height="30" rx="4" /></clipPath>
          <clipPath id="pipeClip"><rect x="560" y="140" width="640" height="26" /></clipPath>
        </defs>

        {/* floor */}
        <line x1="10" x2="1190" y1="252" y2="252" stroke="var(--axis)" strokeWidth="1" />

        {/* motor */}
        <g>
          <rect x="22" y="118" width="80" height="70" rx="10" fill="url(#steel)" stroke="var(--border-strong)" />
          <g className={rpm > 0.5 ? 'spin' : ''} style={anim(Math.max(0.12, 30 / Math.max(rpm, 1)))}>
            {[0, 60, 120].map(a => <rect key={a} x="58" y="134" width="8" height="38" rx="3" fill="var(--steel-1)" transform={`rotate(${a} 62 153)`} />)}
          </g>
          <circle cx="62" cy="153" r="6" fill="var(--surface-1)" />
          <rect x="102" y="146" width="20" height="14" fill="var(--steel-2)" />
          <rect x="40" y="188" width="44" height="64" fill="var(--steel-1)" />
          {r.alarm_motor_overload && <rect x="18" y="114" width="88" height="78" rx="12" fill="var(--critical)" className="alarm-flash" />}
          {label(62, 70, 'Motor', `${fmt(r.motor_load, 0)} %`, r.alarm_motor_overload)}
          <text x="62" y="104" textAnchor="middle" fontSize="11" fill="var(--text-muted)">{fmt(r.motor_current, 0)} A</text>
        </g>

        {/* hopper */}
        <path d="M150,48 L210,48 L194,108 L166,108 Z" fill="url(#steel)" stroke="var(--border-strong)" />
        <rect x="170" y="108" width="20" height="30" fill="var(--steel-2)" />
        {running && [0, 1, 2].map(i => (
          <circle key={i} cx={180} cy={70} r={3} fill="var(--text-muted)" className="bubble" style={{ animationDelay: `${i * 0.8}s`, animationDirection: 'reverse' }} />
        ))}

        {/* barrel + heater bands */}
        <rect x="118" y="132" width="386" height="42" rx="8" fill="url(#steel)" stroke="var(--border-strong)" />
        <g clipPath="url(#barrelClip)">
          <rect x="120" y="138" width="380" height="30" fill="#1b1c20" />
          <rect x="120" y="140" width="420" height="26" fill="url(#flights)" className="screw" style={anim(screwDur)} />
          {flowDur > 0 && <rect x="320" y="138" width="180" height="30" fill="url(#melt)" opacity=".35" />}
        </g>
        {zones.map(z => {
          const c = zoneColor(z.t, z.sp)
          return (
            <g key={z.n}>
              <rect x={z.x - 40} y="126" width="80" height="54" rx="6" fill={c} opacity=".25" filter="url(#glow)" className={running || r.heating ? 'heat-glow' : ''} />
              {[...Array(4)].map((_, i) => <rect key={i} x={z.x - 34 + i * 18} y="128" width="12" height="50" rx="3" fill={c} opacity=".85" />)}
              {label(z.x, 210, z.n, `${fmt(z.t, 1)}°`, Math.abs(z.t - z.sp) > 15)}
              <text x={z.x} y={244} textAnchor="middle" fontSize="10.5" fill="var(--text-muted)">SP {fmt(z.sp, 0)}°</text>
            </g>
          )
        })}
        {label(300, 70, 'Vida', `${fmt(rpm, 1)} rpm`)}
        {label(420, 70, 'Eriyik', `${fmt(r.melt_temp, 1)}°`)}

        {/* die head */}
        <path d="M504,126 L548,134 L560,138 L560,168 L548,172 L504,180 Z" fill={zoneColor(r.die_temp, r.die_temp_sp)} opacity=".9" stroke="var(--border-strong)" />
        <circle cx="526" cy="112" r="11" fill="var(--surface-2)" stroke={r.alarm_melt_pressure_high ? 'var(--critical)' : 'var(--steel-3)'} strokeWidth="2" />
        <line x1="526" y1="112" x2={526 + 8 * Math.sin(Math.min(1, (r.melt_pressure || 0) / 400) * 4 - 2)} y2={112 - 8 * Math.cos(Math.min(1, (r.melt_pressure || 0) / 400) * 4 - 2)} stroke="var(--text-primary)" strokeWidth="2" />
        <line x1="526" y1="123" x2="526" y2="128" stroke="var(--steel-3)" strokeWidth="3" />
        {r.alarm_melt_pressure_high && <circle cx="526" cy="112" r="18" fill="var(--critical)" className="alarm-flash" />}
        {label(526, 54, 'Basınç', `${fmt(r.melt_pressure, 0)} bar`, r.alarm_melt_pressure_high)}
        {label(532, 210, 'Kalıp', `${fmt(r.die_temp, 1)}°`, Math.abs(r.die_temp - r.die_temp_sp) > 15)}

        {/* pipe (moves with haul-off speed) */}
        {pipeOn && (
          <g clipPath="url(#pipeClip)">
            <rect x="560" y="140" width="680" height="26" fill="url(#stripes)" className="pipe-move" style={anim(pipeDur)} />
            {flowDur > 0 && <line x1="560" x2="640" y1="153" y2="153" stroke="url(#melt)" strokeWidth="26" opacity=".55" />}
          </g>
        )}

        {/* vacuum tank */}
        <g>
          <rect x="620" y="112" width="160" height="84" rx="12" fill="var(--surface-2)" stroke="var(--steel-3)" strokeWidth="2" opacity=".95" />
          <rect x="624" y={116 + 76 * (1 - vacLevel)} width="152" height={76 * vacLevel} rx="9" fill="var(--water)" opacity=".18" />
          {running && [0, 1, 2, 3, 4].map(i => (
            <circle key={i} cx={640 + i * 30} cy={188} r={2.5 + (i % 2)} fill="var(--water)" className="bubble" style={{ animationDelay: `${i * 0.45}s` }} />
          ))}
          {pipeOn && <rect x="620" y="140" width="160" height="26" fill="url(#stripes)" opacity=".75" className="pipe-move" style={anim(pipeDur)} />}
          {r.alarm_vacuum_low && <rect x="616" y="108" width="168" height="92" rx="14" fill="var(--critical)" className="alarm-flash" />}
          <rect x="680" y="196" width="40" height="56" fill="var(--steel-1)" />
          {label(700, 70, 'Vakum', `${fmt(r.vacuum, 0)} mbar`, r.alarm_vacuum_low)}
        </g>

        {/* cooling bath */}
        <g>
          <rect x="800" y="118" width="170" height="74" rx="10" fill="var(--surface-2)" stroke="var(--steel-3)" strokeWidth="2" />
          <g clipPath="url(#coolClip)">
            <clipPath id="coolClip"><rect x="803" y="121" width="164" height="68" rx="8" /></clipPath>
            <path d="M790,132 q10,-6 20,0 t20,0 t20,0 t20,0 t20,0 t20,0 t20,0 t20,0 t20,0 t20,0 V200 H790 Z" fill={water} opacity=".35" className={running ? 'ripple' : ''} />
          </g>
          {pipeOn && <rect x="800" y="140" width="170" height="26" fill="url(#stripes)" opacity=".8" className="pipe-move" style={anim(pipeDur)} />}
          {r.alarm_cooling_water_high && <rect x="796" y="114" width="178" height="82" rx="12" fill="var(--critical)" className="alarm-flash" />}
          <rect x="860" y="192" width="50" height="60" fill="var(--steel-1)" />
          {label(885, 70, 'Soğutma suyu', `${fmt(cw, 1)}°`, r.alarm_cooling_water_high)}
        </g>

        {/* haul-off caterpillar */}
        <g>
          {[112, 170].map(y => (
            <g key={y}>
              <rect x="995" y={y} width="120" height="24" rx="12" fill="var(--steel-1)" />
              <rect x="997" y={y + 2} width="116" height="20" rx="10" fill="none" stroke="var(--steel-3)" strokeWidth="4" strokeDasharray="6 6" className="track" style={anim(pipeDur * 0.6)} />
            </g>
          ))}
          {r.alarm_haul_off_deviation && <rect x="990" y="106" width="130" height="94" rx="14" fill="var(--critical)" className="alarm-flash" />}
          <rect x="1030" y="196" width="50" height="56" fill="var(--steel-1)" />
          {label(1055, 70, 'Çekici', `${fmt(haul, 2)} m/dk`, r.alarm_haul_off_deviation)}
        </g>

        {/* output */}
        {label(1150, 210, 'Üretilen', `${fmt(r.total_produced_length, 0)} m`)}
        {label(1150, 70, 'Ø / et', `${fmt(r.pipe_outer_diameter, 1)} / ${fmt(r.pipe_wall_thickness, 2)}`)}

        {fault && <rect x="0" y="0" width="1200" height="290" fill="var(--critical)" opacity=".05" className="alarm-flash" />}
      </svg>
    </div>
  )
}
