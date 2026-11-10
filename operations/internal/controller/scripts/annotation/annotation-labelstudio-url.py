def validate_application(value):
    url = value['PUBLIC_URL']
    if not url:
        return
    parsed = urllib.parse.urlsplit(url)
    if len(url) > 2048 or any(c.isspace() or ord(c) < 32 for c in url) or any(c in url for c in '\\?#') or parsed.scheme not in ('http','https') or not parsed.hostname or parsed.username or parsed.password or parsed.port is not None and not 1 <= parsed.port <= 65535:
        fail('对外访问地址须为无账号、查询参数和片段的完整 HTTP 或 HTTPS 地址')
