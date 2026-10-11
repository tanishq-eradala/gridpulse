import { useCallback, useEffect, useState } from 'react'
import { api, base, type Alert, type Equipment, type Reading, type Summary } from './api'

const blank: Summary = { totalReadings: 0, activeAlerts: 0, equipment: 0 }

function time(value: string) {
  return new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit', second: '2-digit' }).format(new Date(value))
}

export default function App() {
  const [equipment, setEquipment] = useState<Equipment[]>([])
  const [selected, setSelected] = useState('lab-unit-01')
  const [readings, setReadings] = useState<Reading[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [summary, setSummary] = useState<Summary>(blank)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    try {
      const [nextEquipment, nextReadings, nextAlerts, nextSummary] = await Promise.all([
        api.equipment(), api.readings(selected), api.alerts(), api.summary(),
      ])
      setEquipment(nextEquipment)
      setReadings(nextReadings)
      setAlerts(nextAlerts)
      setSummary(nextSummary)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load telemetry')
    } finally {
      setLoading(false)
    }
  }, [selected])

  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => {
    const stream = new EventSource(`${base}/api/stream`)
    stream.onopen = () => setConnected(true)
    stream.onmessage = () => void refresh()
    stream.onerror = () => setConnected(false)
    const fallback = window.setInterval(() => void refresh(), 10000)
    return () => { stream.close(); window.clearInterval(fallback) }
  }, [refresh])

  const latest = readings[0]
  const activeAlerts = alerts.filter(alert => alert.active)
  const selectedName = equipment.find(item => item.id === selected)?.name ?? selected

  return <div className="shell">
    <aside className="sidebar">
      <div className="logo"><div className="logo-mark">⌁</div><div>GridPulse<small>Equipment intelligence</small></div></div>
      <nav><a className="current" href="#overview">Overview</a><a href="#signals">Signals</a><a href="#alerts">Alerts</a><a href="#history">History</a></nav>
      <p className="disclaimer">Synthetic telemetry only<br />Portfolio project · No company affiliation</p>
    </aside>
    <main id="overview" className="content">
      <header className="topbar"><div><p className="overline">LIVE OPERATIONS</p><h1>Equipment telemetry</h1><p className="sub">Monitor readings, trends, and persistent exceptions.</p></div>
        <div className={`connection ${connected ? 'online' : ''}`}><span />{connected ? 'Live stream' : 'Polling fallback'}</div></header>
      {error && <div className="error" role="alert">{error}</div>}
      {loading ? <p className="loading">Loading synthetic equipment data…</p> : <>
        <div className="toolbar"><label>Equipment <select value={selected} onChange={event => setSelected(event.target.value)}>{equipment.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><span>{summary.totalReadings.toLocaleString()} readings ingested</span></div>
        <section className="kpis" aria-label="Current equipment metrics">
          <Kpi label="Voltage" value={latest ? latest.voltage.toFixed(1) : '—'} unit="V" detail="Nominal 480 V" />
          <Kpi label="Current" value={latest ? latest.current.toFixed(1) : '—'} unit="A" detail="Most recent sample" />
          <Kpi label="Temperature" value={latest ? latest.temperature.toFixed(1) : '—'} unit="°C" detail="Alert after 3 readings > 85 °C" danger={!!latest && latest.temperature > 85} />
          <Kpi label="Efficiency" value={latest ? latest.efficiency.toFixed(1) : '—'} unit="%" detail="Alert after 3 readings < 92%" danger={!!latest && latest.efficiency < 92} />
        </section>
        <div className="grid" id="signals">
          <section className="card chart-card"><div className="card-head"><div><p className="overline">SIGNAL TREND</p><h2>Temperature</h2></div><span>Last {readings.length} readings</span></div><Chart readings={readings} metric="temperature" min={45} max={105} threshold={85} color="#4bc9bf" unit="°C" /></section>
          <section className="card health-card"><div className="card-head"><div><p className="overline">EQUIPMENT STATE</p><h2>{selectedName}</h2></div></div><div className={`health-ring ${activeAlerts.some(a => a.equipmentId === selected) ? 'degraded' : ''}`}><span>{activeAlerts.some(a => a.equipmentId === selected) ? 'Attention' : readings.length ? 'Normal' : 'Waiting'}</span></div><p>Alerts require three consecutive abnormal readings to reduce noise. A normal reading clears the active condition.</p></section>
        </div>
        <div className="grid lower">
          <section className="card" id="alerts"><div className="card-head"><div><p className="overline">EXCEPTIONS</p><h2>Alert timeline</h2></div><span className="badge">{summary.activeAlerts} active</span></div>{alerts.length === 0 ? <p className="empty">No alerts yet. Run the simulator to generate a hot-period exception.</p> : <div className="list">{alerts.slice(0, 8).map(alert => <div className="alert-row" key={alert.id}><span className={`alert-icon ${alert.severity}`}>!</span><div><strong>{alert.message}</strong><small>{alert.equipmentId} · {time(alert.firstSeen)}</small></div><em className={alert.active ? 'active' : ''}>{alert.active ? 'Active' : 'Resolved'}</em></div>)}</div>}</section>
          <section className="card" id="history"><div className="card-head"><div><p className="overline">LATEST SAMPLES</p><h2>Reading history</h2></div></div>{readings.length === 0 ? <p className="empty">No samples received. Start the simulator.</p> : <div className="list">{readings.slice(0, 8).map(reading => <div className="reading-row" key={reading.sampleId}><span>{time(reading.recordedAt)}</span><strong>{reading.temperature.toFixed(1)} °C</strong><span>{reading.voltage.toFixed(1)} V</span><span>{reading.efficiency.toFixed(1)}%</span></div>)}</div>}</section>
        </div>
      </>}
    </main>
  </div>
}

function Kpi({ label, value, unit, detail, danger = false }: { label: string; value: string; unit: string; detail: string; danger?: boolean }) {
  return <div className={`kpi ${danger ? 'danger' : ''}`}><span>{label}</span><div><strong>{value}</strong><b>{unit}</b></div><small>{detail}</small></div>
}

function Chart({ readings, metric, min, max, threshold, color, unit }: { readings: Reading[]; metric: 'temperature'; min: number; max: number; threshold: number; color: string; unit: string }) {
  const ordered = [...readings].reverse()
  if (!ordered.length) return <div className="chart-empty">Waiting for readings from the simulator</div>
  const width = 680, height = 250, top = 20, bottom = 215
  const x = (index: number) => 35 + index * (width - 60) / Math.max(ordered.length - 1, 1)
  const y = (value: number) => bottom - (value - min) * (bottom - top) / (max - min)
  const points = ordered.map((item, index) => `${x(index)},${y(item[metric])}`).join(' ')
  return <div className="chart-wrap"><svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${metric} trend in ${unit}`}>
    {[50, 65, 80, 95].map(value => <g key={value}><line x1="35" x2="655" y1={y(value)} y2={y(value)} stroke="#2a4248" strokeDasharray="4 5" /><text x="0" y={y(value) + 4} fill="#80979a" fontSize="11">{value}</text></g>)}
    <line x1="35" x2="655" y1={y(threshold)} y2={y(threshold)} stroke="#c98070" strokeDasharray="7 5" />
    <polyline points={points} fill="none" stroke={color} strokeWidth="3" strokeLinejoin="round" strokeLinecap="round" />
    {ordered.length === 1 && <circle cx={x(0)} cy={y(ordered[0][metric])} r="4" fill={color} />}
  </svg><div className="chart-footer"><span>Older</span><span>Threshold: {threshold} {unit}</span><span>Latest</span></div></div>
}
