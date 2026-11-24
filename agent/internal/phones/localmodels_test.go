package phones

import (
	"os"
	"path/filepath"
	"testing"
)

// 本地机型库：坏文件、引用了不存在 CPU 模板的跳过；同 ID 本地覆盖内置；重新加载后删掉的本地机型消失。
func TestLocalModels(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	dir := svc.LocalModelsDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "models"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "cpu"), 0o755))
	write := func(name, body string) { must(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)) }
	write("models/new-phone.json", `{"id":"new-phone","gpuFamily":"Mali-G610","tier":"consistent","values":{"brand":"X"},"androidVersions":[15]}`)
	write("models/bad.json", `{这不是 JSON`)
	write("models/needs-cpu.json", `{"id":"needs-cpu","cpuTree":"nope","values":{},"androidVersions":[15]}`)
	write("models/poco-x4-gt.json", `{"id":"poco-x4-gt","gpuFamily":"Mali-G610","values":{"brand":"本地版"},"androidVersions":[14]}`)
	builtin, local := svc.ReloadModels()
	if local != 2 || builtin == 0 {
		t.Fatalf("内置 %d、本地 %d（应该本地 2：new-phone、poco-x4-gt）", builtin, local)
	}
	if m := svc.model("new-phone"); m == nil || !m.Local {
		t.Fatal("本地机型没加进来")
	}
	if svc.model("needs-cpu") != nil {
		t.Fatal("引用了不存在 CPU 模板的应该跳过")
	}
	if m := svc.model("poco-x4-gt"); m == nil || m.Values["brand"] != "本地版" {
		t.Fatal("同 ID 本地的应该覆盖内置的")
	}
	must(os.Remove(filepath.Join(dir, "models/new-phone.json")))
	must(os.Remove(filepath.Join(dir, "models/poco-x4-gt.json")))
	svc.ReloadModels()
	if svc.model("new-phone") != nil {
		t.Fatal("删掉的本地机型重新加载后还在")
	}
	if m := svc.model("poco-x4-gt"); m == nil || m.Local {
		t.Fatal("删掉本地覆盖后应该回到内置的")
	}
}
