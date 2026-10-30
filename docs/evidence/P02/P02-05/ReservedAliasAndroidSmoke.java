import java.lang.reflect.Method;
import java.util.Base64;

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
        // Every possible final byte of a 32-byte key produces a legal alias.
        // This catches an erroneously narrow subset of canonical tail chars.
        for (int i = 0; i < 256; i++) {
            byte[] bytes = new byte[32];
            bytes[31] = (byte) i;
            check(validAlias, instance, Base64.getUrlEncoder().withoutPadding().encodeToString(bytes), true);
        }
        String alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
        for (int i = 0; i < alphabet.length(); i++) {
            String candidate = "A".repeat(42) + alphabet.charAt(i);
            byte[] decoded = Base64.getUrlDecoder().decode(candidate);
            boolean canonical = candidate.equals(Base64.getUrlEncoder().withoutPadding().encodeToString(decoded));
            check(validAlias, instance, candidate, canonical);
        }
        System.out.println("PASS: Android accepts every canonical 32-byte alias and rejects non-canonical aliases");
    }

    private static void check(Method method, Object receiver, String alias, boolean expected) throws Exception {
        boolean actual = (Boolean) method.invoke(receiver, alias);
        if (actual != expected) throw new AssertionError("alias validation mismatch");
    }
}
