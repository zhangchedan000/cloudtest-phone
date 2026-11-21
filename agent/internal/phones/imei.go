package phones

import (
	"context"
	"debug/elf"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IMEI 补丁（踩坑 #115）：框架 getImei() 走 Cuttlefish RIL（/vendor/lib64/libril-modem-lib.so）的
// RefRadioModem::getImei，它返回写死的 IMEI（不查管家 AT+CGSN）。管家本就每台生成唯一 IMEI（simSpec），
// 只是被这层盖掉。做法：建机时把该台唯一 IMEI 打进一份 libril-modem-lib.so 副本，开机前只读
// bind-mount 到 /vendor/lib64/libril-modem-lib.so（和 build.prop 改写一个机制，buildprop.go）。
//
//   - 安卓14：IMEI 是 .rodata 里一个孤立的 15 位数字串（null 包住）→ 直接改字符串（"str" 模式）。
//   - 安卓15：IMEI 是 getImei 里用 movz/movk 立即数拼到栈上的（编译器 SSO 内联）→ 改那几条指令的
//     立即数（"imm" 模式，见 a15Pattern）。
//   - 安卓16：镜像里没有 libril-modem-lib.so，radio 走 libreference-ril（AT+CGSN→管家），IMEI 本就每台
//     唯一，不需要补丁 → imageRilLib 取不到库 → kind="" → 跳过。
//
// 任何一步失败/定位不到都只记日志、照镜像原样开机，不挡开机、不影响老手机。

const rilLibPath = "/vendor/lib64/libril-modem-lib.so"

// defImei 这几版 Cuttlefish RIL 写死的默认 IMEI（14=.rodata 串；15=立即数拼的，都是这个值）。
const defImei = "867400022047199"

// imei15re 匹配 .rodata 里被 \x00 包住的 15 位纯数字串（安卓14 getImei 的默认 IMEI）。
var imei15re = regexp.MustCompile(`\x00([0-9]{15})\x00`)

// getImeiSym Cuttlefish RefRadioModem::getImei(int) 的 mangled 符号。
const getImeiSym = "_ZN2cf3ril13RefRadioModem7getImeiEi"

// a15Pattern 安卓15 getImei 里拼 IMEI 的 8 条 movz/movk：imm 是拼 defImei 时的立即数，lo/hi 是这条
// 立即数的低/高字节对应 IMEI 的第几位数字（d3 被第2、3条共用，值一样所以一致）。校验：
//
//	0x3638="86"→d0d1  0x3437="74"→d2d3  0x3034="40"→d3d4  0x3030="00"→d5d6
//	0x3232="22"→d7d8  0x3430="04"→d9d10 0x3137="71"→d11d12 0x3939="99"→d13d14  = 867400022047199
var a15Pattern = []struct {
	imm    uint32
	lo, hi int
}{
	{0x3638, 0, 1}, {0x3437, 2, 3}, {0x3034, 3, 4}, {0x3030, 5, 6},
	{0x3232, 7, 8}, {0x3430, 9, 10}, {0x3137, 11, 12}, {0x3939, 13, 14},
}

// immSlot 一条要改立即数的指令：文件偏移 + 它编码 IMEI 的哪两位（低字节=lo，高字节=hi）。
type immSlot struct {
	Off int64 `json:"off"`
	Lo  int   `json:"lo"`
	Hi  int   `json:"hi"`
}

// rilPlan 这个镜像的 libril-modem-lib.so 怎么改 IMEI。
type rilPlan struct {
	Kind   string    `json:"kind"`   // "str" | "imm" | "" (跳过)
	StrOff int64     `json:"strOff"` // str 模式：15 字节串的偏移
	Imm    []immSlot `json:"imm"`    // imm 模式：要改的指令
}

// movImm16 若 word 是 movz/movk（w 或 x 寄存器），返回它的 16 位立即数和 true。
func movImm16(word uint32) (uint32, bool) {
	top := word & 0x7F800000
	if top != 0x52800000 && top != 0x72800000 { // movz / movk
		return 0, false
	}
	return (word >> 5) & 0xFFFF, true
}

// findImmPlan 在 [start,end) 的指令里按 a15Pattern 顺序找那 8 条 movz/movk，返回 imm 计划。
func findImmPlan(data []byte, start, end int64) []immSlot {
	var slots []immSlot
	k := 0
	for off := start; off+4 <= end && k < len(a15Pattern); off += 4 {
		word := uint32(data[off]) | uint32(data[off+1])<<8 | uint32(data[off+2])<<16 | uint32(data[off+3])<<24
		if imm, ok := movImm16(word); ok && imm == a15Pattern[k].imm {
			slots = append(slots, immSlot{Off: off, Lo: a15Pattern[k].lo, Hi: a15Pattern[k].hi})
			k++
		}
	}
	if k == len(a15Pattern) {
		return slots
	}
	return nil
}

// imageRilPlan 取镜像里的 libril-modem-lib.so、算出改 IMEI 的计划，缓存在 DataDir/cache/rillib2/<镜像ID>/。
// 镜像没这个库（安卓16）、或定位不到 → plan.Kind=""（跳过）。
func (s *Service) imageRilPlan(ctx context.Context, image string) (base string, plan rilPlan, err error) {
	id, err := s.run.Run(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil || strings.TrimSpace(id) == "" {
		return "", plan, fmt.Errorf("读镜像 ID：%v %s", err, id)
	}
	short := strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	dir := filepath.Join(s.cfg.DataDir, "cache", "rillib2", short)
	base = filepath.Join(dir, "base.so")
	meta := filepath.Join(dir, "plan.json")
	if b, err := os.ReadFile(meta); err == nil {
		_ = json.Unmarshal(b, &plan)
		return base, plan, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", plan, err
	}
	tmp := "pp-rillib-" + short[:12]
	_, _ = s.run.Run(ctx, "docker", "rm", "-f", tmp)
	if out, err := s.run.Run(ctx, "docker", "create", "--name", tmp, image); err != nil {
		return "", plan, fmt.Errorf("建临时容器：%v %s", err, out)
	}
	defer s.run.Run(context.Background(), "docker", "rm", "-f", tmp)
	if _, err := s.run.Run(ctx, "docker", "cp", tmp+":"+rilLibPath, base); err != nil {
		// 镜像没这个库（安卓16 走 libreference-ril，本就每台唯一）：记成跳过，不再重试
		_ = writeJSON(meta, plan)
		return base, plan, nil
	}
	data, err := os.ReadFile(base)
	if err != nil {
		return "", plan, err
	}
	// 先试字符串模式（安卓14）：唯一的 null 包住的 15 位数字串
	if ms := imei15re.FindAllSubmatchIndex(data, -1); len(ms) == 1 {
		plan = rilPlan{Kind: "str", StrOff: int64(ms[0][2])}
		_ = writeJSON(meta, plan)
		return base, plan, nil
	}
	// 再试立即数模式（安卓15）：在 getImei 里找 a15Pattern
	if start, size, ok := elfSym(base, getImeiSym); ok {
		if slots := findImmPlan(data, start, start+size); slots != nil {
			plan = rilPlan{Kind: "imm", Imm: slots}
		}
	}
	_ = writeJSON(meta, plan)
	return base, plan, nil
}

// elfSym 返回符号在文件里的偏移和大小（找不到 ok=false）。
func elfSym(path, name string) (off, size int64, ok bool) {
	f, err := elf.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	syms, _ := f.Symbols()
	dyn, _ := f.DynamicSymbols()
	for _, list := range [][]elf.Symbol{syms, dyn} {
		for _, sym := range list {
			if sym.Name != name || sym.Section == elf.SHN_UNDEF || int(sym.Section) >= len(f.Sections) {
				continue
			}
			sec := f.Sections[sym.Section]
			return int64(sym.Value-sec.Addr) + int64(sec.Offset), int64(sym.Size), true
		}
	}
	return 0, 0, false
}

func writeJSON(path string, v any) error {
	b, _ := json.Marshal(v)
	return os.WriteFile(path, b, 0o644)
}

// prepareImei 生成这台手机打了 IMEI 补丁的 libril-modem-lib.so，返回要加的挂载（宿主机文件:容器路径:ro）。
// imei 必须是 15 位数字；镜像没库/定位不到（plan.Kind==""）时返回空串（不打补丁、不报错）。
func (s *Service) prepareImei(ctx context.Context, id, image, imei string) (string, error) {
	if len(imei) != 15 || strings.TrimLeft(imei, "0123456789") != "" {
		return "", fmt.Errorf("IMEI 不是 15 位数字：%q", imei)
	}
	base, plan, err := s.imageRilPlan(ctx, image)
	if err != nil {
		return "", err
	}
	if plan.Kind == "" {
		return "", nil // 没库（安卓16）或定位不到：不处理
	}
	data, err := os.ReadFile(base)
	if err != nil {
		return "", err
	}
	if err := applyImeiPatch(data, plan, imei); err != nil {
		return "", err
	}
	dir := filepath.Join(s.phoneDir(id), "ril")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "libril-modem-lib.so")
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return "", err
	}
	return out + ":" + rilLibPath + ":ro", nil
}

