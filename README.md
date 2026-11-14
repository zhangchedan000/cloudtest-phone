# prism-phone · ARM 云手机

在 ARM 机器上用容器跑多台安卓（Redroid），每台有独立的机型档案和独立代理，用网页统一管理。以后作为 Prism 指纹浏览器的一个页面接入。

## 项目定位

本仓库用于在**自有或经过授权的私有测试环境**中研发 ARM 安卓云手机系统。开发重点是 Redroid 容器、安卓多实例、设备配置档案与硬件兼容性、独立网络、远程操作、后台管理、运行稳定性和自动化测试。技术架构、已实现功能与开发路线保持不变。

**系统信息处理的当前实现**：部分实验模块根据设备档案生成 `/proc`、`/sys` 的替代文件内容，并通过 bind mount 调整安卓容器中的读取视图；这不同于修改 Linux 内核。真正的内核及驱动修改属于后续硬件适配阶段。现有实验代码、历史命名与实际测试记录均保留。

后续工作按具体功能开展：优先推进容器管理、版本管理、系统兼容、独立网络、远程操作和后台管理。系统属性、内核接口与文件视图的测试应在自有或授权的隔离环境内进行，记录实施方式、兼容性、稳定性与回滚方案。涉及第三方应用身份识别或安全机制的具体实现需要单独评估；不要因此暂停其他正常开发，也不要将仅有代码的功能视为已通过实机验证。

**研发验收原则**：优先通过系统诊断工具、自编测试程序与容器日志验证各项功能；保留真实系统行为和既有技术历史，不以第三方平台误判设备类型作为验收指标。

> 接手前先读 [`CLAUDE.md`](CLAUDE.md)，再按顺序读 [`docs/进度记录.md`](docs/进度记录.md)、[`docs/踩坑记录.md`](docs/踩坑记录.md)、[`docs/架构.md`](docs/架构.md)、`git log`。

## 目录

| 目录 | 是什么 | 换硬件要不要改 |
|---|---|---|
| `agent/` | 宿主机管家程序（Go）：开关安卓、改参数、管代理、传画面，同时提供网页 | 不改 |
| `web/` | 管理页（React 19 + Ant Design 6，和 Prism 同一套） | 不改 |
| `profiles/` | 机型库（真实机型的参数档案） | 换显卡要换一批机型 |
| `host/` | 宿主机安装脚本、环境自检 | 不改 |
| `boards/` | **板级适配层**：每种机器一个目录（内核、显卡驱动、编译参数） | 加机器就加一个目录 |
| `docs/` | 方案、进度记录、踩坑记录 | — |

## 机器

| 机器 | 用途 |
|---|---|
| 甲骨文阿什本 ARM（`instance-20260727-1515`） | 开发测试机，可重启，**不能失联** |
| 甲骨文新加坡 ARM（`instance-20260730-1622`） | 跑现有业务（Prism 团队服务等），**不动** |
| RK3588 板子（待买） | 最终运行环境，显卡加速 |

## 部署（ARM64 Linux，root）

```bash
git clone https://github.com/zhangchedan000/prism-phone /opt/prism-phone/repo && cd /opt/prism-phone/repo
bash host/setup-host.sh                      # 第一次：Docker、binderfs 等
bash host/install-agent.sh --no-pull         # 编译并安装管家程序（systemd 服务 prism-phone-agent）
bash host/itest-api.sh                       # 实机测试，逐项打勾
```

- 更新：`cd /opt/prism-phone/repo && git pull && bash host/install-agent.sh --no-pull`（新程序起不来会自动换回旧的）
- 参数：`/etc/prism-phone/agent.env`；管理页默认只监听本机，在自己电脑上 `ssh -L 8099:127.0.0.1:8099 服务器` 后打开 http://127.0.0.1:8099
- 网络模块（每台手机走自己的代理）：`bash host/network.sh install` → 管家程序参数加 `-mihomo-api 127.0.0.1:9090` → `bash host/network.sh apply`（3 分钟内不 `confirm` 自动撤销）
