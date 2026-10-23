from huaweicloudsdkcore.auth.credentials import GlobalCredentials,BasicCredentials
from huaweicloudsdkeps.v1.region.eps_region import EpsRegion
from huaweicloudsdkcore.exceptions import exceptions
from huaweicloudsdkbss.v2.region.bss_region import BssRegion
from huaweicloudsdkeps.v1 import *
from huaweicloudsdkbss.v2 import *
from huaweicloudsdkecs.v2 import *
from huaweicloudsdkecs.v2.region.ecs_region import EcsRegion
from huaweicloudsdkdcs.v2 import *
from huaweicloudsdkdcs.v2.region.dcs_region import DcsRegion
from huaweicloudsdkrds.v3 import *
from huaweicloudsdkrds.v3.region.rds_region import RdsRegion
import datetime,traceback
from units.cloud import sync_ecs
from units.cloud import sync_rds
from units.cloud import sync_redis
from units.config_log import *
from units import consul_kv

def group(account):
    ak,sk = consul_kv.get_aksk('huaweicloud',account)
    now = datetime.datetime.now().strftime('%m.%d/%H:%M')
    credentials = GlobalCredentials(ak, sk)
    try:
        client = EpsClient.new_builder() \
            .with_credentials(credentials) \
            .with_region(EpsRegion.value_of("cn-north-4")) \
            .build()
        request = ListEnterpriseProjectRequest()
        # request.status = 1
        request.offset = 0
        info = client.list_enterprise_project(request).to_dict()['enterprise_projects']
        group_dict = {i['id']:i['name'] for i in info}
        consul_kv.put_kv(f'ConsulManager/assets/huaweicloud/group/{account}',group_dict)
        count = len(group_dict)
        data = {'count':count,'update':now,'status':20000,'msg':f'同步企业项目成功！总数：{count}'}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/group', data)
        logger.info(f'【JOB】===>huaweicloud_group {account} {data}')
    except exceptions.ClientRequestException as e:
        logger.error(e.status_code)
        logger.error(e.request_id)
        logger.error(e.error_code)
        logger.error(f'{e.error_msg}\n{traceback.format_exc()}')
        data = consul_kv.get_value(f'ConsulManager/record/jobs/huaweicloud/{account}/group')
        if data == {}:
            data = {'count':'无','update':f'失败{e.status_code}','status':50000,'msg':e.error_msg}
        else:
            data['update'] = f'失败{e.status_code}'
            data['msg'] = e.error_msg
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/group', data)
    except Exception as e:
        logger.error(f'{e}\n{traceback.format_exc()}')
        data = {'count':'无','update':f'失败','status':50000,'msg':str(e)}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/group', data)

def ecs(account,region,isextip=False):
    ak,sk = consul_kv.get_aksk('huaweicloud',account)
    now = datetime.datetime.now().strftime('%m.%d/%H:%M')
    group_dict = consul_kv.get_value(f'ConsulManager/assets/huaweicloud/group/{account}')
    credentials = BasicCredentials(ak, sk)
    try:
        paycredentials = GlobalCredentials(ak, sk)
        payclient = BssClient.new_builder() \
            .with_credentials(paycredentials) \
            .with_region(BssRegion.value_of("cn-north-1")) \
            .build()
        payrequest = ListPayPerUseCustomerResourcesRequest()
        listQueryResourcesReqStatusListbody = [2]
        payrequest.body = QueryResourcesReq(
            limit=500,
            status_list=listQueryResourcesReqStatusListbody,
            only_main_resource=1
        )
        exp_list = payclient.list_pay_per_use_customer_resources(payrequest).to_dict()['data']
        #logger.info(exp_list)
        exp_dict = {}
        for i in exp_list:
            if i['service_type_code'].replace('hws.service.type.',''):
               #exp_dict[i['resource_id']] = datetime.datetime.strptime(i['expire_time'],'%Y-%m-%d')
               exp_dict[i['resource_id']] = i['expire_time'].split('T')[0]
        client = EcsClient.new_builder() \
            .with_credentials(credentials) \
            .with_region(EcsRegion.value_of(region)) \
            .build()
        request = ListServersDetailsRequest()
        request.limit = 1000
        info = client.list_servers_details(request).to_dict()['servers']
        ecs_dict = {i['id']:{'name':i['name'],
                             'ip':i['addresses'][i['metadata']['vpc_id']][0].addr,
                             'region':region,
                             'group':group_dict[i['enterprise_project_id']],
                             'status':i['status'],
                             'ostype':i['metadata']['os_type'].lower(),
                             'cpu':i['flavor']['vcpus'] + '核',
                             'mem':f"{str(round(int(i['flavor']['ram'])/1024,1)).rstrip('.0')}GB",
                             'exp': exp_dict.get(i['id'], '-')
                            } for i in info}

        if isextip:
            for i in info:
                try:
                    pubip_list = [x.addr for x in i['addresses'][i['metadata']['vpc_id']] if 'floating' in str(x)]
                    if pubip_list:
                        ecs_dict[i['id']]['ip'] = pubip_list[0]
                    else:
                        pass
                except:
                    pass
        count = len(ecs_dict)
        off,on = sync_ecs.w2consul('huaweicloud',account,region,ecs_dict)
        data = {'count':count,'update':now,'status':20000,'on':on,'off':off,'msg':f'ECS同步成功！总数：{count}，开机：{on}，关机：{off}'}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/ecs/{region}', data)
        logger.info(f'【JOB】===>huaweicloud_ecs {account} {region} {data}')
    except exceptions.ClientRequestException as e:
        logger.error(e.status_code)
        logger.error(e.request_id)
        logger.error(e.error_code)
        logger.error(f'{e.error_msg}\n{traceback.format_exc()}')
        data = consul_kv.get_value(f'ConsulManager/record/jobs/huaweicloud/{account}/ecs/{region}')
        if data == {}:
            data = {'count':'无','update':f'失败{e.status_code}','status':50000,'on':0,'off':0,'msg':e.error_msg}
        else:
            data['update'] = f'失败{e.status_code}'
            data['msg'] = e.error_msg
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/ecs/{region}', data)
    except Exception as e:
        logger.error(f'{e}\n{traceback.format_exc()}')
        data = {'count':'无','update':f'失败','status':50000,'msg':str(e)}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/ecs/{region}', data)


