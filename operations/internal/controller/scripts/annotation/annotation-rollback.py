owner()
before = load(backup/'before.json')
if mode == 'DOCKER':
    restore_container()
    restore_files()
else:
    stop()
    restore_files()
    start()
if actual() != before:
    fail('原配置未完全恢复，请核对操作备份')
health(before)
print('原配置、服务身份和原数据目录已恢复；源素材及已复制文件均保留', flush=True)
