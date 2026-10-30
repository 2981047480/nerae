package uts.sdk.modules.neraeRandom

fun main() {
    val zeroBytes = ByteArray(32)
    check(neraeBase64Url(zeroBytes) == "A".repeat(43))

    val vector = ByteArray(32) { it.toByte() }
    check(neraeBase64Url(vector) == "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")

    val generated = NeraeAndroidSecureRandomBridge.generateRequestKey()
    check(Regex("^[A-Za-z0-9_-]{43}$").matches(generated))
    println("PASS: Android bridge returned a 43-character request key; value redacted. Deterministic vectors test encoding only.")
}
