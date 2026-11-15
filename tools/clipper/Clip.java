// 云手机剪贴板小工具：在容器里用 app_process 运行（不装 App、不装输入法），直接调系统剪贴板服务。
//
//   CLASSPATH=/data/local/tmp/pp-clip.dex app_process / Clip set <base64(UTF-8 文字)>   → 打印 ok
//   CLASSPATH=/data/local/tmp/pp-clip.dex app_process / Clip get                       → 打印 ok <base64> 或 empty
//
// 文字用 base64 传，中文、换行、引号都不怕被 shell 弄坏。
// 只用反射调隐藏接口（app_process 起的进程不受隐藏接口限制），不依赖 android.jar 编译；
// setPrimaryClip / getPrimaryClip 的参数每个安卓版本不一样，按参数类型自动填（ClipData、包名、null、0）。
// 做法参考 scrcpy 的剪贴板同步。编译：tools/clipper/build.sh。

import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.Base64;

public class Clip {
    // 先切成 shell 用户（uid 2000，包名 com.android.shell）：安卓 10 以后只有前台 App、输入法，
    // 和有 READ_CLIPBOARD_IN_BACKGROUND 权限的包能读剪贴板；这个权限系统只给了 Shell（"Shell can access the
    // clipboard for testing purposes"）。root（uid 0）、系统用户（uid 1000）读都会被拒（2026-11-15 实测，
    // 日志 "Denying clipboard access to root / android"）。scrcpy 也是以 shell 身份读的。
    private static final String[] PACKAGES = {"com.android.shell", "android", "root"};

    private static void becomeSystem() {
        try {
            Class<?> os = Class.forName("android.system.Os");
            if ((int) os.getMethod("getuid").invoke(null) != 0) return;
            os.getMethod("setgid", int.class).invoke(null, 2000);
            os.getMethod("setuid", int.class).invoke(null, 2000);
        } catch (Throwable ignored) {
            // 切不了就算了，后面按 root 身份试
        }
    }

    public static void main(String[] args) {
        try {
            becomeSystem();
            if (args.length == 2 && args[0].equals("set")) {
                set(new String(Base64.getDecoder().decode(args[1]), StandardCharsets.UTF_8));
                System.out.println("ok");
            } else if (args.length == 1 && args[0].equals("get")) {
                String text = get();
                System.out.println(text == null ? "empty" : "ok " + Base64.getEncoder().encodeToString(text.getBytes(StandardCharsets.UTF_8)));
            } else {
                System.out.println("error 用法：set <base64> | get");
                System.exit(2);
            }
        } catch (Throwable t) {
            Throwable c = t.getCause() != null ? t.getCause() : t;
            System.out.println("error " + c);
            System.exit(1);
        }
    }

    private static Object service() throws Exception {
        Class<?> sm = Class.forName("android.os.ServiceManager");
        Object binder = sm.getMethod("getService", String.class).invoke(null, "clipboard");
        if (binder == null) throw new IllegalStateException("没有剪贴板服务（安卓还没开机完成？）");
        Class<?> stub = Class.forName("android.content.IClipboard$Stub");
        return stub.getMethod("asInterface", Class.forName("android.os.IBinder")).invoke(null, binder);
    }

    private static Method find(Object svc, String name) {
        for (Method m : svc.getClass().getMethods()) {
            if (m.getName().equals(name)) return m;
        }
        throw new IllegalStateException("这个安卓版本没有 " + name);
    }

    /** 按参数类型填参数：第一个 String 是包名，其余 String 为 null，int 为 0（用户 0、设备 0）。 */
    private static Object[] fill(Method m, Object clip, String pkg) {
        Class<?>[] types = m.getParameterTypes();
        Object[] out = new Object[types.length];
        boolean pkgDone = false;
        for (int i = 0; i < types.length; i++) {
            Class<?> t = types[i];
            if (t == int.class) out[i] = 0;
            else if (t == boolean.class) out[i] = false;
            else if (t == String.class) { out[i] = pkgDone ? null : pkg; pkgDone = true; }
            else if (clip != null && t.isInstance(clip)) out[i] = clip;
            else out[i] = null;
        }
        return out;
    }

    static void set(String text) throws Exception {
        Object svc = service();
        Class<?> clipData = Class.forName("android.content.ClipData");
        Object clip = clipData.getMethod("newPlainText", CharSequence.class, CharSequence.class).invoke(null, "pp", text);
        Method m = find(svc, "setPrimaryClip");
        Exception last = null;
        for (String pkg : PACKAGES) {
            try {
                m.invoke(svc, fill(m, clip, pkg));
                return;
            } catch (Exception e) {
                last = e;
            }
        }
        throw last;
    }

    static String get() throws Exception {
        Object svc = service();
        Method m = find(svc, "getPrimaryClip");
        Exception last = null;
        for (String pkg : PACKAGES) {
            try {
                Object clip = m.invoke(svc, fill(m, null, pkg));
                if (clip == null) return null;
                Class<?> clipData = Class.forName("android.content.ClipData");
                if ((int) clipData.getMethod("getItemCount").invoke(clip) == 0) return null;
                Object item = clipData.getMethod("getItemAt", int.class).invoke(clip, 0);
                Object cs = item.getClass().getMethod("getText").invoke(item);
                return cs == null ? null : cs.toString();
            } catch (Exception e) {
                last = e;
            }
        }
        throw last;
    }
}
