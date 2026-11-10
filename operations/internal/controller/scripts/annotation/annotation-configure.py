owner()
before = actual()
value = requested(before)
legacy = None
if app == 'ytlabel' and '/uploads/samples' in fields(read(unit_path)).get('BindReadOnlyPaths','') and before['SOURCE_DIRECTORY'] != value['SOURCE_DIRECTORY']:
    legacy = dict(value, SOURCE_DIRECTORY=before['SOURCE_DIRECTORY'])
    import_source(legacy, True)
    if value['SOURCE_DIRECTORY']:
        old, _, _ = tree(Path(before['SOURCE_DIRECTORY']), True)
        new, _, _ = tree(Path(value['SOURCE_DIRECTORY']), True)
        for name in old.keys() & new.keys():
            if old[name][4] != new[name][4]:
                fail('新旧素材同名且内容不同，未覆盖：'+name)
        combined = dict(old, **new)
        needed = sum(info[3] for name,info in combined.items() if info[4] and not (Path(value['DATA_DIRECTORY'])/'uploads/samples'/name).exists())
        if shutil.disk_usage(value['DATA_DIRECTORY']).free < needed+67108864:
            fail('保留旧素材并导入新素材的空间不足')
import_source(value, True)
snapshot(before)
stop()
if app == 'ytlabel':
    if legacy:
        import_source(legacy)
    import_source(value)
apply(value)
save(meta, dict(softwareId=sid, dataDirectory=value['DATA_DIRECTORY']))
save(backup/'expected.json', value)
start()
if app != 'ytlabel':
    import_source(value)
print('配置已写入，正在核对服务', flush=True)
