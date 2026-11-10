export type Protocol = 'tcp' | 'udp'

export interface Endpoints {
  listenAddress: string
  listenPort: number
  targetAddress: string
  targetPort: number
  protocol: Protocol
  remark?: string
}

export interface Rule extends Endpoints {
  id: string
  enabled: boolean
  status: 'listening' | 'disabled' | 'failed'
  listenError?: string
  targetError?: string
}

export async function request<T>(path = '', method = 'GET', body?: object): Promise<T> {
  const headers = new Headers({ Accept: 'application/json' })
  if (method !== 'GET') headers.set('X-Port-Forwarding-Request', '1')
  if (body) headers.set('Content-Type', 'application/json')
  const response = await fetch(import.meta.env.BASE_URL + 'api/rules' + path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body ? JSON.stringify(body) : undefined,
  })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => null)
  if (!response.ok || data === null) {
    throw new Error(data?.message ?? (response.status === 403
      ? '需要飞牛管理员权限'
      : '无法访问转发服务，请检查应用是否正在运行'))
  }
  return data
}

export function formatEndpoint(host: string, port: number | string) {
  if (host === '*') return '全部 IPv4 / IPv6 · ' + port
  return (host.includes(':') ? '[' + host + ']' : host) + ':' + port
}

export function listenAddressScope(value: string): 'loopback' | 'all' | 'lan' | 'public' | undefined {
  let address = value.trim().toLowerCase()
  if (address === '*') return 'all'
  if (address === 'localhost' || address === 'localhost.') return 'loopback'

  if (address.includes(':')) {
    try {
      address = new URL('http://[' + address + ']').hostname.slice(1, -1)
    } catch {
      return
    }
    if (address === '::') return 'all'
    if (address === '::1') return 'loopback'
    const mapped = /^::ffff:([\da-f]{1,4}):([\da-f]{1,4})$/.exec(address)
    if (mapped) {
      const high = Number.parseInt(mapped[1], 16)
      const low = Number.parseInt(mapped[2], 16)
      address = [high >> 8, high & 255, low >> 8, low & 255].join('.')
    } else {
      const prefix = Number.parseInt(address.split(':')[0], 16)
      return (prefix & 0xfe00) === 0xfc00 || (prefix & 0xffc0) === 0xfe80 ? 'lan' : 'public'
    }
  }

  const octets = address.split('.')
  if (octets.length !== 4 || octets.some(part => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)) return
  const [first, second, third, fourth] = octets.map(Number)
  if (first === 0 && second === 0 && third === 0 && fourth === 0) return 'all'
  if (first === 127) return 'loopback'
  if (first === 10 || (first === 172 && second >= 16 && second <= 31) ||
      (first === 192 && second === 168) || (first === 169 && second === 254) ||
      (first === 100 && second >= 64 && second <= 127)) return 'lan'
  return 'public'
}
