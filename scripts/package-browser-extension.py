#!/usr/bin/env python3
"""Reproducibly package the public MV3 files; never reads a private key."""
import hashlib, json, pathlib, sys, zipfile
root = pathlib.Path(__file__).resolve().parent.parent
out = pathlib.Path(sys.argv[1]) if len(sys.argv)>1 else root / 'dist/browser-extension'
out.mkdir(parents=True, exist_ok=True)
m = json.loads((root/'extension/manifest.json').read_text())
filename = f'coder-browser-{m["version"]}.zip'
with zipfile.ZipFile(out/filename, 'w', zipfile.ZIP_DEFLATED) as z:
    for name in ['manifest.json', 'background.js', 'dispatch.js', 'README.md']:
        info = zipfile.ZipInfo(name, (2026,1,1,0,0,0)); info.compress_type=zipfile.ZIP_DEFLATED; info.external_attr=0o100644<<16
        z.writestr(info, (root/'extension'/name).read_bytes())
b = (out/filename).read_bytes()
meta = dict(version=m['version'],filename=filename,downloadUrl='https://api.tjcoder.com/coder/browser/releases/latest.zip',sha256=hashlib.sha256(b).hexdigest(),size=len(b),extensionId='fjiihpaeanbchmcedkeeofnadiakfcpk',releaseNotes='P0 local stealth browser bridge. Chrome 135+; Allow User Scripts required for evaluate.')
(out/'latest.zip').write_bytes(b)
(out/'version.json').write_text(json.dumps(meta,indent=2)+'\n')
(out/'SHA256SUMS').write_text(meta['sha256']+'  '+filename+'\n')
print(json.dumps(meta,indent=2))
