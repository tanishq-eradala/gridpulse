export type Equipment = { id: string; name: string; nominalVoltage: number }
export type Reading = {
  sampleId: string
  equipmentId: string
  recordedAt: string
  voltage: number
  current: number
  temperature: number
  efficiency: number
}
export type Alert = {
  id: number
  equipmentId: string
  kind: string
  severity: 'critical' | 'warning'
  message: string
  firstSeen: string
  lastSeen: string
  active: boolean
}
export type Summary = { totalReadings: number; activeAlerts: number; equipment: number }

export const base = import.meta.env.VITE_API_URL ?? 'http://localhost:8090'

async function get<T>(path: string): Promise<T> {
  const response = await fetch(`${base}${path}`)
  const body: unknown = await response.json()
  if (!response.ok) {
    const message = typeof body === 'object' && body !== null && 'error' in body
      ? String(body.error)
      : `Request failed (${response.status})`
    throw new Error(message)
  }
  return body as T
}

export const api = {
  equipment: () => get<Equipment[]>('/api/equipment'),
  readings: (id: string) => get<Reading[]>(`/api/readings?equipmentId=${encodeURIComponent(id)}&limit=60`),
  alerts: () => get<Alert[]>('/api/alerts?limit=30'),
  summary: () => get<Summary>('/api/summary'),
}
