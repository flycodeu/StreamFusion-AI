import importlib, json, os, sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
settings = json.loads((root / 'config' / 'ytlabel.json').read_text(encoding='utf8'))
os.chdir(Path(settings.get('dataDirectory', str(root / 'data'))))
sys.path.insert(0, str(root / 'runtime' / 'source'))
upstream = importlib.import_module('app')
upstream.app.config['MAX_CONTENT_LENGTH'] = settings['maxUploadMiB'] * 1024 * 1024
upstream.socketio.run(upstream.app, host=settings['host'], port=settings['port'], debug=False, use_reloader=False, allow_unsafe_werkzeug=True)
