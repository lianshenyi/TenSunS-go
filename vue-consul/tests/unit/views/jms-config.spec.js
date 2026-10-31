import JMS from '@/views/jms/index.vue'

function viewModel(overrides = {}) {
  const vm = {
    emptyJmsConfig: JMS.methods.emptyJmsConfig,
    jms_config: {
      auth_type: 'access_key',
      url: 'https://jumpserver.example.com',
      ver: 'V3',
      access_key_id: 'akid-example',
      access_key_secret: 'replace-only-secret',
      linuxport: '22',
      linuxuid: 'linux-user',
      winport: '3389',
      winuid: 'windows-user',
      custom_ecs_info: ''
    },
    shouldRequireSecret: false,
    rotateSecret: false
  }
  return Object.assign(vm, overrides)
}

describe('JMS Access Key configuration', () => {
  it('does not retain a legacy token or a returned Secret Key', () => {
    const vm = viewModel()
    const normalized = JMS.methods.normalizeJmsConfig.call(vm, {
      url: 'https://jumpserver.example.com',
      token: 'legacy-token-must-not-render',
      access_key_id: 'akid-example',
      access_key_secret: 'secret-must-not-render'
    })

    expect(normalized.auth_type).toBe('access_key')
    expect(normalized.access_key_id).toBe('akid-example')
    expect(normalized.access_key_secret).toBe('')
    expect(normalized.token).toBeUndefined()
  })

  it('omits Secret Key when updating an already-configured key', () => {
    const payload = JMS.methods.configPayload.call(viewModel())

    expect(payload.auth_type).toBe('access_key')
    expect(payload.access_key_id).toBe('akid-example')
    expect(payload.access_key_secret).toBeUndefined()
    expect(payload.token).toBeUndefined()
  })

  it('includes Secret Key only for a creation or explicit rotation', () => {
    const payload = JMS.methods.configPayload.call(viewModel({ shouldRequireSecret: true, rotateSecret: true }))

    expect(payload.access_key_secret).toBe('replace-only-secret')
  })
})
