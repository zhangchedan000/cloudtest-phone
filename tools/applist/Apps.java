// 云手机「应用列表」小工具：在容器里用 app_process 运行（不装 App），列出有桌面图标的应用——
// 包名、显示名字、系统/用户、以及图标 PNG。管家程序拿它渲染成魔云腾那样的「图标 + 名字」网格。
//
//   CLASSPATH=/data/local/tmp/pp-apps.dex app_process / Apps            → 只出包名+名字（快）
//   CLASSPATH=/data/local/tmp/pp-apps.dex app_process / Apps icon=96    → 连 96px 图标 PNG 一起出
//
// 每行一个应用，制表符分隔：  包名 \t 启动Activity \t 系统?(0/1) \t base64(名字UTF-8) \t base64(图标PNG，没有就空)
// 最后一行打印  ok <数量>  表示成功。启动Activity 给管家用 `am start -n 包名/Activity` 打开，不用 monkey（monkey 也是 app_process）。
//
// 做法（参考 scrcpy Workarounds）：安卓的 PackageManager 要有个「App 身份的 Context」才能用；
// app_process 起来是裸进程，没有 Context。于是反射造一个 ActivityThread + system Context，
// 再包一层假的 shell 包名，就能拿到真正的 PackageManager：queryIntentActivities 列启动项、
// loadLabel 取名字、loadIcon 取图标。图标是 Drawable，用软件 Canvas 画进 Bitmap 再压成 PNG（不需要屏幕）。
//
// 只有 ActivityThread / AppBindData 这些是隐藏类，用反射；其余 Intent/PackageManager/Drawable/Bitmap
// 都是公开 API，编译时对着 android.jar（见 tools/applist/build.sh），运行时用容器里真正的框架。

import android.content.Context;
import android.content.ContextWrapper;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.drawable.Drawable;
import android.os.Looper;

import java.io.ByteArrayOutputStream;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.List;

public class Apps {
    // 假装成系统自带的 shell 包（和剪贴板小工具一样）：很多系统服务只认已知包名，shell 是最稳的。
    static final String PKG = "com.android.shell";

    // 一个「App 身份的 Context」：包住 system Context，报出 shell 包名，PackageManager 才肯干活。
    static final class Fake extends ContextWrapper {
        Fake(Context base) { super(base); }
        @Override public String getPackageName() { return PKG; }
        @Override public String getOpPackageName() { return PKG; }
        @Override public Context getApplicationContext() { return this; }
    }

    public static void main(String[] args) {
        int icon = 0;
        for (String a : args) {
            if (a.startsWith("icon=")) {
                try { icon = Integer.parseInt(a.substring(5)); } catch (Exception ignore) {}
            }
        }
        if (icon < 0) icon = 0;
        if (icon > 192) icon = 192; // 别太大，一屏几十个图标够用了
        try {
            Context sys = systemContext();
            if (sys == null) { System.out.println("error 拿不到系统 Context"); System.exit(1); return; }
            PackageManager pm = new Fake(sys).getPackageManager();
            if (pm == null) pm = sys.getPackageManager();

            Intent it = new Intent(Intent.ACTION_MAIN);
            it.addCategory(Intent.CATEGORY_LAUNCHER);
            List<ResolveInfo> ris = pm.queryIntentActivities(it, 0);

            StringBuilder sb = new StringBuilder();
            int n = 0;
            for (ResolveInfo ri : ris) {
                if (ri == null || ri.activityInfo == null) continue;
                String pkg = ri.activityInfo.packageName;
                if (pkg == null || pkg.isEmpty()) continue;
                String act = ri.activityInfo.name == null ? "" : ri.activityInfo.name;
                ApplicationInfo ai = ri.activityInfo.applicationInfo;

                String label;
                try {
                    CharSequence cs = ri.loadLabel(pm);
                    label = cs == null ? pkg : cs.toString();
                } catch (Throwable t) {
                    label = pkg;
                }

                boolean system = ai != null
                        && (ai.flags & (ApplicationInfo.FLAG_SYSTEM | ApplicationInfo.FLAG_UPDATED_SYSTEM_APP)) != 0;

                String png = "";
                if (icon > 0) {
                    try { png = iconPng(pm, ri, icon); } catch (Throwable t) { png = ""; }
                }

                sb.append(pkg).append('\t')
                  .append(act).append('\t')
                  .append(system ? '1' : '0').append('\t')
                  .append(b64(label.getBytes(StandardCharsets.UTF_8))).append('\t')
                  .append(png).append('\n');
                n++;
            }
            System.out.print(sb);
            System.out.println("ok " + n);
        } catch (Throwable t) {
            Throwable c = t.getCause() != null ? t.getCause() : t;
            System.out.println("error " + c);
            System.exit(1);
        }
    }

