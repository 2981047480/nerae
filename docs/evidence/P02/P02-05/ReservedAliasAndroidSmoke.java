import java.lang.reflect.Method;

public final class ReservedAliasAndroidSmoke {
    public static void main(String[] args) throws Exception {
        Class<?> bridge = Class.forName("uts.sdk.modules.neraeDeviceKey.NeraeAndroidDeviceKeyBridge");
        Object instance = bridge.getField("INSTANCE").get(null);
        Method validAlias = bridge.getDeclaredMethod("validAlias", String.class);
        validAlias.setAccessible(true);

        check(validAlias, instance, "A".repeat(43), true);
        check(validAlias, instance, "A".repeat(42) + "Q", true);
        check(validAlias, instance, "A".repeat(42) + "B", false);
        check(validAlias, instance, "A".repeat(42) + "+", false);
        check(validAlias, instance, "A".repeat(42), false);
        System.out.println("PASS: Android rejects invalid and non-canonical reserved aliases");
    }

    private static void check(Method method, Object receiver, String alias, boolean expected) throws Exception {
        boolean actual = (Boolean) method.invoke(receiver, alias);
        if (actual != expected) throw new AssertionError("alias validation mismatch");
    }
}
