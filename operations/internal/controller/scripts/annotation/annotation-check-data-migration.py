owner()
before = actual()
source, target, size, count, free = migration(before)
table({'CURRENT_DATA_DIRECTORY': source, 'DATA_DIRECTORY': target, 'FILE_COUNT': count, 'DATA_BYTES': size, 'AVAILABLE_BYTES': free, 'CHECK': '可迁移，原目录保留'})
