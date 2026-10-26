package com.streamfusion.platform.camera.ingress;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.*;
import java.util.regex.Pattern;

/** GB28181 SIP Digest MD5; nonces are short-lived, endpoint-bound and consumed by the server. */
final class GbDigest {
    private static final Pattern FIELD =
            Pattern.compile("([a-zA-Z]+)=(?:\"([^\"]*)\"|([^, ]+))(?:, *|$)");

    private GbDigest() {}

    static boolean verify(
            String header,
            String username,
            String password,
            String realm,
            String nonce,
            String requestUri) {
        if (!header.startsWith("Digest ") || header.length() > 2048) return false;
        Map<String, String> fields = new HashMap<>();
        var matcher = FIELD.matcher(header.substring(7));
        int end = 0;
        while (matcher.find()) {
            if (matcher.start() != end
                    || fields.put(
                                    matcher.group(1),
                                    matcher.group(2) == null ? matcher.group(3) : matcher.group(2))
                            != null) return false;
            end = matcher.end();
        }
        if (end != header.length() - 7
                || !username.equals(fields.get("username"))
                || !realm.equals(fields.get("realm"))
                || !nonce.equals(fields.get("nonce"))
                || !requestUri.equals(fields.get("uri"))
                || !"MD5".equalsIgnoreCase(fields.getOrDefault("algorithm", "MD5"))) return false;
        String ha1 = md5(username + ":" + realm + ":" + password),
                ha2 = md5("REGISTER:" + requestUri);
        String expected;
        if (fields.containsKey("qop")) {
            if (!"auth".equals(fields.get("qop"))
                    || !fields.getOrDefault("nc", "").matches("[0-9a-fA-F]{8}")
                    || !fields.getOrDefault("cnonce", "").matches("[A-Za-z0-9_-]{1,128}"))
                return false;
            expected =
                    md5(
                            ha1
                                    + ":"
                                    + nonce
                                    + ":"
                                    + fields.get("nc")
                                    + ":"
                                    + fields.get("cnonce")
                                    + ":auth:"
                                    + ha2);
        } else expected = md5(ha1 + ":" + nonce + ":" + ha2);
        return MessageDigest.isEqual(
                expected.getBytes(StandardCharsets.US_ASCII),
                fields.getOrDefault("response", "")
                        .toLowerCase(Locale.ROOT)
                        .getBytes(StandardCharsets.US_ASCII));
    }

    static String md5(String value) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance("MD5")
                                    .digest(value.getBytes(StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException failure) {
            throw new IllegalStateException("SIP_DIGEST_UNAVAILABLE");
        }
    }
}
