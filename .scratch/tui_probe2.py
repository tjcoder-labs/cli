#!/usr/bin/env python3
"""Per-stage screen dump of a /model flow using a virtual terminal emulator."""
import os, pty, select, time, signal, re, sys

BIN = os.path.expanduser("~/.local/bin/coder")
env = dict(os.environ)
env["TERM"] = "xterm-256color"

# Minimal VT: collect final screen state per stage via a dumb screen grid.
class Screen:
    W, H = 130, 42
    def __init__(self):
        self.grid = [[" "] * self.W for _ in range(self.H)]
        self.cx = self.cy = 0
    def feed(self, data: bytes):
        s = data.decode("utf-8", "replace")
        i = 0
        while i < len(s):
            ch = s[i]
            if ch == "\x1b":
                m = re.match(r"\x1b\[([0-9;]*)([A-Za-z])", s[i:])
                if m:
                    args, code = m.group(1), m.group(2)
                    nums = [int(x) if x else 0 for x in args.split(";")] if args else [0]
                    if code == "H" or code == "f":
                        self.cy = (nums[0] or 1) - 1
                        self.cx = (nums[1] if len(nums) > 1 else 1) - 1
                    elif code == "A": self.cy = max(0, self.cy - (nums[0] or 1))
                    elif code == "B": self.cy = min(self.H - 1, self.cy + (nums[0] or 1))
                    elif code == "C": self.cx = min(self.W - 1, self.cx + (nums[0] or 1))
                    elif code == "D": self.cx = max(0, self.cx - (nums[0] or 1))
                    elif code == "G": self.cx = (nums[0] or 1) - 1
                    elif code == "d": self.cy = (nums[0] or 1) - 1
                    elif code == "J":
                        if nums[0] == 0:
                            self._clear_from_cursor()
                    elif code == "K":
                        for x in range(self.cx, self.W): self.grid[self.cy][x] = " "
                    i += m.end(); continue
                m2 = re.match(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)", s[i:])
                if m2: i += m2.end(); continue
                m3 = re.match(r"\x1b[^\[](?:.{0,2})", s[i:], re.S)
                i += 2 if m3 else 1
                continue
            if ch == "\r": self.cx = 0
            elif ch == "\n": self.cy = min(self.H - 1, self.cy + 1)
            elif ch == "\b": self.cx = max(0, self.cx - 1)
            elif ord(ch) < 32 and ch not in "\t": pass
            elif ch == "\t": self.cx = min(self.W - 1, (self.cx + 8) & ~7)
            else:
                if 0 <= self.cy < self.H and 0 <= self.cx < self.W:
                    self.grid[self.cy][self.cx] = ch
                self.cx += 1
                if self.cx >= self.W: self.cx, self.cy = 0, min(self.H - 1, self.cy + 1)
            i += 1
    def _clear_from_cursor(self):
        for x in range(self.cx, self.W): self.grid[self.cy][x] = " "
        for y in range(self.cy + 1, self.H): self.grid[y] = [" "] * self.W
    def text(self):
        return "\n".join("".join(r).rstrip() for r in self.grid)

pid, fd = pty.fork()
if pid == 0:
    os.chdir(os.path.expanduser("~/tenants/cli"))
    os.execve(BIN, [BIN], env)

scr = Screen()
def pump(sec):
    end = time.time() + sec
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if fd in r:
            try: scr.feed(os.read(fd, 65536))
            except OSError: break

pump(5.0)
os.write(fd, b"/model"); pump(0.8)
os.write(fd, b"\r"); pump(2.5)
print("########## STAGE 1: modal opened ##########")
print(scr.text())
first = [l for l in scr.text().splitlines() if "→" not in l and (":cloud" in l or "gemma" in l)]
os.write(fd, b"\x1b[B"); pump(0.9)
os.write(fd, b"\x1b[B"); pump(0.9)
print("\n########## STAGE 2: after Down x2 ##########")
print(scr.text())
os.write(fd, b"\r"); pump(1.4)
print("\n########## STAGE 3: after Enter ##########")
print(scr.text())

os.kill(pid, signal.SIGTERM)
time.sleep(0.3)
try: os.waitpid(pid, os.WNOHANG)
except Exception: pass
