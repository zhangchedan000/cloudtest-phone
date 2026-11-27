package phones

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/touch"
)

// 收短信（2026-11-27 用户要求，对齐魔云腾）：管理端填一条短信（发件号 + 正文），手机就「收到」一条——
// 基带（modem/sms.go）主动上报 +CMT，安卓框架写进短信库、弹通知，和真收到一条一样。
// 走已有的命令通道 touch.Send（和点击、藏属性一个路子），命令格式 "sms <base64发件号> <base64正文>"。

// smsSenderRe 发件号：纯数字，可带开头的 +（PDU 地址段要号码；名字发件号 v1 先不支持）。
var smsSenderRe = regexp.MustCompile(`^\+?[0-9]{1,20}$`)

// MaxSMSBody 正文字数上限（超了基带会自动拆成几条）。
const MaxSMSBody = 1000

// SendSMS 让这台手机收到一条来自 sender 的短信。
func (s *Service) SendSMS(ctx context.Context, id, sender, body string) error {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		sender = "10000"
	}
	if !smsSenderRe.MatchString(sender) {
		return fmt.Errorf("%w：发件号只能是号码（可带 +）", ErrInvalid)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w：短信内容是空的", ErrInvalid)
	}
	if len([]rune(body)) > MaxSMSBody {
		return fmt.Errorf("%w：短信太长", ErrInvalid)
	}
	if err := s.requireRunning(ctx, id, true); err != nil {
		return err
	}
	info, err := s.docker.InspectContainer(ctx, containerName(id))
	if err != nil || info.State.Pid <= 0 {
		return fmt.Errorf("拿不到容器进程：%v", err)
	}
	ns := fmt.Sprintf("/proc/%d/ns/net", info.State.Pid)
	cmd := "sms " + base64.StdEncoding.EncodeToString([]byte(sender)) + " " + base64.StdEncoding.EncodeToString([]byte(body))
	if err := touch.Send(ns, cmd, 5*time.Second); err != nil {
		return fmt.Errorf("发短信失败：%w", err)
	}
	return nil
}
