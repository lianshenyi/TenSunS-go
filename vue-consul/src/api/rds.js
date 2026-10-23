import request from '@/utils/request-ops'

export function getResList(job_id) {
  return request({
    url: '/api/nodes/res',
    method: 'get',
    params: { job_id }
  })
}

export function getJobRds() {
  return request({
    url: '/api/rds/jobrds',
    method: 'get'
  })
}

export function postCstRds(cst_rds_dict) {
  return request({
    url: '/api/rds/cstrds',
    method: 'post',
    data: { cst_rds_dict }
  })
}

export function getCstRdsConfig(iid) {
  return request({
    url: '/api/rds/cstrdsconf',
    method: 'get',
    params: { iid }
  })
}

export function getCstRdsList(jobrds_name, checked) {
  return request({
    url: '/api/rds/cstrdslist',
    method: 'get',
    params: { jobrds_name, checked }
  })
}
