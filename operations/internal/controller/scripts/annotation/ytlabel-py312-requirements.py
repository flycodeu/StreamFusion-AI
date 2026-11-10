import sys
from pathlib import Path

source, config = Path(sys.argv[1]), Path(sys.argv[2])
upstream = source.read_bytes()
lines = upstream.decode('utf-8').splitlines(keepends=True)
overrides = {
    'opencv-python==4.5.4.60': 'opencv-python==4.10.0.84',
    'psutil==5.9.1': 'psutil==5.9.8',
}
for old in overrides:
    if sum(line.strip() == old for line in lines) != 1:
        sys.exit('上游依赖定义已变化，无法应用 Python 3.12 兼容方案：' + old)
effective = ''.join(overrides.get(line.strip(), line.rstrip('\r\n')) + ('\n' if line.endswith(('\r', '\n')) else '') for line in lines)
(config / 'requirements.upstream.txt').write_bytes(upstream)
(config / 'requirements.effective.txt').write_text(effective, encoding='utf-8', newline='\n')
for old, new in overrides.items():
    print('Python 3.12 兼容调整：' + old + ' → ' + new)
print('上游依赖：' + str(config / 'requirements.upstream.txt'))
print('实际依赖：' + str(config / 'requirements.effective.txt'))
