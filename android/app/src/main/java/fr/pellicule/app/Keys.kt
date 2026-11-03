package fr.pellicule.app

import android.content.Context
import android.content.pm.PackageManager
import android.net.http.SslCertificate
import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.io.ByteArrayInputStream
import java.math.BigInteger
import java.net.Socket
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.MessageDigest
import java.security.Principal
import java.security.PrivateKey
import java.security.cert.CertificateException
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.util.Date
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLEngine
import javax.net.ssl.SSLSocketFactory
import javax.net.ssl.X509ExtendedKeyManager
import javax.net.ssl.X509ExtendedTrustManager
import javax.security.auth.x500.X500Principal

/**
 * Clés de l'appareil, toutes dans le Keystore matériel d'Android (StrongBox si le téléphone en a une) :
 *  - « device » : paire EC P-256 dont la clé privée ne peut pas sortir de la puce. Elle signe la poignée
 *    de main TLS (certificat client) : c'est ainsi que le serveur reconnaît ce téléphone appairé.
 *  - « mk-wrap » : clé AES qui chiffre la clé maître du compte, stockée chiffrée dans les préférences.
 * Le certificat du serveur est épinglé (empreinte SHA-256 lue dans le QR code d'appairage).
 */
object Keys {
    private const val STORE = "AndroidKeyStore"
    private const val DEVICE = "pellicule-device"
    private const val MK_WRAP = "pellicule-mk-wrap"

    /** Même nom que DeviceCertName côté serveur (seul ce nom est annoncé dans la demande de certificat). */
    private val DEVICE_NAME = X500Principal("CN=Pellicule appareil, O=Pellicule")

    private fun store() = KeyStore.getInstance(STORE).apply { load(null) }

    private fun hasStrongBox(ctx: Context) =
        Build.VERSION.SDK_INT >= 28 && ctx.packageManager.hasSystemFeature(PackageManager.FEATURE_STRONGBOX_KEYSTORE)

