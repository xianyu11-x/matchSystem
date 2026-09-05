"""Research protocol: one UTF-8 JSON line per request, stdout reserved for RPC."""
import contextlib
import json
import pathlib
import sys

sys.stdin.reconfigure(encoding="utf-8")
sys.stdout.reconfigure(encoding="utf-8")
# Compile source directly: restart always reads new bytes, including same-size edits.
namespace = {"__name__": "provider", "__file__": sys.argv[1]}
with contextlib.redirect_stdout(sys.stderr):
    exec(compile(pathlib.Path(sys.argv[1]).read_bytes(), sys.argv[1], "exec"), namespace)
for line in sys.stdin:
    try:
        request = json.loads(line)
        if request["Version"] != 1:
            raise ValueError("unsupported protocol")
        with contextlib.redirect_stdout(sys.stderr):
            facts = namespace["provide"](request["Method"], request["Input"])
        response = {"Version": 1, "Facts": facts}
    except Exception as exc:
        response = {"Version": 1, "Error": str(exc)[:4096]}
    print(json.dumps(response, ensure_ascii=True, allow_nan=False), flush=True)
