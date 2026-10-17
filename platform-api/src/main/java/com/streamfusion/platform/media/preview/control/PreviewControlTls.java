package com.streamfusion.platform.media.preview.control;

import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.security.MessageDigest;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;
import java.util.HexFormat;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;

/** A per-node certificate pin plus normal HTTPS hostname verification; no trust-all fallback. */
public final class PreviewControlTls {
    private PreviewControlTls() {}

    public static SSLContext client(String fingerprint) {
        if (fingerprint == null || !fingerprint.matches("[a-fA-F0-9]{64}"))
            throw new IllegalArgumentException("A SHA-256 node certificate pin is required");
        byte[] expected = HexFormat.of().parseHex(fingerprint);
        try {
            var context = SSLContext.getInstance("TLS");
            context.init(
                    null,
                    new TrustManager[] {
                        new X509TrustManager() {
                            public X509Certificate[] getAcceptedIssuers() {
                                return new X509Certificate[0];
                            }

                            public void checkClientTrusted(X509Certificate[] chain, String auth)
                                    throws CertificateException {
                                throw new CertificateException(
                                        "Client authentication is not supported here");
                            }

                            public void checkServerTrusted(X509Certificate[] chain, String auth)
                                    throws CertificateException {
                                try {
                                    if (chain == null || chain.length == 0)
                                        throw new CertificateException("Missing certificate");
                                    chain[0].checkValidity();
                                    if (!MessageDigest.isEqual(
                                            expected,
                                            MessageDigest.getInstance("SHA-256")
                                                    .digest(chain[0].getEncoded())))
                                        throw new CertificateException(
                                                "Node certificate identity mismatch");
                                } catch (java.security.NoSuchAlgorithmException ex) {
                                    throw new CertificateException(ex);
                                }
                            }
                        }
                    },
                    null);
            return context;
        } catch (java.security.GeneralSecurityException ex) {
            throw new IllegalStateException("TLS unavailable", ex);
        }
    }

    public static SSLContext server(Path storeFile, Path passwordFile) {
        char[] password = secret(passwordFile).toCharArray();
        try (var input = Files.newInputStream(storeFile)) {
            var store = KeyStore.getInstance("PKCS12");
            store.load(input, password);
            var keys = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
            keys.init(store, password);
            var context = SSLContext.getInstance("TLS");
            context.init(keys.getKeyManagers(), null, null);
            return context;
        } catch (Exception ex) {
            throw new IllegalStateException("Cannot load node TLS identity");
        } finally {
            java.util.Arrays.fill(password, '\0');
        }
    }

    public static String secret(Path file) {
        try {
            if (Files.size(file) > 512) throw new IllegalArgumentException();
            String value = Files.readString(file).strip();
            if (!value.matches("[A-Za-z0-9_+/=-]{32,256}")) throw new IllegalArgumentException();
            return value;
        } catch (Exception ex) {
            throw new IllegalStateException("Missing or invalid preview node secret file");
        }
    }
}