    /** Crée la paire de clés de l'appareil (remplace l'ancienne : un réappairage = une nouvelle identité). */
    fun createDeviceKey(ctx: Context) {
        store().deleteEntry(DEVICE)
        fun spec(strongBox: Boolean) = KeyGenParameterSpec.Builder(DEVICE, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256, KeyProperties.DIGEST_SHA384, KeyProperties.DIGEST_SHA512, KeyProperties.DIGEST_NONE)
            .setCertificateSubject(DEVICE_NAME)
            .setCertificateSerialNumber(BigInteger(64, java.security.SecureRandom()))
            .setCertificateNotBefore(Date(System.currentTimeMillis() - 86_400_000L))
            .setCertificateNotAfter(Date(System.currentTimeMillis() + 20L * 365 * 86_400_000L))
            .apply { if (strongBox && Build.VERSION.SDK_INT >= 28) setIsStrongBoxBacked(true) }
            .build()
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, STORE)
        val strongBox = hasStrongBox(ctx)
        try {
            gen.initialize(spec(strongBox))
            gen.generateKeyPair()
        } catch (e: Exception) {
            if (!strongBox) throw e
            gen.initialize(spec(false)) // StrongBox annoncée mais refusée (P-256 non géré…) : TEE classique
            gen.generateKeyPair()
        }
    }

    fun hasDeviceKey() = store().containsAlias(DEVICE)

    private fun devicePrivateKey(): PrivateKey = store().getKey(DEVICE, null) as PrivateKey
    private fun deviceChain(): Array<X509Certificate> = arrayOf(store().getCertificate(DEVICE) as X509Certificate)

    /** Pour la WebView (onReceivedClientCertRequest). */
    fun clientCredentials(): Pair<PrivateKey, Array<X509Certificate>> = devicePrivateKey() to deviceChain()

    // --- clé maître du compte, chiffrée par une clé AES du Keystore ---

    private fun mkWrapKey(ctx: Context): SecretKey {
        (store().getKey(MK_WRAP, null) as? SecretKey)?.let { return it }
        fun spec(strongBox: Boolean) = KeyGenParameterSpec.Builder(MK_WRAP, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .apply { if (strongBox && Build.VERSION.SDK_INT >= 28) setIsStrongBoxBacked(true) }
            .build()
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, STORE)
        return try {
            gen.init(spec(hasStrongBox(ctx))); gen.generateKey()
        } catch (e: Exception) {
            gen.init(spec(false)); gen.generateKey()
        }
    }

    fun saveMasterKey(ctx: Context, mk: ByteArray) {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, mkWrapKey(ctx))
        c.updateAAD(Crypto.L.MK.toByteArray())
        Prefs(ctx).wrappedMK = Crypto.b64(c.iv + c.doFinal(mk))
    }

    /** Clé maître en clair, le temps d'un usage ; l'appelant l'efface (fill(0)) dès que possible. */
    fun masterKey(ctx: Context): ByteArray? {
        val box = Prefs(ctx).wrappedMK.takeIf { it.isNotEmpty() }?.let(Crypto::unb64) ?: return null
        val key = store().getKey(MK_WRAP, null) as? SecretKey ?: return null
        return runCatching {
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, box, 0, 12))
            c.updateAAD(Crypto.L.MK.toByteArray())
            c.doFinal(box, 12, box.size - 12)
        }.getOrNull()
    }

    /** Oubli complet : clés de l'appareil et clé maître. */
    fun forget(ctx: Context) {
        runCatching { store().deleteEntry(DEVICE) }
        runCatching { store().deleteEntry(MK_WRAP) }
        Prefs(ctx).wrappedMK = ""
    }

    // --- TLS : certificat serveur épinglé + certificat client de l'appareil ---

    fun fingerprint(der: ByteArray): String = Crypto.toHex(MessageDigest.getInstance("SHA-256").digest(der))

    fun normFingerprint(s: String) = s.lowercase().filter { it in "0123456789abcdef" }

    /** Empreinte d'un certificat présenté à la WebView. */
    fun fingerprint(cert: SslCertificate): String? {
        val der = if (Build.VERSION.SDK_INT >= 29) cert.x509Certificate?.encoded
        else SslCertificate.saveState(cert)?.getByteArray("x509-certificate")
        return der?.let(::fingerprint)
    }

    /** N'accepte que le certificat dont l'empreinte est épinglée. Aucune autorité de certification n'entre en jeu. */
    class PinnedTrust(private val pin: String) : X509ExtendedTrustManager() {
        var seen: String? = null // empreinte réellement présentée (affichée si elle ne correspond pas)
        private fun check(chain: Array<out X509Certificate>?) {
            val leaf = chain?.firstOrNull() ?: throw CertificateException("aucun certificat")
            seen = fingerprint(leaf.encoded)
            if (pin.isEmpty() || seen != pin) throw CertificateException("certificat du serveur inattendu")
        }
        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = check(chain)
        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = check(chain)
        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = check(chain)
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) = throw CertificateException()
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = throw CertificateException()
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = throw CertificateException()
        override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
    }

    /** Présente toujours la clé de l'appareil (si elle existe) quand le serveur demande un certificat client. */
    private class DeviceKeyManager : X509ExtendedKeyManager() {
        override fun chooseClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, socket: Socket?) =
            if (hasDeviceKey()) DEVICE else null
        override fun chooseEngineClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, engine: SSLEngine?) =
            if (hasDeviceKey()) DEVICE else null
        override fun getCertificateChain(alias: String?) = if (alias == DEVICE) deviceChain() else null
        override fun getPrivateKey(alias: String?) = if (alias == DEVICE) devicePrivateKey() else null
        override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?) = if (hasDeviceKey()) arrayOf(DEVICE) else null
        override fun chooseServerAlias(keyType: String?, issuers: Array<out Principal>?, socket: Socket?) = null
        override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?) = null
    }

    fun socketFactory(trust: PinnedTrust): SSLSocketFactory =
        SSLContext.getInstance("TLS").apply { init(arrayOf(DeviceKeyManager()), arrayOf(trust), null) }.socketFactory

    fun parseCert(der: ByteArray) =
        CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(der)) as X509Certificate
}
