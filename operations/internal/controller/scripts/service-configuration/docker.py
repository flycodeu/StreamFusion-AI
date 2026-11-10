import copy
import http.client
import json
import os
import re
import socket
import urllib.parse

_docker_prefix = None


def _docker_version(value):
    if not isinstance(value,str) or not re.fullmatch(r'1\.[1-9][0-9]{0,2}',value):
        raise RuntimeError('无法确认 Docker API 版本')
    return tuple(map(int,value.split('.')))


class _DockerConnection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(75)
        self.sock.connect('/var/run/docker.sock')


def _docker_api(method, path, body=None, expected=(200, 201, 204, 304)):
    global _docker_prefix
    if path != '/version':
        if _docker_prefix is None:
            info = _docker_api('GET','/version')
            if not isinstance(info,dict):
                raise RuntimeError('无法确认 Docker API 版本')
            version = min(_docker_version(info.get('ApiVersion')),(1,44))
            if version < max(_docker_version(info.get('MinAPIVersion')),(1,41)):
                raise RuntimeError('Docker API 版本不兼容，请升级接入组件')
            _docker_prefix = '/v%d.%d' % version
        path = _docker_prefix + path
    connection = _DockerConnection('localhost', timeout=75)
    try:
        payload = None if body is None else json.dumps(body).encode()
        connection.request(method, path, payload, {'Content-Type': 'application/json'})
        response = connection.getresponse()
        data = response.read(1048577)
        if len(data) > 1048576:
            raise RuntimeError('Docker 响应过大，请检查容器配置')
        if response.status not in expected:
            # Error bodies and inspect data can contain environment secrets.
            raise RuntimeError('Docker 操作失败（HTTP %s）' % response.status)
        return json.loads(data) if data else None
    finally:
        connection.close()


def _docker_id(value):
    if not re.fullmatch(r'[a-f0-9]{64}', value):
        raise RuntimeError('无法确认完整容器身份')
    return value


def docker_inspect(locator):
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,160}', locator):
        raise RuntimeError('容器标识无效')
    value = _docker_api('GET', '/containers/' + locator + '/json')
    labels = value.get('Config', {}).get('Labels') or {}
    sid = os.environ['OPS_SOFTWARE_ID']
    if labels.get('com.streamfusion.ops.software', labels.get('streamfusion.ops.software-id')) != sid:
        raise RuntimeError('容器归属不符')
    _docker_id(value['Id'])
    return value


def docker_plan(original, ports=None, memory_mib=None, config=None):
    """Build from the complete original create configuration; never mutate inspect."""
    settings = copy.deepcopy(original['Config'] if config is None else config)
    if settings.get('Labels') != original['Config'].get('Labels'):
        raise RuntimeError('配置修改不能改变容器归属标签')
    settings['Image'] = original['Image']
    host = copy.deepcopy(original['HostConfig'])
    if host.get('AutoRemove') or host.get('NetworkMode', '').startswith('container:'):
        raise RuntimeError('当前容器运行方式不支持安全重建')
    if original['State'].get('Paused') or original['State'].get('Restarting'):
        raise RuntimeError('请先确认容器运行状态')
    if ports is not None:
        host['PortBindings'] = copy.deepcopy(ports)
    if memory_mib is not None:
        host['Memory'] = int(memory_mib) * 1048576
        if host['Memory'] and host.get('MemoryReservation',0) > host['Memory']:
            raise RuntimeError('内存上限不能低于原容器的预留内存')
        if not host['Memory']:
            host['MemorySwap'] = 0
        # Keep an explicit swap limit valid when the memory ceiling increases.
        if 0 < host.get('MemorySwap', 0) < host['Memory']:
            host['MemorySwap'] = host['Memory']
    # Reuse anonymous volumes by their existing name.
    mounts = host.get('Mounts') or []
    host['Mounts'] = mounts
    destinations = {item.get('Target') for item in mounts}
    binds = host.setdefault('Binds', []) or []
    host['Binds'] = binds
    bound = {item.split(':')[1] for item in binds if ':' in item}
    for item in original.get('Mounts', []):
        if item['Type'] == 'volume' and item['Destination'] not in destinations | bound:
            mounts.append({'Type': 'volume', 'Source': item['Name'], 'Target': item['Destination'], 'ReadOnly': not item['RW'], 'VolumeOptions': {'NoCopy': True}})
        elif item['Type'] not in ('bind', 'volume', 'tmpfs'):
            raise RuntimeError('存在无法安全复用的容器挂载')
    endpoints = {}
    for name, endpoint in original.get('NetworkSettings', {}).get('Networks', {}).items():
        ipam = endpoint.get('IPAMConfig')
        if ipam and any(ipam.values()):
            raise RuntimeError('固定容器 IP 需先单独核对，当前配置保持不变')
        if endpoint.get('Links') or endpoint.get('MacAddress') and original.get('Config', {}).get('MacAddress'):
            raise RuntimeError('自定义网络连接需先单独核对，当前配置保持不变')
        aliases = [item for item in endpoint.get('Aliases') or [] if item not in (original['Id'], original['Id'][:12])]
        endpoints[name] = {'Aliases': aliases}
        if endpoint.get('DriverOpts'):
            endpoints[name]['DriverOpts'] = copy.deepcopy(endpoint['DriverOpts'])
    settings['HostConfig'] = host
    settings['NetworkingConfig'] = {'EndpointsConfig': endpoints}
    return settings


def docker_recreate(original, job, ports=None, memory_mib=None, config=None):
    if not re.fullmatch(r'[a-f0-9-]{36}', job):
        raise RuntimeError('操作标识无效')
    body = docker_plan(original, ports, memory_mib, config)
    current = docker_inspect(original['Id'])
    name = original['Name'].lstrip('/')
    if current['Name'].lstrip('/') != name:
        raise RuntimeError('原容器名称已变更，请重新读取配置')
    previous = name + '-previous-' + job[:8]
    _docker_api('POST', '/containers/' + original['Id'] + '/stop?t=60')
    _docker_api('POST', '/containers/' + original['Id'] + '/rename?name=' + urllib.parse.quote(previous))
    created = _docker_api('POST', '/containers/create?name=' + urllib.parse.quote(name), body)
    return _docker_id(created['Id'])


def docker_restore(original):
    old = docker_inspect(original['Id'])
    name = original['Name'].lstrip('/')
    try:
        candidate = docker_inspect(name)
    except RuntimeError:
        candidate = None
    if candidate and candidate['Id'] != old['Id']:
        if candidate['Image'] != original['Image'] or candidate['Config'].get('Labels') != original['Config'].get('Labels'):
            raise RuntimeError('回滚候选镜像不符')
        _docker_api('POST', '/containers/' + candidate['Id'] + '/stop?t=60')
        _docker_api('DELETE', '/containers/' + candidate['Id'] + '?v=false')
    if old['Name'] != original['Name']:
        _docker_api('POST', '/containers/' + old['Id'] + '/rename?name=' + urllib.parse.quote(name))
    if os.environ.get('OPS_PREVIOUS_STATE') == 'RUNNING':
        _docker_api('POST', '/containers/' + old['Id'] + '/start')
