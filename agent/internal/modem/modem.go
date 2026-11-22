// Package modem 模拟手机的基带（modem）和 SIM 卡（踩坑 #65）。
//
// Redroid 镜像里本来就有安卓标准的电话驱动 rild + libreference-ril.so，它用 AT 命令（纯文本）跟基带说话，只是被
// ro.radio.noril=yes 关掉了。我们把它打开、让它连一个伪终端（/dev/ttyC0），这里扮演基带：SIM 卡已就绪、
// 注册到当地运营商（按代理出口国家选真实运营商和 MCC/MNC）、信号强度、IMEI / IMSI / ICCID。
// 移动数据不开（手机走 WiFi，管家开机时把 mobile_data 设成 0），不支持打电话、短信（回 ERROR / 不响应）。
//
// AT 协议：命令以 \r 结尾；回复是若干行（每行 \r\n 包住），最后一行 OK / ERROR / +CME ERROR: n。
// 一行里可以有多条用 ; 隔开的命令（reference-ril 查运营商名时会这样发）。
// SIM 文件（AT+CRSM）：安卓读 ICCID、运营商显示名（SPN）、MNC 长度（EF_AD）等，用 2G 格式回 GET RESPONSE / READ BINARY。
package modem

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec 一张 SIM 卡和它所在的网络。
type Spec struct {
	IMEI    string `json:"imei"`
	IMSI    string `json:"imsi"`
	ICCID   string `json:"iccid"`
	MCC     string `json:"mcc"`
	MNC     string `json:"mnc"`
	Long    string `json:"long"`  // 运营商全名
	Short   string `json:"short"` // 运营商简称
	SPN     string `json:"spn"`   // SIM 卡上的运营商显示名
	LAC     string `json:"lac"`   // 位置区（4 位十六进制）
	CellID  string `json:"cid"`   // 小区（8 位十六进制）
	Signal  int    `json:"rssi"`  // 0~31
	RSRP    int    `json:"rsrp"`  // LTE 参考信号功率（正数，-dBm）
	Version string `json:"ver"`   // 基带版本
	// Absent 没插 SIM 卡（代理国家不在运营商表里时）：基带在、有 IMEI，SIM 相关查询回「没插卡」，不注册网络
	Absent bool `json:"absent,omitempty"`
	// Dialect 对面电话 HAL 的口味（镜像标签 prism.radio）。空 = reference-ril / cuttlefish RIL（安卓 13~15）；
	// "ranchu" = 安卓 16 模拟器的原生 AIDL 电话 HAL（radio-service.ranchu，踩坑 #121）：行尾只认 \r、
	// 运营商名不带引号、要 +CTEC、注册状态和信号只靠主动上报（+CREG / +CSQ）、认不出的前缀直接崩溃。
	Dialect string `json:"dialect,omitempty"`
}

// DialectRanchu 安卓 16 模拟器电话 HAL。
const DialectRanchu = "ranchu"

// Modem 一个基带。
type Modem struct {
	spec   Spec
	mu     sync.Mutex
	cfun   int
	format int // +COPS? 回哪种名字：0 全名 / 1 简称 / 2 数字
	w      io.Writer
	wmu    sync.Mutex // 回复和定时上报不能交错写
	urc    []string   // 这条命令回完之后要主动上报的行（ranchu：开射频后报注册状态、信号）
}

// HeartbeatEvery ranchu 口味下定时主动上报信号的间隔（测试时改短）。
var HeartbeatEvery = 10 * time.Second

func (m *Modem) ranchu() bool { return m.spec.Dialect == DialectRanchu }

func New(spec Spec, w io.Writer) *Modem {
	return &Modem{spec: spec, cfun: 1, w: w}
}

// Serve 一直读命令、写回复（r 断了就返回）。
func (m *Modem) Serve(r io.Reader) error {
	if m.ranchu() {
		// 定时报信号（真手机也会定期报）。兼当保险：ranchu 某条请求失败会关掉连接、等读线程退出再重连，
		// 可读线程卡在伪终端的 read() 上永远等不到数据 → 两个线程互等、整个电话 HAL 停住（踩坑 #121）。
		// 这边写一行它认得的上报，read() 就返回了，它自己能恢复。
		done := make(chan struct{})
		defer close(done)
		every := HeartbeatEvery
		go func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					m.reply([]string{m.csq()})
				}
			}
		}()
	}
	br := bufio.NewReader(r)
	var line strings.Builder
	for {
		b, err := br.ReadByte()
		if err != nil {
			return err
		}
		if b != '\r' && b != '\n' {
			line.WriteByte(b)
			continue
		}
		cmd := strings.TrimSpace(line.String())
		line.Reset()
		if cmd == "" {
			continue
		}
		m.reply(m.Handle(cmd))
		m.mu.Lock()
		urc := m.urc
		m.urc = nil
		m.mu.Unlock()
		if len(urc) > 0 {
			m.reply(urc)
		}
	}
}

