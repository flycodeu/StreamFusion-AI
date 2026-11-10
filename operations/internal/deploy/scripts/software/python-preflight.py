import importlib
import json
import sys

profile = sys.argv[1] if len(sys.argv) > 1 else ""
result = {
    "version": list(sys.version_info[:3]),
    "missingModules": [],
    "missingLibraries": [],
    "sqliteVersion": [],
}
modules = ["venv", "ensurepip"]
if profile == "label-studio":
    modules.append("sqlite3")
if profile == "ytlabel":
    modules.append("ctypes")
for name in modules:
    try:
        module = importlib.import_module(name)
        if name == "sqlite3":
            result["sqliteVersion"] = list(module.sqlite_version_info)
    except (ImportError, OSError):
        result["missingModules"].append(name)
if profile == "ytlabel" and "ctypes" not in result["missingModules"]:
    from ctypes import CDLL

    for name in ["libGL.so.1", "libglib-2.0.so.0", "libgthread-2.0.so.0"]:
        try:
            CDLL(name)
        except OSError:
            result["missingLibraries"].append(name)
print(json.dumps(result, separators=(",", ":")))
