import { normalizeIP, validateThirdPartyHosts } from '@/utils/third-party-hosts'

describe('third-party host validation', () => {
  it('accepts and normalizes IPv4 and IPv6 hosts across accounts', () => {
    const result = validateThirdPartyHosts([
      { name: 'default-v4', spec: '2C4G', ip: '10.0.0.1', port: '9100', account: '', project: '' },
      { name: 'partner-v6', spec: '4C8G', ip: '2001:db8::10', port: 9100, account: 'partner', project: 'core' }
    ])
    expect(result.valid).toBe(true)
    expect(result.hosts).toEqual([
      { name: 'default-v4', spec: '2C4G', ip: '10.0.0.1', port: 9100, account: '', project: '' },
      { name: 'partner-v6', spec: '4C8G', ip: '2001:db8::10', port: 9100, account: 'partner', project: 'core' }
    ])
  })

  it('rejects malformed IPs, invalid ports, duplicate names and endpoints', () => {
    expect(normalizeIP('300.0.0.1')).toBe('')
    expect(validateThirdPartyHosts([{ name: 'a', spec: '2C4G', ip: '10.0.0.1', port: 0 }]).valid).toBe(false)
    expect(validateThirdPartyHosts([
      { name: 'a', spec: '2C4G', ip: '10.0.0.1', port: 9100 },
      { name: 'a', spec: '4C8G', ip: '10.0.0.2', port: 9100 }
    ]).valid).toBe(false)
    expect(validateThirdPartyHosts([
      { name: 'a', spec: '2C4G', ip: '2001:db8::1', port: 9100 },
      { name: 'b', spec: '4C8G', ip: '2001:0db8:0:0:0:0:0:1', port: 9100 }
    ]).valid).toBe(false)
  })
})