    // 把应用图标（可能是自适应图标 / 矢量图）用软件画布渲染成一张 size×size 的 PNG。
    static String iconPng(PackageManager pm, ResolveInfo ri, int size) throws Exception {
        Drawable d = ri.loadIcon(pm);
        if (d == null) return "";
        Bitmap bmp = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888);
        try {
            Canvas cv = new Canvas(bmp);
            d.setBounds(0, 0, size, size);
            d.draw(cv);
            ByteArrayOutputStream bos = new ByteArrayOutputStream();
            bmp.compress(Bitmap.CompressFormat.PNG, 100, bos);
            return b64(bos.toByteArray());
        } finally {
            bmp.recycle();
        }
    }

    static String b64(byte[] b) { return Base64.getEncoder().encodeToString(b); }

    // 造一个 ActivityThread + system Context。app_process 是裸进程，先备好主线程 Looper，
    // 再用 ActivityThread.systemMain()（它内部 new ActivityThread + attach(true)，比手工拼装稳），
    // 然后 getSystemContext()。出错就把真正的异常抛出去，main 里打成 "error <异常>" 方便定位。
    static Context systemContext() {
        try {
            try {
                if (Looper.myLooper() == null) Looper.prepareMainLooper();
            } catch (Throwable ignore) {}

            Class<?> at = Class.forName("android.app.ActivityThread");
            Object thread;
            try {
                Method systemMain = at.getDeclaredMethod("systemMain");
                systemMain.setAccessible(true);
                thread = systemMain.invoke(null);
            } catch (Throwable t) {
                // 退路：手工 new + attach(true)
                Constructor<?> ctor = at.getDeclaredConstructor();
                ctor.setAccessible(true);
                thread = ctor.newInstance();
                try { setStatic(at, "sCurrentActivityThread", thread); } catch (Throwable ignore) {}
                try {
                    Field f = at.getDeclaredField("mSystemThread");
                    f.setAccessible(true);
                    f.setBoolean(thread, true);
                } catch (Throwable ignore) {}
            }

            // thread.mBoundApplication = AppBindData{appInfo = ApplicationInfo{packageName = shell}};
            // 让 PackageManager 认得调用方是个「App」，取名字/图标更稳。
            try {
                Class<?> abd = Class.forName("android.app.ActivityThread$AppBindData");
                Constructor<?> abdC = abd.getDeclaredConstructor();
                abdC.setAccessible(true);
                Object bind = abdC.newInstance();
                ApplicationInfo ai = new ApplicationInfo();
                ai.packageName = PKG;
                Field appInfo = abd.getDeclaredField("appInfo");
                appInfo.setAccessible(true);
                appInfo.set(bind, ai);
                Field mBound = at.getDeclaredField("mBoundApplication");
                mBound.setAccessible(true);
                mBound.set(thread, bind);
            } catch (Throwable ignore) {}

            Method m = at.getDeclaredMethod("getSystemContext");
            m.setAccessible(true);
            return (Context) m.invoke(thread);
        } catch (Throwable t) {
            Throwable c = t.getCause() != null ? t.getCause() : t;
            throw new RuntimeException("systemContext: " + c, c);
        }
    }

    static void setStatic(Class<?> c, String name, Object val) throws Exception {
        Field f = c.getDeclaredField(name);
        f.setAccessible(true);
        f.set(null, val);
    }
}
