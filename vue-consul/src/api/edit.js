import request from '@/utils/request-ops'

export function getCloud(vendor) {
  return request({
    url: '/api/edit/cloud',
    method: 'get',
    params: vendor ? { vendor } : undefined
  })
}
export function findGroup(vendor, account, region) {
  return request({
    url: '/api/edit/find',
    method: 'get',
    params: { vendor, account, region }
  })
}
export function PostEditJob(editJob) {
  return request({
    url: '/api/edit/commit',
    method: 'post',
    data: { editJob }
  })
}

export function postThirdPartyHosts(hosts) {
  return request({
    url: '/api/edit/commit',
    method: 'post',
    data: { vendor: 'third_party', hosts }
  })
}
