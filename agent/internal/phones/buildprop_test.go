package phones

import (
	"strings"
	"testing"
)

func TestRewriteBuildProp(t *testing.T) {
	orig := "# begin common build properties\nro.product.system.model=redroid14_arm64_only\nro.build.description=redroid_arm64_only-userdebug 14 X eng.frank test-keys\nro.build.version.sdk=34\n"
	final := map[string]string{"ro.product.system.model": "mainline", "ro.build.version.sdk": "34"}
	extra := map[string]string{"ro.build.description": "missi_phone_global-user 14 UP1A.230905.011 V816.0.2.0.UMFMIXM release-keys", "ro.product.marketname": "Xiaomi 13T"}
	got := rewriteBuildProp(orig, final, extra, true)
	for _, want := range []string{
		"# begin common build properties\n",
		"ro.product.system.model=mainline\n",
		"ro.build.description=missi_phone_global-user 14 UP1A.230905.011 V816.0.2.0.UMFMIXM release-keys\n",
		"ro.build.version.sdk=34\n",
		"ro.product.marketname=Xiaomi 13T\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺 %q：\n%s", want, got)
		}
	}
	if strings.Contains(got, "redroid") {
		t.Fatalf("还有 redroid：\n%s", got)
	}
	if strings.Count(got, "ro.build.description=") != 1 {
		t.Fatalf("description 应该只有一行：\n%s", got)
	}
}

// 带空格的属性不进启动参数，改走 build.prop；description 按最终值拼。
func TestSpacedPropsGoToBuildProp(t *testing.T) {
	svc, _, _ := newTestService(t, true, "software")
	svc.model("poco-x4-gt").BuildProps = nil
	p, _ := svc.Create(CreateRequest{Name: "A", ModelID: "poco-x4-gt", AndroidVersion: 14, ProxyID: DirectDevProxyID})
	args, spaced := svc.bootProps(*mustPhone(t, svc, p.ID), svc.store.Snapshot())
	for _, a := range args {
		if strings.Contains(a, " ") {
			t.Fatalf("启动参数带空格：%q", a)
		}
	}
	if spaced["ro.product.marketname"] != "POCO X4 GT" {
		t.Errorf("市场名应走 build.prop：%v", spaced)
	}
	if d := spaced["ro.build.description"]; d != "xaga_global-user 14 UP1A.231005.007 OS2.0.3.0.ULOMIXM release-keys" {
		t.Errorf("description 不对：%q", d)
	}
}

// 不追加的文件（system_ext 等）：原文件里有的带空格属性也要替换，否则后加载的文件会把值盖回 redroid。
func TestRewriteBuildPropReplacesSpacedInEveryFile(t *testing.T) {
	orig := "ro.product.system_ext.model=redroid14_arm64\nro.product.system_ext.name=redroid_arm64\n"
	got := rewriteBuildProp(orig, map[string]string{"ro.product.system_ext.name": "missi_phone_global"}, map[string]string{"ro.product.system_ext.model": "POCO X4 GT", "ro.build.description": "x y"}, false)
	if strings.Contains(got, "redroid") || !strings.Contains(got, "ro.product.system_ext.model=POCO X4 GT\n") {
		t.Fatalf("同名带空格属性没替换：\n%s", got)
	}
	if strings.Contains(got, "ro.build.description") {
		t.Fatal("不追加的文件不该多出原来没有的属性")
	}
}
