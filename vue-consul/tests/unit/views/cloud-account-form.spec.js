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
  it('requires credentials for a cloud vendor and preserves character validation', () => {
    const { state } = createForm('alicloud')
    const validateCredential = state.createRules.sk[1].validator

    expect(state.createRules.sk[0]).toMatchObject({ required: true, message: '此为必填项' })
    expect(validationError(validateCredential, 'secret/key').message).toMatch('不能含有空格')
  })

  it('routes third-party creation to the full host-set editor', () => {
    const { vm } = createForm('third_party')
    vm.newFormVisible = true
    vm.editFormVisible = false
    vm.loadThirdPartyHosts = jest.fn()

    expect(Jobs.computed.isThirdPartyCloudAccount.call(vm)).toBe(true)
    Jobs.methods.openThirdPartyEditor.call(vm)
    expect(vm.newFormVisible).toBe(false)
    expect(vm.editFormVisible).toBe(true)
    expect(vm.editJob.vendor).toBe('third_party')
    expect(vm.loadThirdPartyHosts).toHaveBeenCalledTimes(1)
  })
})