func (m *Modem) reply(lines []string) {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	var b strings.Builder
	for _, l := range lines {
		if m.ranchu() {
			// ranchu 只认 \r 断行；多行回复（+COPS 三连）中间夹 \n 会解析失败
			b.WriteString(l + "\r")
		} else {
			b.WriteString("\r\n" + l + "\r\n")
		}
	}
	_, _ = io.WriteString(m.w, b.String())
}

// registrationURC ranchu 的注册状态、信号只从主动上报里来（它不会去问 AT+CREG? / AT+CSQ）。
func (m *Modem) registrationURC() []string {
	s := m.spec
	if s.Absent {
		return []string{"+CREG: 0", "+CGREG: 0"}
	}
	reg := fmt.Sprintf("1,\"%s\",\"%s\",7", s.LAC, s.CellID)
	return []string{"+CREG: " + reg, "+CGREG: " + reg, m.csq()}
}

func (m *Modem) csq() string {
	s := m.spec
	// gsm rssi,ber, cdma(2), evdo(3), lte: ss,rsrp,rsrq,rssnr,cqi, timing advance（13 个数，ranchu 认 12/13/14/22 个）
	return fmt.Sprintf("+CSQ: %d,99,-1,-1,-1,-1,-1,%d,%d,10,150,2147483647,2147483647", s.Signal, s.Signal, s.RSRP)
}

// Handle 处理一行命令，返回要回的各行（最后一行是 OK / ERROR）。
// ranchu（安卓 16 电话 HAL）的每条请求只认「结果行 / +CME ERROR: n」，从来不认光秃秃的 ERROR：
// 回 ERROR 它就等 3 秒超时、关掉连接，下一行上报到来时读线程报错直接崩，HAL 每 10 秒重启一次、
// 运营商时有时无（踩坑 #125，开机时框架会发 AT+CCHO 开 SIM 逻辑通道）。所以 ranchu 口味下把 ERROR
// 换成 +CME ERROR: 4（不支持，它转成 REQUEST_NOT_SUPPORTED 正常返回给框架）。
func (m *Modem) Handle(line string) []string {
	out := m.handle(line)
	if m.ranchu() && len(out) > 0 && out[len(out)-1] == "ERROR" {
		out[len(out)-1] = "+CME ERROR: 4"
	}
	return out
}

func (m *Modem) handle(line string) []string {
	if !strings.HasPrefix(strings.ToUpper(line), "AT") {
		return []string{"ERROR"}
	}
	body := line[2:]
	if body == "" {
		return []string{"OK"}
	}
	var out []string
	for i, part := range strings.Split(body, ";") {
		if i > 0 && !strings.HasPrefix(part, "+") && !strings.HasPrefix(part, "%") {
			part = "+" + part
		}
		res, ok := m.one(part)
		if !ok {
			return append(out, res...)
		}
		out = append(out, res...)
	}
	return append(out, "OK")
}

