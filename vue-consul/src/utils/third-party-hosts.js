function normalizeIPv4(value) {
  const parts = value.split('.')
  if (parts.length !== 4 || parts.some(part => !/^\d+$/.test(part))) {
    return ''
  }
  const numbers = parts.map(part => Number(part))
  if (numbers.some(part => part > 255)) {
    return ''
  }
  return numbers.join('.')
}

export function normalizeIP(value) {
  const ip = String(value || '').trim()
  if (ip.indexOf(':') === -1) {
    return normalizeIPv4(ip)
  }
  try {
    return new URL('http://[' + ip + ']').hostname.slice(1, -1).toLowerCase()
  } catch (error) {
    return ''
  }
}

export function validateThirdPartyHosts(hosts) {
  const names = new Set()
  const endpoints = new Set()
  const normalizedHosts = []
  for (let index = 0; index < hosts.length; index++) {
    const host = hosts[index]
    const name = String(host.name || '').trim()
    const spec = String(host.spec || '').trim()
    const ip = normalizeIP(host.ip)
    const port = Number(host.port)
    const account = String(host.account || '').trim()
    const project = String(host.project || '').trim()
    const label = '第' + (index + 1) + '台机器'
    if (!name || !spec || !ip || !Number.isInteger(port)) {
      return { valid: false, message: label + '的名称、规格、IP和端口不能为空' }
    }
    if (port < 1 || port > 65535) {
      return { valid: false, message: label + '的端口必须在1-65535之间' }
    }
    if (names.has(name)) {
      return { valid: false, message: '机器名称重复：' + name }
    }
    const endpoint = (ip.indexOf(':') === -1 ? ip : '[' + ip + ']') + ':' + port
    if (endpoints.has(endpoint)) {
      return { valid: false, message: '机器IP和端口重复：' + endpoint }
    }
    names.add(name)
    endpoints.add(endpoint)
    normalizedHosts.push({ name, spec, ip, port, account, project })
  }
  return { valid: true, hosts: normalizedHosts }
}
