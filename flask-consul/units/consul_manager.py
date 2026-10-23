import requests
from config import consul_token,consul_url
from units.config_log import *

headers = {'X-Consul-Token': consul_token}

def get_consul_ver():
    url = f'{consul_url}/operator/autopilot/state'
    response = requests.get(url, headers=headers, timeout=2)
    if response.status_code == 404:
        url = f'{consul_url}/operator/autopilot/health'
        response = requests.get(url, headers=headers, timeout=2)
    response.encoding='utf-8'
    if response.status_code == 200:
        logger.info(f'【consul】请求成功, 认证成功: {consul_url} ')
        return True
    elif response.status_code == 403:
        logger.error(f'【consul】连接正常: {consul_url}【认证失败】请检查consul token!')
        return False
    elif response.status_code == 404:
        logger.error(f'【consul】连接异常 {response.status_code}: {consul_url}, 您的consul版本可能过低!')
        return False
    else:
        logger.error(f'【consul】连接失败 {response.status_code}: {consul_url}, 请检查consul状态以及网络是否正常!')
        return False

def get_instances(service_name):
    url = f'{consul_url}/health/service/{service_name}'
    response = requests.get(url, headers=headers)
    if response.status_code == 200:
        info = response.json()
        instances_list = []
        for i in info:
            instance_dict = {}
            instance_dict['ID'] = i['Service']['ID']
            instance_dict['name'] = i['Service']['Service']
            instance_dict['tags'] = '无' if i['Service']['Tags'] == [] else i['Service']['Tags']
            instance_dict['address'] = i['Service'].get('Address')
            instance_dict['port'] = i['Service'].get('Port')
            if i['Service']['Meta'] == {} or i['Service']['Meta'] is None:
                instance_dict['meta'] = '无'
            else:
                instance_dict['meta'] = [i['Service']['Meta']]
                instance_dict['meta_label'] = [{'prop': x, 'label': x} for x in i['Service']['Meta'].keys()]
            if len(i['Checks']) ==2:
                instance_dict['status'] = i['Checks'][1]['Status']
                instance_dict['output'] = i['Checks'][1]['Output']
            else:
                instance_dict['status'] = '无'
                instance_dict['output'] = '无'
            instances_list.append(instance_dict)
        return {'code': 20000,'instances':instances_list}
    else:
        logger.error(f"{response.status_code}：{response.text}")
        return {'code': 50000, 'data': f'{response.status_code}:{response.text}'}
