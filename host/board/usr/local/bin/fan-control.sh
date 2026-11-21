#!/bin/sh
# 风扇温控（魔云腾 Q1：内核无风扇驱动，风扇在 pwmchip1，用户态控；按温度分档调 duty）。
# 别的板子风扇多半内核自动温控，用不上这个。判断风扇在哪个 pwmchip：逐个 export + 开满，看哪个一开就转。
CHIP=/sys/class/pwm/pwmchip1; PWM=$CHIP/pwm0; PERIOD=40000
[ -d "$PWM" ] || echo 0 > $CHIP/export
sleep 1
echo 0 > $PWM/enable 2>/dev/null
# !!! 关键(踩坑 #111/#113) !!! 这板子 pwmchip1 默认极性是 inversed(反的):
# 极性反了的话 duty 越大风扇越慢、duty=0 反而全速 → "越热越慢"把盒子烤糊,凉时反而全速狂转。
# 必须扳成 normal。坑:rockchip PWM 在 period=0 时拒绝改极性,所以**必须先写 period 再写 polarity**;
# 开机那次 period 还是 0,旧写法(先 polarity 后 period)会被拒、极性一直保持 inversed。
# 这里先设 period,再扳极性,并校验+重试(开机偶发失败)。
echo $PERIOD > $PWM/period 2>/dev/null
i=0
while [ "$(cat $PWM/polarity 2>/dev/null)" != "normal" ] && [ "$i" -lt 10 ]; do
	echo 0 > $PWM/enable 2>/dev/null
	echo $PERIOD > $PWM/period 2>/dev/null
	echo normal > $PWM/polarity 2>/dev/null
	i=$((i + 1))
	sleep 1
done
echo 0 > $PWM/duty_cycle 2>/dev/null
echo 1 > $PWM/enable 2>/dev/null
while :; do
  t=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null)
  # 读不到温度（或读到乱值）就开满，宁可吵也不能停转
  case "$t" in ''|*[!0-9]*) echo $PERIOD > $PWM/duty_cycle 2>/dev/null; sleep 5; continue ;; esac
  t=$((t/1000))
  if   [ "$t" -ge 63 ]; then d=40000
  elif [ "$t" -ge 55 ]; then d=26000
  elif [ "$t" -ge 48 ]; then d=16000
  elif [ "$t" -ge 42 ]; then d=8000
  else d=0; fi
  echo $d > $PWM/duty_cycle 2>/dev/null
  sleep 5
done
