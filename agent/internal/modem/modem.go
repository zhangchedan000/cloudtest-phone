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
}

// Modem 一个基带。
type Modem struct {
	spec   Spec
	mu     sync.Mutex
	cfun   int
	format int // +COPS? 回哪种名字：0 全名 / 1 简称 / 2 数字
	w      io.Writer
}

func New(spec Spec, w io.Writer) *Modem {
	return &Modem{spec: spec, cfun: 1, w: w}
}

// Serve 一直读命令、写回复（r 断了就返回）。
func (m *Modem) Serve(r io.Reader) error {
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
	}
}

func (m *Modem) reply(lines []string) {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("\r\n" + l + "\r\n")
	}
	_, _ = io.WriteString(m.w, b.String())
}

// Handle 处理一行命令，返回要回的各行（最后一行是 OK / ERROR）。
func (m *Modem) Handle(line string) []string {
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
		m.mu.Unlock()
		return nil, true
	case up == "+CGSN":
		return []string{s.IMEI}, true
	case up == "+CIMI":
		return []string{s.IMSI}, true
	case up == "+CGMR":
		return []string{s.Version}, true
	case up == "+CSQ":
		// gsm rssi,ber, cdma(2), evdo(3), lte: ss,rsrp,rsrq,rssnr,cqi, timing advance
		return []string{fmt.Sprintf("+CSQ: %d,99,-1,-1,-1,-1,-1,%d,%d,10,150,2147483647,2147483647", s.Signal, s.Signal, s.RSRP)}, true
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
	case strings.HasPrefix(up, "+CUSD"), strings.HasPrefix(up, "+CSIM"), strings.HasPrefix(up, "+CUSATD"),
		strings.HasPrefix(up, "+CUSATE"), strings.HasPrefix(up, "+CUSATT"), strings.HasPrefix(up, "+CCFCU"):
		// USSD、SIM 卡工具包、呼叫转移：没有
		return []string{"ERROR"}, false
	case strings.HasPrefix(up, "+CRSM="):
		return m.simIO(strings.TrimPrefix(c, "+CRSM=")), true
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