// applyImeiPatch 按 plan 把 data 里的默认 IMEI 改成 imei（15 位）。就地改 data。
func applyImeiPatch(data []byte, plan rilPlan, imei string) error {
	switch plan.Kind {
	case "str":
		if plan.StrOff+15 > int64(len(data)) {
			return fmt.Errorf("str 偏移越界")
		}
		copy(data[plan.StrOff:plan.StrOff+15], []byte(imei))
		return nil
	case "imm":
		for _, sl := range plan.Imm {
			if sl.Off+4 > int64(len(data)) {
				return fmt.Errorf("imm 偏移越界")
			}
			word := uint32(data[sl.Off]) | uint32(data[sl.Off+1])<<8 | uint32(data[sl.Off+2])<<16 | uint32(data[sl.Off+3])<<24
			newImm := uint32(imei[sl.Hi])<<8 | uint32(imei[sl.Lo]) // 高位数字在高字节
			word = (word &^ (0xFFFF << 5)) | (newImm << 5)
			data[sl.Off] = byte(word)
			data[sl.Off+1] = byte(word >> 8)
			data[sl.Off+2] = byte(word >> 16)
			data[sl.Off+3] = byte(word >> 24)
		}
		return nil
	}
	return fmt.Errorf("未知 plan.Kind=%q", plan.Kind)
}