// one 处理一条命令（不带 AT 前缀）。ok=false 时 res 里已经包含最后的错误行。
func (m *Modem) one(c string) (res []string, ok bool) {
	s := m.spec
	up := strings.ToUpper(c)
	if s.Absent {
		if res, ok, handled := m.absent(up); handled {
			return res, ok
		}
	}
	switch {
	case strings.HasPrefix(up, "E") || strings.HasPrefix(up, "S0") || strings.HasPrefix(up, "Q") || strings.HasPrefix(up, "V"):
		return nil, true
	case up == "+CPIN?":
		return []string{"+CPIN: READY"}, true
	case up == "+CFUN?":
		m.mu.Lock()
		defer m.mu.Unlock()
		return []string{fmt.Sprintf("+CFUN: %d", m.cfun)}, true
	case strings.HasPrefix(up, "+CFUN="):
		v, _ := strconv.Atoi(strings.TrimPrefix(up, "+CFUN="))
		m.mu.Lock()
		m.cfun = v
		if v == 1 && m.ranchu() {
			m.urc = append(m.urc, m.registrationURC()...)
		}
		m.mu.Unlock()
		return nil, true
	case up == "+CGSN":
		return []string{s.IMEI}, true
	case up == "+CGSN=2":
		// ranchu 读 IMEI 用的：一行裸数字 = 15 位 IMEI + 2 位软件版本号（不足 15 位它会越界崩溃）
		return []string{s.IMEI + "01"}, true
	case up == "+CIMI":
		return []string{s.IMSI}, true
	case up == "+CGMR":
		return []string{s.Version}, true
	case up == "+CSQ":
		return []string{m.csq()}, true
	case up == "+CREG?":
		return []string{fmt.Sprintf("+CREG: 2,1,\"%s\",\"%s\",7", s.LAC, s.CellID)}, true
	case up == "+CGREG?":
		return []string{fmt.Sprintf("+CGREG: 2,1,\"%s\",\"%s\",7", s.LAC, s.CellID)}, true
	case up == "+COPS?":
		m.mu.Lock()
		defer m.mu.Unlock()
		return []string{m.copsAnswer()}, true
	case up == "+COPS=3,0" || up == "+COPS=3,1" || up == "+COPS=3,2":
		m.mu.Lock()
		m.format = int(up[len(up)-1] - '0')
		m.mu.Unlock()
		return nil, true
	case up == "+CSMS=1":
		return []string{"+CSMS: 1,1,1"}, true
	case up == "+CEREG?":
		return []string{fmt.Sprintf("+CEREG: 2,1,\"%s\",\"%s\",7", s.LAC, s.CellID)}, true
	case up == "+CGACT?", up == "+CGDCONT?", up == "+CLCC":
		return nil, true
	// 下面这些是 cuttlefish RIL（libcuttlefish-ril-2.so）会查的。它用「多行」方式发的命令（CLCK、CPINR），
	// 只回 OK 不带内容行它会读空指针崩溃（踩坑 #65），所以必须回一行像样的答案。
	case strings.HasPrefix(up, "+CLCK=") && clckMode(up) == "2":
		return []string{"+CLCK: 0"}, true // SIM 锁、固定拨号都没开
	case strings.HasPrefix(up, "+CPINR"):
		pin := "SIM PIN"
		if strings.Contains(up, "PIN2") {
			pin = "SIM PIN2"
		}
		return []string{fmt.Sprintf("+CPINR: %s,3,3", pin)}, true
	case up == "+CICCID":
		return []string{s.ICCID}, true
	case up == "+CCSS?":
		return []string{"+CCSS: 0"}, true
	case up == "+CLIP?":
		return []string{"+CLIP: 1,1"}, true
	case up == "+CLIR?":
		return []string{"+CLIR: 0,4"}, true
	case up == "+CMUT?":
		return []string{"+CMUT: 0"}, true
	case up == "+CSCA?":
		return []string{"+CSCA: \"\",145"}, true
	case up == "+CSCB?":
		return []string{"+CSCB: 0,\"\",\"\""}, true
	// 安卓 16 模拟器电话 HAL（radio-service.ranchu）开机初始化发 AT+CUSD=1（只是打开 USSD 结果上报），
	// 初始化序列里任何一条不回 OK 它就判「Can't init the host channel」崩溃重启（踩坑 #121）。
	// 只有真的发 USSD（AT+CUSD=1,"*100#"）才回错。
	case up == "+CUSD=0", up == "+CUSD=1":
		return nil, true
	case up == "+CUSD?":
		return []string{"+CUSD: 1"}, true
	// ranchu 开机就会问 SIM 卡工具包（REPORT_STK_SERVICE_IS_RUNNING → AT+CUSATD?），只认「+CUSATD: <int>, <int>」；
	// 回 ERROR 它等 3 秒超时、重置连接、然后卡死（踩坑 #121）。CDMA 漫游偏好同理。
	case m.ranchu() && up == "+CUSATD?":
		return []string{"+CUSATD: 0, 0"}, true
	case m.ranchu() && strings.HasPrefix(up, "+CUSATD="):
		return nil, true
	case m.ranchu() && up == "+WRMP?":
		return []string{"+WRMP: 0"}, true
	case m.ranchu() && strings.HasPrefix(up, "+WRMP="):
		return nil, true
	case strings.HasPrefix(up, "+CUSD"), strings.HasPrefix(up, "+CSIM"), strings.HasPrefix(up, "+CUSATD"),
		strings.HasPrefix(up, "+CUSATE"), strings.HasPrefix(up, "+CUSATT"), strings.HasPrefix(up, "+CCFCU"):
		// USSD、SIM 卡工具包、呼叫转移：没有
		return []string{"ERROR"}, false
	case strings.HasPrefix(up, "+CRSM="):
		return m.simIO(strings.TrimPrefix(c, "+CRSM=")), true
	// ranchu 的网络制式：AT+CTEC? 回位掩码（1<<5 = LTE，回 5 会被当成 GSM），AT+CTEC=? 回支持的制式序号
	// （GSM=0 WCDMA=1 LTE=5），AT+CTEC=<掩码>,... 设偏好时回当前的。
	case m.ranchu() && (up == "+CTEC?" || strings.HasPrefix(up, "+CTEC=") && up != "+CTEC=?"):
		return []string{"+CTEC: 32"}, true
	case m.ranchu() && up == "+CTEC=?":
		return []string{"+CTEC: 0,1,5"}, true
	case strings.HasPrefix(up, "+CTEC"), strings.HasPrefix(up, "+WRMP"), strings.HasPrefix(up, "+WPRL"), strings.HasPrefix(up, "+WNAM"),
		strings.HasPrefix(up, "+CCHO"), strings.HasPrefix(up, "+CCHC"), strings.HasPrefix(up, "+CGLA"),
		strings.HasPrefix(up, "D"), up == "A", strings.HasPrefix(up, "+CMGS"), strings.HasPrefix(up, "+CHLD"):
		// CDMA、逻辑通道、打电话、发短信：没有
		return []string{"ERROR"}, false
	}
	return nil, true // 其他设置类命令一律 OK
}

