#!/usr/bin/env python3
"""Drive the TUI in a pty: /model, wait, Down, Enter, then screenshot the last frame."""
import os, pty, select, sys, time, signal, re

BIN = os.path.expanduser("~/.local/bin/coder")
env = dict(os.environ)
env["TERM"] = "xterm-256color"
env.setdefault("COLUMNS", "120")
env.setdefault("LINES", "40")

pid, fd = pty.fork()
if pid == 0:
    os.chdir(os.path.expanduser("~/tenants/cli"))
    os.execve(BIN, [BIN], env)

frames = []
def pump(sec):
    end = time.time() + sec
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if fd in r:
            try:
                data = os.read(fd, 65536)
            except OSError:
                break
            frames.append(data)

pump(5.0)              # splash/warmup
os.write(fd, b"/model")
pump(0.8)
os.write(fd, b"\r")      # Enter -> runs slash command
pump(2.5)
os.write(fd, b"\x1b[B")  # Arrow Down
pump(1.2)
os.write(fd, b"\x1b[B")  # Arrow Down again
pump(1.2)
os.write(fd, b"\r")      # Enter -> select highlighted model
pump(1.5)

os.kill(pid, signal.SIGTERM)
time.sleep(0.3)
try: os.waitpid(pid, os.WNOHANG)
except Exception: pass

def clean(b):
    s = b.decode("utf-8", "replace")
    s = re.sub(r"\x1b\[[0-9;?]*[A-Za-z~]", "", s)
    s = re.sub(r"\x1b\][^\x07]*\x07", "", s)
    return s

out = clean(b"".join(frames))
print("=== modal visible:", "SELECT MODEL" in out.upper())
sel = re.findall(r"Selected model: (\S+)", out)
print("=== selection events:", sel)
print("=== tail of screen ===")
lines = [l for l in out.splitlines() if l.strip()]
for l in lines[-30:]:
    print(l.rstrip()[:118])
