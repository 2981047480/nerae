import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.security.Signature;

/** Exercises the production DER-to-JOSE converter with process-local test keys only. */
public final class DerSmoke {
    private static byte[] derInteger(byte[] raw, int start) {
        int first = start;
        while (first < start + 31 && raw[first] == 0) first++;
        boolean needsSignPadding = (raw[first] & 0x80) != 0;
        int valueLength = start + 32 - first;
        byte[] encoded = new byte[valueLength + (needsSignPadding ? 1 : 0) + 2];
        encoded[0] = 0x02;
        encoded[1] = (byte) (encoded.length - 2);
        System.arraycopy(raw, first, encoded, 2 + (needsSignPadding ? 1 : 0), valueLength);
        return encoded;
    }

    private static byte[] joseToDer(byte[] raw) {
        byte[] r = derInteger(raw, 0);
        byte[] s = derInteger(raw, 32);
        byte[] encoded = new byte[2 + r.length + s.length];
        encoded[0] = 0x30;
        encoded[1] = (byte) (r.length + s.length);
        System.arraycopy(r, 0, encoded, 2, r.length);
        System.arraycopy(s, 0, encoded, 2 + r.length, s.length);
        return encoded;
    }

    private static boolean rejects(Method converter, Object bridge, byte[] der) throws Exception {
        try {
            converter.invoke(bridge, (Object) der);
            return false;
        } catch (InvocationTargetException expected) {
            return true;
        }
    }

    private static boolean verifies(KeyPair pair, byte[] message, byte[] der) throws Exception {
        Signature verifier = Signature.getInstance("SHA256withECDSA");
        verifier.initVerify(pair.getPublic());
        verifier.update(message);
        return verifier.verify(der);
    }

    public static void main(String[] args) throws Exception {
        Class<?> bridgeClass = Class.forName("uts.sdk.modules.neraeDeviceKey.NeraeAndroidDeviceKeyBridge");
        Object bridge = bridgeClass.getField("INSTANCE").get(null);
        Method converter = bridgeClass.getDeclaredMethod("derToJose", byte[].class);
        converter.setAccessible(true);

        KeyPairGenerator generator = KeyPairGenerator.getInstance("EC");
        generator.initialize(256);
        KeyPair pair = generator.generateKeyPair();
        byte[] message = "synthetic DER conversion check".getBytes(java.nio.charset.StandardCharsets.UTF_8);
        Signature signer = Signature.getInstance("SHA256withECDSA");
        signer.initSign(pair.getPrivate());
        signer.update(message);
        byte[] der = signer.sign();

        byte[] jose = (byte[]) converter.invoke(bridge, (Object) der);
        if (jose.length != 64 || !verifies(pair, message, joseToDer(jose))) {
            throw new AssertionError("valid signature did not round-trip through the production converter");
        }

        byte[][] malformed = {
            {0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01},
            {0x30, 0x06, 0x02, 0x01, (byte) 0x80, 0x02, 0x01, 0x01},
            {0x30, 0x07, 0x02, 0x02, 0x00, 0x01, 0x02, 0x01, 0x01},
            {0x30, 0x06, 0x02, 0x01, 0x00, 0x02, 0x01, 0x01},
            {0x30, (byte) 0x81, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x01}
        };
        for (byte[] input : malformed) {
            if (!rejects(converter, bridge, input)) throw new AssertionError("malformed DER was accepted");
        }

        byte[] changed = jose.clone();
        changed[0] ^= 1;
        if (verifies(pair, message, joseToDer(changed))) throw new AssertionError("changed signature verified");
        System.out.println("PASS: valid synthetic signature round-trips; five malformed DER inputs and one changed signature are rejected.");
    }
}
