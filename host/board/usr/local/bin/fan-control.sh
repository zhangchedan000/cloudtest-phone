#!/bin/sh
# 风扇温控（魔云腾 Q1：内核无风扇驱动，风扇在 pwmchip1，用户态控；按温度分档调 duty）。
# 别的板子风扇多半内核自动温控，用不上这个。判断风扇在哪个 pwmchip：逐个 export + 开满，看哪个一开就转。
CHIP=/sys/class/pwm/pwmchip1; PWM=$CHIP/pwm0; PERIOD=40000
[ -d "$PWM" ] || echo 0 > $CHIP/export
sleep 1
echo 0 > $PWM/enable 2>/dev/null
echo $PERIOD > $PWM/period 2>/dev/null
echo 0 > $PWM/duty_cycle 2>/dev/null
echo 1 > $PWM/enable 2>/dev/null
while :; do
  t=$(cat /sys/class/thermal/thermal_zone0/temp 2>/dev/null); t=$((t/1000))
  if   [ "$t" -ge 63 ]; then d=40000
  elif [ "$t" -ge 55 ]; then d=26000
  elif [ "$t" -ge 48 ]; then d=16000
  elif [ "$t" -ge 42 ]; then d=8000
  else d=0; fi
  echo $d > $PWM/duty_cycle 2>/dev/null
  sleep 5
done
