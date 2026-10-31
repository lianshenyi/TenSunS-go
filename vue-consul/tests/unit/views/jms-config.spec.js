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
    hasExistingConfig: true,
    secretConfigured: true,
    originalAccessKeyId: 'akid-example',
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

  it('requires and submits a Secret Key when the Access Key ID changes', () => {
    const vm = viewModel({
      jms_config: Object.assign({}, viewModel().jms_config, {
        access_key_id: 'akid-replacement',
        access_key_secret: 'replacement-secret'
      })
    })

    const accessKeyIdChanged = JMS.computed.accessKeyIdChanged.call(vm)
    const shouldRequireSecret = JMS.computed.shouldRequireSecret.call(Object.assign(vm, { accessKeyIdChanged }))
    const payload = JMS.methods.configPayload.call(Object.assign(vm, { shouldRequireSecret }))

    expect(accessKeyIdChanged).toBe(true)
    expect(shouldRequireSecret).toBe(true)
    expect(payload.access_key_id).toBe('akid-replacement')
    expect(payload.access_key_secret).toBe('replacement-secret')
  })

  it('blocks an Access Key ID change without a Secret Key', () => {
    const error = jest.fn()
    const vm = viewModel({
      jms_config: Object.assign({}, viewModel().jms_config, {
        access_key_id: 'akid-replacement',
        access_key_secret: ''
      }),
      $message: { error }
    })
    const accessKeyIdChanged = JMS.computed.accessKeyIdChanged.call(vm)
    const shouldRequireSecret = JMS.computed.shouldRequireSecret.call(Object.assign(vm, { accessKeyIdChanged }))

    expect(JMS.methods.validateAccessKeyConfig.call(Object.assign(vm, { shouldRequireSecret }))).toBe(false)
    expect(error).toHaveBeenCalledWith('请填写 Secret Key；该值仅在创建、轮换或修改 Access Key ID 时需要')
  })
})
