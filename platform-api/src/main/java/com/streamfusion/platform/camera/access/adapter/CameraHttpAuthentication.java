package com.streamfusion.platform.camera.access.adapter;

import java.net.URI;
import java.nio.charset.Charset;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Pattern;

final class CameraHttpAuthentication {
    private static final SecureRandom RANDOM = new SecureRandom();
    private static final Pattern PARAMETER =
            Pattern.compile(
                    "([A-Za-z][A-Za-z0-9_-]*)\\s*=\\s*(?:\"((?:\\\\.|[^\"\\\\])*)\"|([^,\\s]+))");

    private CameraHttpAuthentication() {}

    static String challenge(
            String header, String method, URI uri, String username, String password) {
        byte[] random = new byte[16];
        RANDOM.nextBytes(random);
        return challenge(header, method, uri, username, password, HexFormat.of().formatHex(random));
    }

    static String challenge(
            String header,
            String method,
            URI uri,
            String username,
            String password,
            String cnonce) {
        if (header == null || username == null || password == null)
            throw new CameraAdapterException("AUTHENTICATION_FAILED");
        String lower = header.toLowerCase(Locale.ROOT);
        if (lower.startsWith("basic ")) {
            if (username.contains(":"))
                throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
            return "Basic "
                    + Base64.getEncoder()
                            .encodeToString(
                                    (username + ":" + password).getBytes(StandardCharsets.UTF_8));
        }
        if (!lower.startsWith("digest "))
            throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
        Map<String, String> parameters = new LinkedHashMap<>();
        var matcher = PARAMETER.matcher(header.substring(7));
        while (matcher.find()) {
            String value =
                    matcher.group(2) == null
                            ? matcher.group(3)
                            : matcher.group(2).replaceAll("\\\\(.)", "$1");
            parameters.put(matcher.group(1).toLowerCase(Locale.ROOT), value);
        }
        String realm = parameters.get("realm");
        String nonce = parameters.get("nonce");
        if (realm == null || nonce == null || nonce.isEmpty())
            throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
        String algorithm = parameters.getOrDefault("algorithm", "MD5");
        String digest =
                switch (algorithm.toUpperCase(Locale.ROOT)) {
                    case "MD5", "MD5-SESS" -> "MD5";
                    case "SHA-256", "SHA-256-SESS" -> "SHA-256";
                    default -> throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
                };
        String qop = parameters.get("qop");
        if (qop != null
                && java.util.Arrays.stream(qop.split(",")).noneMatch(v -> v.strip().equals("auth")))
            throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
        Charset charset =
                "UTF-8".equalsIgnoreCase(parameters.get("charset"))
                        ? StandardCharsets.UTF_8
                        : StandardCharsets.ISO_8859_1;
        String target =
                uri.getRawPath() + (uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery());
        String a1 = hash(digest, username + ":" + realm + ":" + password, charset);
        if (algorithm.toUpperCase(Locale.ROOT).endsWith("-SESS"))
            a1 = hash(digest, a1 + ":" + nonce + ":" + cnonce, charset);
        String a2 = hash(digest, method + ":" + target, charset);
        String response =
                hash(
                        digest,
                        a1
                                + ":"
                                + nonce
                                + ":"
                                + (qop == null ? "" : "00000001:" + cnonce + ":auth:")
                                + a2,
                        charset);
        String value =
                "Digest username="
                        + quoted(username)
                        + ", realm="
                        + quoted(realm)
                        + ", nonce="
                        + quoted(nonce)
                        + ", uri="
                        + quoted(target)
                        + ", response="
                        + quoted(response)
                        + ", algorithm="
                        + algorithm;
        if (qop != null) value += ", qop=auth, nc=00000001, cnonce=" + quoted(cnonce);
        else if (algorithm.toUpperCase(Locale.ROOT).endsWith("-SESS"))
            value += ", cnonce=" + quoted(cnonce);
        if (parameters.containsKey("opaque"))
            value += ", opaque=" + quoted(parameters.get("opaque"));
        return value;
    }

    private static String quoted(String value) {
        if (value.codePoints().anyMatch(Character::isISOControl))
            throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
        return "\"" + value.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    static String hash(String algorithm, String value, Charset charset) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance(algorithm).digest(value.getBytes(charset)));
        } catch (java.security.NoSuchAlgorithmException ex) {
            throw new IllegalStateException("Required digest unavailable");
        }
    }
}
