#!/usr/bin/env python3
# 传感器喂数据（原型，踩坑 #59）：假扮安卓模拟器的 qemud 传感器管道。
# 开一个伪终端（pty），从机端给手机里的模拟器传感器 HAL 当 /dev/goldfish_pipe_dprctd 用。
# 协议（device/generic/goldfish/sensors）：每条消息 = 4 位十六进制长度 + 内容。
#   HAL → 我们：先写 "pipe:qemud:sensors\0"（不带长度头），然后 "time:<ns>"、"list-sensors"、"set:<名字>:<0|1>"、"set-delay:<毫秒>"
#   我们 → HAL：list-sensors 回 掩码；之后定时发 "acceleration:x:y:z" 等。
import os, pty, tty, sys, time, random, math, select
master, slave = pty.openpty()
tty.setraw(slave); tty.setraw(master)
name = os.ttyname(slave)
os.chmod(name, 0o666)
print(name, flush=True)
open(sys.argv[1], "w").write(name) if len(sys.argv) > 1 else None
NAMES = ["acceleration", "gyroscope", "magnetic-field", "orientation", "temperature", "proximity", "light", "pressure"]
MASK = (1 << 0) | (1 << 1) | (1 << 2) | (1 << 5) | (1 << 6)   # 加速度、陀螺仪、磁场、距离、光线
enabled, delay = set(), 0.2
buf = b""
def send(s):
    b = s.encode()
    os.write(master, b"%04x" % len(b) + b)
def frames():
    global buf
    out = []
    while True:
        if buf.startswith(b"pipe:"):
            i = buf.find(b"\0")
            if i < 0: break
            out.append(buf[:i]); buf = buf[i+1:]; continue
        if len(buf) < 4: break
        try: n = int(buf[:4], 16)
        except ValueError: buf = buf[1:]; continue
        if len(buf) < 4 + n: break
        out.append(buf[4:4+n]); buf = buf[4+n:]
    return out
t0 = time.time(); last = 0
while True:
    r, _, _ = select.select([master], [], [], 0.02)
    if r:
        try: buf += os.read(master, 4096)
        except OSError: time.sleep(0.5); continue
        for m in frames():
            s = m.decode(errors="replace"); print("HAL>", s, flush=True)
            if s == "list-sensors": send(str(MASK))
            elif s.startswith("set:"):
                _, n, v = s.split(":"); (enabled.add if v == "1" else enabled.discard)(n)
            elif s.startswith("set-delay:"): delay = max(0.01, int(s.split(":")[1]) / 1000)
    now = time.time()
    if enabled and now - last >= delay:
        last = now; t = now - t0
        tilt = 0.15 * math.sin(t / 7)
        if "acceleration" in enabled:
            send("acceleration:%.4f:%.4f:%.4f" % (0.12 + tilt + random.gauss(0, .02), 0.35 + random.gauss(0, .02), 9.78 + random.gauss(0, .02)))
        if "gyroscope" in enabled:
            send("gyroscope:%.5f:%.5f:%.5f" % (random.gauss(0, .002), random.gauss(0, .002), random.gauss(0, .002)))
        if "magnetic-field" in enabled:
            send("magnetic-field:%.2f:%.2f:%.2f" % (21.3 + random.gauss(0, .3), -4.1 + random.gauss(0, .3), -38.6 + random.gauss(0, .3)))
        if "proximity" in enabled: send("proximity:5.0")
        if "light" in enabled: send("light:%.1f" % max(0, 180 + 20 * math.sin(t / 30) + random.gauss(0, 3)))
