package com.streamfusion.platform.storage.s3;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.TreeMap;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/** AWS Signature V4, path-style requests. No presigned URLs or credential-bearing diagnostics. */
public final class S3Signature {
    private static final DateTimeFormatter DATE =
            DateTimeFormatter.ofPattern("yyyyMMdd").withZone(ZoneOffset.UTC);
    private static final DateTimeFormatter TIME =
            DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'").withZone(ZoneOffset.UTC);

    private S3Signature() {}

    public static String encode(String value, boolean path) {
        var out = new StringBuilder();
        for (byte item : value.getBytes(StandardCharsets.UTF_8)) {
            int b = item & 255;
            if (b >= 'a' && b <= 'z'
                    || b >= 'A' && b <= 'Z'
                    || b >= '0' && b <= '9'
                    || "-_.~".indexOf(b) >= 0
                    || path && b == '/') out.append((char) b);
            else
                out.append('%')
                        .append("0123456789ABCDEF".charAt(b >>> 4))
                        .append("0123456789ABCDEF".charAt(b & 15));
        }
        return out.toString();
    }

    public static String query(Map<String, String> query) {
        var sorted = new TreeMap<String, String>();
        query.forEach((key, value) -> sorted.put(encode(key, false), encode(value, false)));
        return sorted.entrySet().stream()
                .map(e -> e.getKey() + "=" + e.getValue())
                .collect(java.util.stream.Collectors.joining("&"));
    }

    public static Map<String, String> sign(
            String method,
            String path,
            String query,
            String host,
            String region,
            String access,
            String secret,
            byte[] body,
            Map<String, String> extra,
            Instant now) {
        String date = DATE.format(now), time = TIME.format(now), payload = hash(body);
        var headers = new TreeMap<String, String>();
        headers.put("host", host);
        headers.put("x-amz-content-sha256", payload);
        headers.put("x-amz-date", time);
        extra.forEach(
                (key, value) ->
                        headers.put(
                                key.toLowerCase(java.util.Locale.ROOT),
                                value.strip().replaceAll("[ \\t]+", " ")));
        String names = String.join(";", headers.keySet());
        String canonical =
                method
                        + "\n"
                        + path
                        + "\n"
                        + query
                        + "\n"
                        + headers.entrySet().stream()
                                .map(e -> e.getKey() + ":" + e.getValue() + "\n")
                                .collect(java.util.stream.Collectors.joining())
                        + "\n"
                        + names
                        + "\n"
                        + payload;
        String scope = date + "/" + region + "/s3/aws4_request";
        byte[] key = hmac(("AWS4" + secret).getBytes(StandardCharsets.UTF_8), date);
        key = hmac(key, region);
        key = hmac(key, "s3");
        key = hmac(key, "aws4_request");
        var result = new LinkedHashMap<>(headers);
        result.put(
                "authorization",
                "AWS4-HMAC-SHA256 Credential="
                        + access
                        + "/"
                        + scope
                        + ", SignedHeaders="
                        + names
                        + ", Signature="
                        + HexFormat.of()
                                .formatHex(
                                        hmac(
                                                key,
                                                "AWS4-HMAC-SHA256\n"
                                                        + time
                                                        + "\n"
                                                        + scope
                                                        + "\n"
                                                        + hash(
                                                                canonical.getBytes(
                                                                        StandardCharsets.UTF_8)))));
        java.util.Arrays.fill(key, (byte) 0);
        return result;
    }

    /** Creates the bounded query signature used for a single immutable inspection PUT. */
    public static String presignedUrl(
            String endpoint,
            String bucket,
            String key,
            String region,
            String access,
            String secret,
            int expiresSeconds,
            Map<String, String> headers,
            Instant now) {
        if (expiresSeconds < 1 || expiresSeconds > 900) throw new IllegalArgumentException();
        String authority = java.net.URI.create(endpoint).getRawAuthority();
        String path = "/" + encode(bucket, false) + "/" + encode(key, true);
        String date = DATE.format(now), time = TIME.format(now);
        var signedHeaders = new TreeMap<String, String>();
        signedHeaders.put("host", authority);
        headers.forEach(
                (name, value) -> signedHeaders.put(name.toLowerCase(java.util.Locale.ROOT), value));
        String names = String.join(";", signedHeaders.keySet());
        String scope = date + "/" + region + "/s3/aws4_request";
        var query = new TreeMap<String, String>();
        query.put("X-Amz-Algorithm", "AWS4-HMAC-SHA256");
        query.put("X-Amz-Credential", access + "/" + scope);
        query.put("X-Amz-Date", time);
        query.put("X-Amz-Expires", Integer.toString(expiresSeconds));
        query.put("X-Amz-SignedHeaders", names);
        String canonicalQuery = query(query);
        String canonicalHeaders =
                signedHeaders.entrySet().stream()
                        .map(
                                e ->
                                        e.getKey()
                                                + ":"
                                                + e.getValue().strip().replaceAll("[ \t]+", " ")
                                                + "\n")
                        .collect(java.util.stream.Collectors.joining());
        String canonical =
                "PUT\n"
                        + path
                        + "\n"
                        + canonicalQuery
                        + "\n"
                        + canonicalHeaders
                        + "\n"
                        + names
                        + "\nUNSIGNED-PAYLOAD";
        byte[] signingKey = hmac(("AWS4" + secret).getBytes(StandardCharsets.UTF_8), date);
        signingKey = hmac(signingKey, region);
        signingKey = hmac(signingKey, "s3");
        signingKey = hmac(signingKey, "aws4_request");
        String signature =
                HexFormat.of()
                        .formatHex(
                                hmac(
                                        signingKey,
                                        "AWS4-HMAC-SHA256\n"
                                                + time
                                                + "\n"
                                                + scope
                                                + "\n"
                                                + hash(
                                                        canonical.getBytes(
                                                                StandardCharsets.UTF_8))));
        java.util.Arrays.fill(signingKey, (byte) 0);
        query.put("X-Amz-Signature", signature);
        String encoded = query(query);
        return endpoint + path + "?" + encoded;
    }

    public static String hash(byte[] value) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(value));
        } catch (java.security.GeneralSecurityException ex) {
            throw new IllegalStateException("SHA256 unavailable");
        }
    }

    private static byte[] hmac(byte[] key, String value) {
        try {
            var mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key, "HmacSHA256"));
            return mac.doFinal(value.getBytes(StandardCharsets.UTF_8));
        } catch (java.security.GeneralSecurityException ex) {
            throw new IllegalStateException("HMAC unavailable");
        }
    }
}
