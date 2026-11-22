package wifi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCryptoHas(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crypto")
	_ = os.WriteFile(p, []byte("name         : gcm(aes)\ndriver       : gcm_base\n\nname         : ccm(aes)\ndriver       : ccm_base(ctr(aes-ce),cbcmac(aes-ce))\n"), 0o644)
	old := cryptoProc
	cryptoProc = p
	defer func() { cryptoProc = old }()
	if !cryptoHas("ccm(aes)") || cryptoHas("ccm(sm4)") {
		t.Fatal("cryptoHas 判断不对")
	}
}
