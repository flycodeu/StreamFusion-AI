package com.streamfusion.platform.camera.access.adapter;

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

/**
 * Basic/Digest credentials for the exact request target supplied by the protocol caller. The target
 * is not parsed or normalized here; HTTP supplies a raw path/query and RTSP an absolute URI without
 * userinfo. The returned header is sensitive and must never be logged.
 */
public final class CameraRequestAuthentication {
    private static final SecureRandom RANDOM = new SecureRandom();
    private static final Pattern PARAMETER =
            Pattern.compile(
                    "([A-Za-z][A-Za-z0-9_-]*)\\s*=\\s*(?:\"((?:\\\\.|[^\"\\\\])*)\"|([^,\\s]+))");

    private CameraRequestAuthentication() {}

    public static String challenge(
            String header, String method, String requestTarget, String username, String password) {
        byte[] random = new byte[16];
        RANDOM.nextBytes(random);
        return challenge(
                header,
                method,
                requestTarget,
                username,
                password,
                HexFormat.of().formatHex(random));
    }

    static String challenge(
            String header,
            String method,
            String requestTarget,
            String username,
            String password,
            String cnonce) {
        if (header == null || username == null || password == null)
            throw new CameraAdapterException("AUTHENTICATION_FAILED");
        if (method == null || requestTarget == null || cnonce == null)
            throw new CameraAdapterException("UNSUPPORTED_AUTHENTICATION");
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
        String a1 = hash(digest, username + ":" + realm + ":" + password, charset);
        if (algorithm.toUpperCase(Locale.ROOT).endsWith("-SESS"))
            a1 = hash(digest, a1 + ":" + nonce + ":" + cnonce, charset);
        String a2 = hash(digest, method + ":" + requestTarget, charset);
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
                        + quoted(requestTarget)
                        + ", response="
                        + quoted(response);
        // An omitted algorithm already means MD5. Preserve legacy camera challenges instead of
        // adding an optional parameter they did not advertise.
        if (parameters.containsKey("algorithm")) value += ", algorithm=" + algorithm;
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
