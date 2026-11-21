# 板子部署配置（魔云腾 Q1 / RK3588，Alpine）

这是把板子跑成「云测机正式板」用到的**宿主机系统级配置**（OpenRC 服务、local.d 开机脚本、sysctl、风扇温控）。
脚本在 `host/*.sh`、管家二进制在 `tools/dist/`、安卓镜像走 `docker load`（见 `docs/上板流程.md` 顶部「换新板快速流程」）。
这里补的是那套**系统配置文件**，照着 Alpine 板子的真实文件布局放。

## 一键铺到新板子

```sh
# 在新板子上（root），仓库 clone 下来后：
cp -a host/board/etc/.  /etc/
cp -a host/board/usr/.  /usr/
chmod +x /etc/init.d/* /etc/local.d/*.start /usr/local/bin/*.sh
# 管家参数和令牌：从模板复制、改令牌、只给 root 读
cp /etc/conf.d/prism-phone-agent.example /etc/conf.d/prism-phone-agent && chmod 600 /etc/conf.d/prism-phone-agent
vi /etc/conf.d/prism-phone-agent            # 改 PP_TOKEN（至少 16 位）
# 开机自启
rc-update add prism-phone-fan default       # !!! 风扇温控:满载必开,否则过热降频/关机(踩坑 #111)
rc-update add docker default
rc-update add local default                 # 跑 local.d 里的 binder-nodes / pp-redir
rc-update add prism-phone-agent default
rc-update add prism-phone-mihomo default
rc-update add prism-phone-oled default      # 正面 OLED 小屏(SSD1306/i2c-3)
rc-update add desktop-commander-remote default   # 可选：板子自挂 DC
sysctl -p /etc/sysctl.d/99-pp-redir.conf
sysctl -p /etc/sysctl.d/98-pp-scale.conf
rc-service docker restart                   # 让 conf.d/docker 生效（去掉 2375 端口）
```

## 各文件干啥

| 文件 | 作用 |
|---|---|
| `etc/init.d/prism-phone-agent` | 管家主服务（supervise-daemon 守护，崩了自动拉起；启动前等时间同步、补 binder 设备和网络规则） |
| `etc/conf.d/prism-phone-agent.example` | 管家启动参数 `PP_ARGS` 和令牌 `PP_TOKEN` 的模板。复制成 `/etc/conf.d/prism-phone-agent`、**chmod 600、改令牌** |
| `etc/conf.d/docker` | Docker 只走本机 socket（原厂开着不加密的 `tcp://0.0.0.0:2375`，局域网谁都能拿 root） |
| `etc/logrotate.d/prism-phone` | 管家 / mihomo / 风扇 / 小屏 / DC 日志超过 2M 就轮转（根分区只有 3.5G） |
| `etc/sysctl.d/98-pp-scale.conf` | 多开余量：`pid_max`、inotify 上限（默认值到二十几台会碰顶） |
| `etc/init.d/prism-phone-mihomo` | 网络模块 mihomo（配置 `config.yaml` 由管家自动生成，含代理订阅，**不进仓库**） |
| `etc/init.d/desktop-commander-remote` | 板子自己挂 Desktop Commander（电脑关机也能远程连，踩坑 #96） |
| `etc/local.d/binder-nodes.start` | 建 `/dev/binder{,hw,vnd}` 指向静态 binderN + chmod 666（无 binderfs 的板子必做，Q1 minor=122/121/120） |
| `etc/local.d/pp-redir.start` | 网络 REDIRECT/NAT 规则（nftables 表 `prism_redir`，只动容器网段，踩坑 #102）；外来的 127.0.0.1 包丢掉，手机只许连宿主机 DNS/透明代理端口 |
| `etc/sysctl.d/99-pp-redir.conf` | 持久化 `route_localnet=1`（REDIRECT 要） |
| `etc/init.d/prism-phone-fan` | **风扇温控服务**(before docker,满载前先转风扇)。跑下面那个脚本。**漏装=过热关机**(踩坑 #111) |
| `etc/init.d/prism-phone-oled` | 正面 OLED 小屏(SSD1306/i2c-3,显示 IP/手机数/温度/内存;管理页有开关) |
| `usr/local/bin/fan-control.sh` | 风扇温控脚本（Q1 风扇在 pwmchip1，内核无驱动，用户态控；按温度分档调 duty，≥63°C 开满；读不到温度也开满） |

## 要自己填的两处密钥

1. **`PP_TOKEN`**（`/etc/conf.d/prism-phone-agent` 里）：自定一个访问令牌，网页登录用，至少 16 位。
2. **代理订阅**：在管理页「代理库 → 导入订阅」贴你的机场链接，管家会生成 mihomo 的 `config.yaml`。

## 另外需要但不在这里的

- **管家二进制**：`tools/dist/prism-phone-agent-arm64.gz`（wget GitHub raw → gunzip 到 `/opt/prism-phone/prism-phone-agent`）。
- **安卓镜像**：太大不进 git。从老板子 `docker save` 的 tarball `docker load`，或 push 到自己的 registry 后 `docker pull`。镜像名 `cnflysky/redroid-rk3588:lineage-20-wifi-tel-resign-apps`，构建链见 `host/build-{wifi,telephony,resign,apps}-image.sh`。
- **mihomo 二进制**、**desktop-commander**：`/usr/local/bin/` 下，装一次即可（见 `docs/上板流程.md`、踩坑 #96）。