// copsAnswer 按 AT+COPS=3,<格式> 设的格式回 +COPS?（reference-ril 一次查全名、简称、数字三种）。
func (m *Modem) copsAnswer() string {
	s := m.spec
	if m.ranchu() {
		// ranchu 把第三段原样读到行尾：不能带引号、不能带 ,7（否则名字变成「"中国移动",7」、MCC/MNC 解析错）
		switch m.format {
		case 1:
			return "+COPS: 0,1," + s.Short
		case 2:
			return "+COPS: 0,2," + s.MCC + s.MNC
		}
		return "+COPS: 0,0," + s.Long
	}
	switch m.format {
	case 1:
		return fmt.Sprintf("+COPS: 0,1,\"%s\",7", s.Short)
	case 2:
		return fmt.Sprintf("+COPS: 0,2,\"%s%s\",7", s.MCC, s.MNC)
	}
	return fmt.Sprintf("+COPS: 0,0,\"%s\",7", s.Long)
}

// clckMode AT+CLCK="设施",模式,... 里的模式（2 = 查询）。
func clckMode(up string) string {
	f := strings.Split(strings.TrimPrefix(up, "+CLCK="), ",")
	if len(f) < 2 {
		return ""
	}
	return strings.TrimSpace(f[1])
}

// absent 没插 SIM 卡时和 SIM / 注册有关的命令（handled=false 的照常处理：IMEI、信号、开关射频等）。
// 27.007：没卡时 +CPIN? 回 +CME ERROR: 10（SIM not inserted）；注册状态 0（没注册、也不在搜）。
func (m *Modem) absent(up string) (res []string, ok, handled bool) {
	notInserted := []string{"+CME ERROR: 10"}
	switch {
	case up == "+CPIN?", up == "+CIMI", up == "+CICCID", strings.HasPrefix(up, "+CRSM="), strings.HasPrefix(up, "+CLCK="),
		strings.HasPrefix(up, "+CPINR"), strings.HasPrefix(up, "+CSIM"), strings.HasPrefix(up, "+CPIN="):
		return notInserted, false, true
	case up == "+CREG?":
		return []string{"+CREG: 2,0"}, true, true
	case up == "+CGREG?":
		return []string{"+CGREG: 2,0"}, true, true
	case up == "+CEREG?":
		return []string{"+CEREG: 2,0"}, true, true
	case up == "+COPS?":
		if m.ranchu() {
			// ranchu 查运营商时「+COPS: 0」这类没有当前运营商的回复会直接崩溃；回「没有网络服务」它按错误处理
			return []string{"+CME ERROR: 30"}, false, true
		}
		return []string{"+COPS: 0"}, true, true
	}
	return nil, false, false
}
