import Jobs from '@/views/node-exporter/jobs.vue'

function createForm(vendor) {
  const vm = {
    checkSpecialKey: Jobs.methods.checkSpecialKey,
    newCloudJob: Jobs.methods.newCloudJob,
    ecsJob: { vendor }
  }
  const state = Jobs.data.call(vm)
  vm.ecsJob = Object.assign(state.ecsJob, { vendor })
  return { vm, state }
}

function validationError(validator, value) {
  let error
  validator({}, value, result => { error = result })
  return error
}

describe('cloud account create form', () => {
  it('allows blank third-party credentials but keeps optional values character-safe', () => {
    const { state } = createForm('third_party')
    const validateCredential = state.createRules.ak[0].validator

    expect(validationError(validateCredential, '')).toBeUndefined()
    expect(validationError(validateCredential, 'partner key').message).toMatch('不能含有空格')
    expect(validationError(validateCredential, 'partner-key')).toBeUndefined()
  })

  it('requires credentials for a cloud vendor and preserves character validation', () => {
    const { state } = createForm('alicloud')
    const validateCredential = state.createRules.sk[0].validator

    expect(validationError(validateCredential, '').message).toBe('此为必填项')
    expect(validationError(validateCredential, 'secret/key').message).toMatch('不能含有空格')
  })

  it('switches third-party accounts to the manual, required region field', () => {
    const { vm, state } = createForm('third_party')

    expect(Jobs.computed.isThirdPartyCloudAccount.call(vm)).toBe(true)
    expect(state.createRules.region[0].required).toBe(true)
    Jobs.methods.handleCreateVendorChange.call(vm, 'third_party')
    expect(vm.ecsJob.region).toEqual([])
  })
})
