package com.streamfusion.platform.storage;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Base64;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Tokens bind an immutable selection; every use still requires current module authorization. */
@Component
@RequiredArgsConstructor
public class StorageTokens {
    private final StorageSecrets secrets;
    private final StorageJson json;

    public String create(String kind, long actor, Map<String, ?> values, int seconds) {
        var body =
                Map.of(
                        "kind",
                        kind,
                        "actor",
                        Long.toString(actor),
                        "expires",
                        Instant.now().plusSeconds(seconds).getEpochSecond(),
                        "values",
                        values);
        return Base64.getUrlEncoder()
                        .withoutPadding()
                        .encodeToString(json.write(body).getBytes(StandardCharsets.UTF_8))
                + "."
                + secrets.sign(body);
    }

    public JsonNode read(String token, String kind, long actor) {
        try {
            if (token == null || token.length() > 16000) throw new IllegalArgumentException();
            int split = token.indexOf('.');
            JsonNode body =
                    json.read(
                            new String(
                                    Base64.getUrlDecoder().decode(token.substring(0, split)),
                                    StandardCharsets.UTF_8));
            if (!secrets.verify(body, token.substring(split + 1))
                    || !kind.equals(body.path("kind").asText())
                    || !Long.toString(actor).equals(body.path("actor").asText())
                    || Instant.now().getEpochSecond() >= body.path("expires").asLong())
                throw new IllegalArgumentException();
            return body.path("values");
        } catch (RuntimeException ex) {
            throw BusinessException.error(ErrorCode.STORAGE_OBJECT_CHANGED);
        }
    }
}
