package com.prism.probe;

import android.app.Activity;
import android.bluetooth.BluetoothAdapter;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.content.pm.FeatureInfo;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.hardware.Sensor;
import android.hardware.SensorManager;
import android.hardware.camera2.CameraCharacteristics;
import android.hardware.camera2.CameraManager;
import android.media.MediaCodecInfo;
import android.media.MediaCodecList;
import android.media.MediaDrm;
import android.net.Uri;
import android.net.wifi.WifiInfo;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Bundle;
import android.os.IBinder;
import android.os.Parcel;
import android.os.StatFs;
import android.os.SystemClock;
import android.provider.Settings;
import android.telephony.TelephonyManager;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.FileReader;
import java.io.InputStreamReader;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.regex.Pattern;

/**
 * pp-probe：按风控 SDK 的思路查这台手机（普通 App 身份、普通权限能拿到的），结果写 JSON。
 * facts：采到的值（标识类拿去和别的手机比，看有没有几台一样的）；tells：本机自己就能看出的破绽。
 * 启动：am start -n com.prism.probe/.Main；结果：/sdcard/Android/data/com.prism.probe/files/probe.json（写完 logcat 打 PPPROBE done）
 */
public class Main extends Activity {
    static final String TAG = "PPPROBE";
    JSONObject facts = new JSONObject();
    JSONArray tells = new JSONArray();
    static final Pattern EMU = Pattern.compile("(?i)redroid|goldfish|ranchu|qemu|docker|cuttlefish|vbox|genymotion|nox|bluestacks|dobox|rk3588|rockchip|\\brk30|prism|pp[-_]init|pp-cmdc|mytos|armcloud|vmos");

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        new Thread(new Runnable() { public void run() { runAll(); } }).start();
    }

    void put(String k, Object v) { try { facts.put(k, v == null ? JSONObject.NULL : v); } catch (Exception e) { } }
    void tell(String code, String msg) {
        try { JSONObject o = new JSONObject(); o.put("code", code); o.put("msg", msg); tells.put(o); } catch (Exception e) { }
    }
    interface P { void run() throws Exception; }
    void probe(String name, P p) {
        try { p.run(); } catch (Throwable t) { put("err." + name, t.getClass().getSimpleName() + ": " + t.getMessage()); }
    }

    static String read(String path, int max) {
        try {
            BufferedReader r = new BufferedReader(new FileReader(path));
            StringBuilder sb = new StringBuilder();
            char[] buf = new char[4096]; int n;
            while ((n = r.read(buf)) > 0 && sb.length() < max) sb.append(buf, 0, n);
            r.close();
            return sb.length() > max ? sb.substring(0, max) : sb.toString();
        } catch (Exception e) { return null; }
    }
    static String exec(String... cmd) {
        try {
            Process p = Runtime.getRuntime().exec(cmd);
            BufferedReader r = new BufferedReader(new InputStreamReader(p.getInputStream()));
            StringBuilder sb = new StringBuilder(); String l;
            while ((l = r.readLine()) != null && sb.length() < 200000) sb.append(l).append('\n');
            p.waitFor();
            return sb.toString();
        } catch (Exception e) { return null; }
    }
    static String hex(byte[] b) {
        if (b == null) return null;
        StringBuilder sb = new StringBuilder();
        for (byte x : b) sb.append(String.format("%02x", x & 0xff));
        return sb.toString();
    }
    static String[] ls(String dir) { String[] a = new File(dir).list(); if (a != null) Arrays.sort(a); return a; }

    void runAll() {
        final Context ctx = this;
        // ---------- 标识（跨机比对用） ----------
        probe("android_id", new P() { public void run() { put("id.android_id", Settings.Secure.getString(getContentResolver(), Settings.Secure.ANDROID_ID)); } });
        probe("boot_id", new P() { public void run() { put("id.boot_id", trim(read("/proc/sys/kernel/random/boot_id", 100))); } });
        probe("widevine", new P() { public void run() throws Exception {
            MediaDrm d = new MediaDrm(new UUID(0xEDEF8BA979D64ACEL, 0xA3C827DCD51D21EDL));
            put("id.widevine_device_id", hex(d.getPropertyByteArray(MediaDrm.PROPERTY_DEVICE_UNIQUE_ID)));
            put("drm.widevine_level", d.getPropertyString("securityLevel"));
            put("drm.widevine_vendor", d.getPropertyString(MediaDrm.PROPERTY_VENDOR));
            put("drm.widevine_version", d.getPropertyString(MediaDrm.PROPERTY_VERSION));
            try { put("drm.widevine_system_id", d.getPropertyString("systemId")); } catch (Exception e) { }
            d.close();
        } });
        if (facts.has("err.widevine")) tell("drm", "没有 Widevine（数字版权组件）：真机都有，风控常拿它的设备 ID 当指纹");
        probe("emmc", new P() { public void run() {
            JSONObject o = new JSONObject();
            String[] blk = ls("/sys/block");
            if (blk != null) for (String s : blk) {
                String cid = read("/sys/block/" + s + "/device/cid", 200), ser = read("/sys/block/" + s + "/device/serial", 200);
                try { if (cid != null) o.put(s + ".cid", trim(cid)); if (ser != null) o.put(s + ".serial", trim(ser)); } catch (Exception e) { }
            }
            put("id.block", o);
            put("id.soc_serial", trim(read("/sys/devices/soc0/serial_number", 100)));
            put("id.dt_serial", trim(read("/proc/device-tree/serial-number", 100)));
        } });
        probe("gsf", new P() { public void run() {
            Cursor c = getContentResolver().query(Uri.parse("content://com.google.android.gsf.gservices"), null, null, new String[]{"android_id"}, null);
            if (c != null) { if (c.moveToFirst() && c.getColumnCount() >= 2) put("id.gsf_id", c.getString(1)); c.close(); }
        } });
        probe("gaid", new P() { public void run() throws Exception { put("id.gaid", adId()); } });
        probe("wifi", new P() { public void run() {
            WifiManager w = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
            WifiInfo i = w.getConnectionInfo();
            put("id.wifi_mac", i.getMacAddress()); put("net.wifi_bssid", i.getBSSID()); put("net.wifi_ssid", i.getSSID());
            put("net.wifi_rssi", i.getRssi()); put("net.wifi_scan_count", w.getScanResults() == null ? -1 : w.getScanResults().size());
            if (w.getScanResults() != null && w.getScanResults().size() <= 1) tell("wifi", "WiFi 只扫到 " + w.getScanResults().size() + " 个热点（真机周围一般有好几个）");
        } });
        probe("bt", new P() { public void run() {
            BluetoothAdapter a = BluetoothAdapter.getDefaultAdapter();
            if (a != null) { put("id.bt_name", a.getName()); put("id.bt_addr", a.getAddress()); }
        } });
        probe("tel", new P() { public void run() {
            TelephonyManager t = (TelephonyManager) getSystemService(Context.TELEPHONY_SERVICE);
            try { put("id.imei", t.getImei()); } catch (Exception e) { put("id.imei", "无权限：" + e.getClass().getSimpleName()); }
            put("tel.operator", t.getNetworkOperatorName()); put("tel.sim_country", t.getSimCountryIso()); put("tel.net_country", t.getNetworkCountryIso());
            put("tel.sim_state", t.getSimState()); put("tel.phone_type", t.getPhoneType());
            try { put("tel.cell_count", t.getAllCellInfo() == null ? -1 : t.getAllCellInfo().size()); } catch (Exception e) { put("tel.cell_count", "无权限"); }
        } });
        // ---------- 系统 ----------
        probe("build", new P() { public void run() {
            put("build.model", Build.MODEL); put("build.brand", Build.BRAND); put("build.device", Build.DEVICE); put("build.board", Build.BOARD);
            put("build.hardware", Build.HARDWARE); put("build.product", Build.PRODUCT); put("build.fingerprint", Build.FINGERPRINT);
            put("build.host", Build.HOST); put("build.user", Build.USER); put("build.tags", Build.TAGS); put("build.type", Build.TYPE);
            put("build.soc", Build.VERSION.SDK_INT >= 31 ? Build.SOC_MANUFACTURER + " " + Build.SOC_MODEL : "");
            put("build.radio", Build.getRadioVersion()); put("build.bootloader", Build.BOOTLOADER);
            put("os.version", System.getProperty("os.version"));
        } });
        probe("props", new P() { public void run() {
            String all = exec("getprop");
            int n = 0; StringBuilder hits = new StringBuilder();
            if (all != null) for (String l : all.split("\n")) if (EMU.matcher(l).find()) { n++; if (hits.length() < 800) hits.append(l).append(" | "); }
            put("props.emu_hits", n);
            if (n > 0) tell("props", n + " 条属性带模拟器 / 容器 / 瑞芯微字样：" + hits);
            put("props.board_platform", getprop("ro.board.platform")); put("props.hardware", getprop("ro.hardware"));
            put("props.adb_enabled", Settings.Global.getString(getContentResolver(), Settings.Global.ADB_ENABLED));
            put("props.dev_options", Settings.Global.getString(getContentResolver(), Settings.Global.DEVELOPMENT_SETTINGS_ENABLED));
            put("props.debuggable", getprop("ro.debuggable")); put("props.secure", getprop("ro.secure"));
            put("props.verifiedbootstate", getprop("ro.boot.verifiedbootstate")); put("props.flash_locked", getprop("ro.boot.flash.locked"));
            put("props.selinux", getprop("ro.boot.selinux"));
            put("props.ril_impl", getprop("gsm.version.ril-impl")); put("props.baseband", getprop("gsm.version.baseband"));
            if ("unknown".equals(Build.BOARD) || Build.BOARD.isEmpty()) tell("build", "Build.BOARD 是 unknown");
            if ("1.0.0.0".equals(Build.getRadioVersion())) tell("build", "基带版本是 1.0.0.0（模拟器默认值）");
            String ril = getprop("gsm.version.ril-impl"), hw = getprop("ro.hardware");
            if ("qcom".equals(hw) && ril != null && !ril.contains("Qualcomm")) tell("build", "高通机型的电话模块却是「" + ril + "」");
            if ("1".equals(Settings.Global.getString(getContentResolver(), Settings.Global.ADB_ENABLED))) tell("adb", "USB 调试（ADB）开着");
        } });
        probe("kernel", new P() { public void run() {
            put("k.proc_version", trim(read("/proc/version", 500)));
            put("k.cmdline", trim(read("/proc/cmdline", 2000)));
            put("k.dt_model", trim(read("/proc/device-tree/model", 200)));
            put("k.dt_compatible", trim(read("/proc/device-tree/compatible", 300)));
            put("k.modules", trim(read("/proc/modules", 3000)));
            put("k.partitions", trim(read("/proc/partitions", 3000)));
            put("k.diskstats_devs", firstCols(read("/proc/diskstats", 20000), 2));
            put("k.loadavg", trim(read("/proc/loadavg", 100)));
            put("k.selinux_enforce", trim(read("/sys/fs/selinux/enforce", 10)));
            for (String k : new String[]{"k.cmdline", "k.dt_model", "k.dt_compatible", "k.modules", "k.partitions", "k.diskstats_devs"}) {
                Object v = facts.opt(k);
                if (v instanceof String && EMU.matcher((String) v).find()) tell("kernel", k + " 有模拟器 / 容器 / 瑞芯微字样：" + clip((String) v, 200));
            }
            String pv = facts.isNull("k.proc_version") ? "" : facts.optString("k.proc_version");
            if (pv.length() > 0 && !pv.contains(String.valueOf(System.getProperty("os.version")))) tell("kernel", "/proc/version 和 uname 对不上：" + clip(pv, 120) + " / " + System.getProperty("os.version"));
        } });
        probe("time", new P() { public void run() {
            String stat = read("/proc/stat", 100000);
            long btime = -1;
            if (stat != null) for (String l : stat.split("\n")) if (l.startsWith("btime ")) btime = Long.parseLong(l.substring(6).trim());
            long bootByUptime = (System.currentTimeMillis() - SystemClock.elapsedRealtime()) / 1000;
            put("time.btime", btime); put("time.boot_by_uptime", bootByUptime); put("time.uptime_s", SystemClock.elapsedRealtime() / 1000);
            if (btime > 0 && Math.abs(btime - bootByUptime) > 120) tell("time", "/proc/stat 的开机时间和 App 算的开机时间差 " + (bootByUptime - btime) + " 秒（容器共用宿主机开机时间）");
            if (stat != null) { String cpu = stat.split("\n")[0]; put("time.proc_stat_cpu", cpu); }
        } });
        probe("fs", new P() { public void run() {
            put("fs.dev", join(ls("/dev")));
            put("fs.sys_class", join(ls("/sys/class")));
            put("fs.platform_devices", clip(join(ls("/sys/bus/platform/devices")), 3000));
            put("fs.net", join(ls("/sys/class/net")));
            put("fs.thermal_types", thermal());
            put("fs.power_supply", join(ls("/sys/class/power_supply")));
            String mounts = read("/proc/self/mounts", 50000);
            int n = 0; StringBuilder h = new StringBuilder();
            if (mounts != null) for (String l : mounts.split("\n")) if (EMU.matcher(l).find() || l.contains("overlay") || l.contains("/dev/block/loop")) { n++; if (h.length() < 500) h.append(l).append(" | "); }
            put("fs.mount_hits", n);
            if (n > 0) tell("mounts", n + " 条挂载带容器痕迹：" + h);
            String maps = read("/proc/self/maps", 400000);
            if (maps != null) for (String l : maps.split("\n")) if (!l.contains(getPackageName()) && EMU.matcher(l).find()) { tell("maps", "本进程内存映射里有可疑库：" + clip(l.replaceAll("^\\S+\\s+\\S+\\s+\\S+\\s+\\S+\\s+\\S+\\s+", ""), 160)); break; }
            for (String f : new String[]{"/system/bin/su", "/system/xbin/su", "/sbin/su", "/data/local/tmp/su", "/system/app/Superuser.apk", "/data/adb/magisk", "/init.goldfish.rc", "/pp-init", "/data/.ppf", "/system/bin/pp-cmdc", "/dev/goldfish_pipe", "/dev/qemu_pipe", "/dev/socket/qemud", "/system/lib/libc_malloc_debug_qemu.so", "/sys/qemu_trace", "/system/bin/qemu-props", "/dev/mpp_service", "/dev/rga", "/dev/rkvenc", "/dev/vpu_service", "/dev/rknpu"})
                if (new File(f).exists()) tell("file", "存在 " + f);
            // 高通真机是 Adreno（/dev/kgsl-3d0），有 Mali 的设备节点就对不上
            if ("qcom".equals(getprop("ro.hardware")) && new File("/dev/mali0").exists() && !new File("/dev/kgsl-3d0").exists()) tell("gpu", "硬件名是高通，却有 Mali 显卡设备 /dev/mali0、没有 Adreno 的 /dev/kgsl-3d0");
            String plat = facts.optString("fs.platform_devices") + " " + facts.optString("fs.thermal_types") + " " + facts.optString("fs.dev");
            if (EMU.matcher(plat).find()) tell("sysfs", "/sys 或 /dev 里有瑞芯微 / 模拟器字样");
        } });
        probe("net", new P() { public void run() {
            put("net.tcp_listen", listen(read("/proc/net/tcp", 100000)) + " " + listen(read("/proc/net/tcp6", 100000)));
            put("net.route", trim(read("/proc/net/route", 3000)));
            put("net.ifaces_dev", firstCols(read("/proc/net/dev", 20000), 1));
            String l = facts.optString("net.tcp_listen");
            if (l.contains(":5555")) tell("net", "5555 端口在监听（ADB 网络调试）");
        } });
        // ---------- 硬件一致性 ----------
        probe("sensors", new P() { public void run() {
            SensorManager sm = (SensorManager) getSystemService(Context.SENSOR_SERVICE);
            JSONArray a = new JSONArray();
            for (Sensor s : sm.getSensorList(Sensor.TYPE_ALL)) a.put(s.getType() + "|" + s.getName() + "|" + s.getVendor() + "|" + s.getMaximumRange());
            put("hw.sensors", a); put("hw.sensor_count", a.length());
            if (a.length() < 10) tell("sensors", "传感器只有 " + a.length() + " 个（真机一般 20 个以上，含系统融合的）");
            if (a.toString().matches("(?is).*(goldfish|ranchu|qemu|android open source).*")) tell("sensors", "传感器名字 / 厂商像模拟器");
        } });
        probe("camera", new P() { public void run() throws Exception {
            CameraManager cm = (CameraManager) getSystemService(Context.CAMERA_SERVICE);
            JSONArray a = new JSONArray();
            for (String id : cm.getCameraIdList()) {
                CameraCharacteristics c = cm.getCameraCharacteristics(id);
                a.put(id + "|facing=" + c.get(CameraCharacteristics.LENS_FACING) + "|" + c.get(CameraCharacteristics.SENSOR_INFO_PIXEL_ARRAY_SIZE)
                        + "|f=" + Arrays.toString(c.get(CameraCharacteristics.LENS_INFO_AVAILABLE_FOCAL_LENGTHS)) + "|level=" + c.get(CameraCharacteristics.INFO_SUPPORTED_HARDWARE_LEVEL));
                StringBuilder keys = new StringBuilder();
                for (CameraCharacteristics.Key<?> k : c.getKeys()) if (!k.getName().startsWith("android.")) keys.append(k.getName()).append(' ');
                if (keys.length() > 0) put("hw.camera_vendor_keys." + id, keys.toString());
            }
            put("hw.cameras", a);
            if (a.length() == 0) tell("camera", "没有摄像头");
        } });
        probe("codecs", new P() { public void run() {
            StringBuilder sb = new StringBuilder();
            for (MediaCodecInfo i : new MediaCodecList(MediaCodecList.ALL_CODECS).getCodecInfos()) sb.append(i.getName()).append(' ');
            put("hw.codecs", sb.toString());
            if (sb.toString().contains("c2.rk.") || sb.toString().toLowerCase().contains("rockchip")) tell("codecs", "编解码器列表里有瑞芯微的（c2.rk.*）");
        } });
        probe("features", new P() { public void run() {
            PackageManager pm = getPackageManager();
            StringBuilder sb = new StringBuilder();
            for (FeatureInfo f : pm.getSystemAvailableFeatures()) if (f.name != null) sb.append(f.name).append(' ');
            put("sys.features", sb.toString());
            String[] libs = pm.getSystemSharedLibraryNames();
            put("sys.shared_libs_count", libs == null ? 0 : libs.length);
            put("sys.shared_libs", libs == null ? "" : join(libs));
            if (sb.indexOf("android.hardware.camera") >= 0 && facts.optJSONArray("hw.cameras") != null && facts.optJSONArray("hw.cameras").length() == 0)
                tell("features", "系统特性说有摄像头，实际 0 个");
            if (sb.indexOf("android.hardware.ethernet") >= 0) tell("features", "系统特性有有线网口（手机没有）");
        } });
        probe("packages", new P() { public void run() {
            PackageManager pm = getPackageManager();
            List<PackageInfo> ps = pm.getInstalledPackages(0);
            put("sys.package_count", ps.size());
            StringBuilder sus = new StringBuilder();
            for (PackageInfo p : ps) if (!p.packageName.equals(getPackageName()) && EMU.matcher(p.packageName).find() || p.packageName.matches(".*(supersu|magisk|xposed|lsposed|substrate|frida|virtualapp|parallel|dualspace).*")) sus.append(p.packageName).append(' ');
            if (sus.length() > 0) tell("packages", "可疑应用：" + sus);
            StringBuilder all = new StringBuilder();
            for (PackageInfo p : ps) all.append(p.packageName).append(' ');
            put("sys.packages", all.toString());
        } });
        probe("storage", new P() { public void run() {
            StatFs s = new StatFs("/data");
            put("hw.data_total", s.getTotalBytes()); put("hw.data_free", s.getAvailableBytes());
            put("hw.mem_total_kb", trim(grep(read("/proc/meminfo", 5000), "MemTotal")));
            put("hw.cpuinfo_hardware", trim(grep(read("/proc/cpuinfo", 20000), "Hardware")));
            put("hw.cpu_parts", grepAll(read("/proc/cpuinfo", 20000), "CPU part"));
            put("hw.cpu0_driver", trim(read("/sys/devices/system/cpu/cpu0/cpufreq/scaling_driver", 100)));
        } });
        // ---------- 写结果 ----------
        try {
            JSONObject out = new JSONObject();
            out.put("facts", facts); out.put("tells", tells); out.put("time", System.currentTimeMillis());
            File dir = getExternalFilesDir(null);
            if (dir != null) {
                FileOutputStream f = new FileOutputStream(new File(dir, "probe.json"));
                f.write(out.toString(1).getBytes("UTF-8")); f.close();
            }
            Log.i(TAG, "done " + tells.length() + " tells");
        } catch (Exception e) { Log.e(TAG, "write failed", e); }
        finish();
    }

    String adId() throws Exception {
        final IBinder[] holder = new IBinder[1];
        final CountDownLatch latch = new CountDownLatch(1);
        ServiceConnection sc = new ServiceConnection() {
            public void onServiceConnected(ComponentName n, IBinder b) { holder[0] = b; latch.countDown(); }
            public void onServiceDisconnected(ComponentName n) { }
        };
        Intent i = new Intent("com.google.android.gms.ads.identifier.service.START").setPackage("com.google.android.gms");
        if (!bindService(i, sc, Context.BIND_AUTO_CREATE)) return "没有谷歌服务";
        try {
            if (!latch.await(10, TimeUnit.SECONDS) || holder[0] == null) return "连不上";
            Parcel d = Parcel.obtain(), r = Parcel.obtain();
            try {
                d.writeInterfaceToken("com.google.android.gms.ads.identifier.internal.IAdvertisingIdService");
                holder[0].transact(1, d, r, 0);
                r.readException();
                return r.readString();
            } finally { d.recycle(); r.recycle(); }
        } finally { unbindService(sc); }
    }

    static String getprop(String k) { String v = exec("getprop", k); return v == null ? null : v.trim(); }
    static String trim(String s) { return s == null ? null : s.trim(); }
    static String clip(String s, int n) { return s == null ? null : s.length() > n ? s.substring(0, n) + "…" : s; }
    static String join(String[] a) { if (a == null) return null; StringBuilder sb = new StringBuilder(); for (String s : a) sb.append(s).append(' '); return sb.toString().trim(); }
    static String grep(String text, String key) { if (text == null) return null; for (String l : text.split("\n")) if (l.startsWith(key)) return l; return null; }
    static String grepAll(String text, String key) { if (text == null) return null; StringBuilder sb = new StringBuilder(); for (String l : text.split("\n")) if (l.startsWith(key)) sb.append(l.replaceAll(".*:\\s*", "")).append(' '); return sb.toString().trim(); }
    static String firstCols(String text, int col) {
        if (text == null) return null;
        List<String> out = new ArrayList<String>();
        for (String l : text.split("\n")) { String[] f = l.trim().split("[\\s:]+"); if (f.length > col) out.add(f[col]); }
        return out.toString();
    }
    static String thermal() {
        String[] z = ls("/sys/class/thermal");
        if (z == null) return null;
        StringBuilder sb = new StringBuilder();
        for (String s : z) { String t = read("/sys/class/thermal/" + s + "/type", 100); if (t != null) sb.append(t.trim()).append(' '); }
        return sb.toString().trim();
    }
    static String listen(String tcp) {
        if (tcp == null) return "";
        StringBuilder sb = new StringBuilder();
        for (String l : tcp.split("\n")) {
            String[] f = l.trim().split("\\s+");
            if (f.length > 3 && "0A".equals(f[3])) { String[] a = f[1].split(":"); sb.append(':').append(Integer.parseInt(a[a.length - 1], 16)).append(' '); }
        }
        return sb.toString().trim();
    }
}