def rds(account,region):
    ak,sk = consul_kv.get_aksk('huaweicloud',account)
    now = datetime.datetime.now().strftime('%m.%d/%H:%M')
    group_dict = consul_kv.get_value(f'ConsulManager/assets/huaweicloud/group/{account}')
    credentials = BasicCredentials(ak, sk)
    try:
        client = RdsClient.new_builder() \
            .with_credentials(credentials) \
            .with_region(RdsRegion.value_of(region)) \
            .build()
        request = ListInstancesRequest()
        request.datastore_type = "MySQL"
        request.limit = 100
        info = client.list_instances(request).to_dict()['instances']

        rds_dict = {i['id']:{'name':i['name'],
                             'domain':i['private_dns_names'][0] if i['private_dns_names'] else i['private_ips'][0],
                             'ip':i['private_ips'][0],
                             'port':i['port'],
                             'region':region,
                             'group':group_dict[i['enterprise_project_id']],
                             'status':i['status'],
                             'itype':i['type'],
                             'ver':i['datastore']['version'],
                             'cpu':f"{i['cpu']}核",
                             'mem':f"{i['mem']}GB",
                             'disk':f"{i['volume']['size']}GB",
                             'exp': '-' if i['expiration_time'] == None else i['expiration_time'].split('T')[0],
                             'team': {'team':x.get('value') for x in i['tags'] if x.get('key') == 'team'}.get('team','无')
                            } for i in info if i['datastore']['type'] == 'MySQL'}
        count = len(rds_dict)
        off,on = sync_rds.w2consul('huaweicloud',account,region,rds_dict)
        data = {'count':count,'update':now,'status':20000,'on':on,'off':off,'msg':f'RDS同步成功！总数：{count}，开机：{on}，关机：{off}'}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/rds/{region}', data)
        logger.info(f'【JOB】===>huaweicloud_rds {account} {region} {data}')
    except exceptions.ClientRequestException as e:
        logger.error(e.status_code)
        logger.error(e.request_id)
        logger.error(e.error_code)
        logger.error(f'{e.error_msg}\n{traceback.format_exc()}')
        data = consul_kv.get_value(f'ConsulManager/record/jobs/huaweicloud/{account}/rds/{region}')
        if data == {}:
            data = {'count':'无','update':f'失败{e.status_code}','status':50000,'on':0,'off':0,'msg':e.error_msg}
        else:
            data['update'] = f'失败{e.status_code}'
            data['msg'] = e.error_msg
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/rds/{region}', data)
    except Exception as e:
        logger.error(f'{e}\n{traceback.format_exc()}')
        data = {'count':'无','update':f'失败','status':50000,'msg':str(e)}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/rds/{region}', data)

def redis(account,region):
    ak,sk = consul_kv.get_aksk('huaweicloud',account)
    now = datetime.datetime.now().strftime('%m.%d/%H:%M')
    group_dict = consul_kv.get_value(f'ConsulManager/assets/huaweicloud/group/{account}')
    credentials = BasicCredentials(ak, sk)
    try:
        client = DcsClient.new_builder() \
            .with_credentials(credentials) \
            .with_region(DcsRegion.value_of(region)) \
            .build()
        request = ListInstancesRequest()
        request.include_failure = "false"
        request.include_delete = "false"
        request.limit = 1000
        info = client.list_instances(request).to_dict()['instances']

        redis_dict = {i['instance_id']:{'name':i['name'],
                             'domain':i['domain_name'],
                             'ip':i['ip'],
                             'port':i['port'],
                             'region':region,
                             'group':group_dict[i['enterprise_project_id']],
                             'status':i['status'],
                             'itype':i['spec_code'],
                             'ver':i['engine_version'],
                             'mem':f"{i['max_memory']}MB",
                             'exp': '-'
                            } for i in info}
        count = len(redis_dict)
        off,on = sync_redis.w2consul('huaweicloud',account,region,redis_dict)
        data = {'count':count,'update':now,'status':20000,'on':on,'off':off,'msg':f'REDIS同步成功！总数：{count}，开机：{on}，关机：{off}'}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/redis/{region}', data)
        logger.info(f'【JOB】===>huaweicloud_redis {account} {region} {data}')
    except exceptions.ClientRequestException as e:
        logger.error(e.status_code)
        logger.error(e.request_id)
        logger.error(e.error_code)
        logger.error(f'{e.error_msg}\n{traceback.format_exc()}')
        data = consul_kv.get_value(f'ConsulManager/record/jobs/huaweicloud/{account}/redis/{region}')
        if data == {}:
            data = {'count':'无','update':f'失败{e.status_code}','status':50000,'on':0,'off':0,'msg':e.error_msg}
        else:
            data['update'] = f'失败{e.status_code}'
            data['msg'] = e.error_msg
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/redis/{region}', data)
    except Exception as e:
        logger.error(f'{e}\n{traceback.format_exc()}')
        data = {'count':'无','update':f'失败','status':50000,'msg':str(e)}
        consul_kv.put_kv(f'ConsulManager/record/jobs/huaweicloud/{account}/redis/{region}', data)

