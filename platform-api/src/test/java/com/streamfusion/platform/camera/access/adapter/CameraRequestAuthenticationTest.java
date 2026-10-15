package com.streamfusion.platform.camera.access.adapter;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;

/** Deterministic authentication vectors; all addresses and credentials are synthetic. */
class CameraRequestAuthenticationTest {
    private static final String RTSP_TARGET =
            "rtsp://camera.example:554/live%2Fmain?token=a%2Bb&x=1";
    private static final String CHALLENGE =
            "Digest realm=\"fixture-realm\", nonce=\"fixture-nonce\"";

    @ParameterizedTest
    @CsvSource({
        "MD5, false, 75e816ea02713afdec36bdbb82ade97f",
        "MD5, true, ef39e210957da910c9cb0d041e724566",
        "MD5-SESS, false, a98c3089a17fdf4ebe339be907a0e8c5",
        "SHA-256, true, 546290d14ade34ad21abc2a4003d040e72bf7ed2629793cca6b73f21cac3067d",
        "SHA-256-SESS, true, f931c5dc3f597c85bd71c30f3722109b1af631f99e48e6820ee1ee30c4529941"
    })
    void digestUsesTheExactAbsoluteRtspRequestTarget(
            String algorithm, boolean qop, String expectedResponse) {
        // Expected values were calculated independently with .NET MD5/SHA256, not this helper.
        String header =
                CHALLENGE
                        + ", algorithm="
                        + algorithm
                        + (qop ? ", qop=\"auth\"" : "")
                        + ", opaque=\"fixture-opaque\"";
        String value =
                CameraRequestAuthentication.challenge(
                        header,
                        "DESCRIBE",
                        RTSP_TARGET,
                        "fixture-user",
                        "fixture-password",
                        "fixture-cnonce");

        assertThat(value)
                .contains(
                        "uri=\"" + RTSP_TARGET + "\"",
                        "response=\"" + expectedResponse + "\"",
                        "algorithm=" + algorithm,
                        "opaque=\"fixture-opaque\"")
                .doesNotContain("fixture-password");
        if (qop) {
            assertThat(value).contains("qop=auth, nc=00000001, cnonce=\"fixture-cnonce\"");
        } else {
            assertThat(value).doesNotContain("qop=", "nc=");
            if (algorithm.endsWith("-SESS")) {
                assertThat(value).contains("cnonce=\"fixture-cnonce\"");
            } else {
                assertThat(value).doesNotContain("cnonce=");
            }
        }
    }

    @Test
    void httpCallerKeepsRawPathAndQueryWithoutTheAuthority() {
        URI uri = URI.create("http://camera.example/live%2Fmain?token=a%2Bb&x=1");
        String requestTarget =
                uri.getRawPath() + (uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery());
        String value =
                CameraRequestAuthentication.challenge(
                        CHALLENGE + ", qop=\"auth\"",
                        "GET",
                        requestTarget,
                        "fixture-user",
                        "fixture-password",
                        "fixture-cnonce");

        assertThat(value)
                .contains(
                        "uri=\"/live%2Fmain?token=a%2Bb&x=1\"",
                        "response=\"2c4598d373da1b062e9ae1dd4529710a\"", "algorithm=MD5")
                .doesNotContain("camera.example", "http://");
    }

    @Test
    void digestRetainsExplicitUtf8Charset() {
        String value =
                CameraRequestAuthentication.challenge(
                        CHALLENGE + ", algorithm=SHA-256, qop=\"auth\", charset=UTF-8",
                        "DESCRIBE",
                        RTSP_TARGET,
                        "使用者",
                        "测试密码",
                        "fixture-cnonce");

        assertThat(value)
                .contains(
                        "response=\"2bea2f8337a2fbaf43ee9eff7aa4873ca72ff89221feb66cabb20a5f91e5ec76\"",
                        "username=\"使用者\"")
                .doesNotContain("测试密码");
    }

    @Test
    void digestRetainsLatin1WhenCharsetIsNotSpecified() {
        String value =
                CameraRequestAuthentication.challenge(
                        CHALLENGE + ", algorithm=SHA-256, qop=\"auth\"",
                        "DESCRIBE",
                        RTSP_TARGET,
                        "usér",
                        "päss",
                        "fixture-cnonce");

        assertThat(value)
                .contains(
                        "response=\"f769f545b52c2c0beb7ff96e1f419955201df98879219c1352bc21589ded22b3\"",
                        "username=\"usér\"")
                .doesNotContain("päss");
    }

    @Test
    void publicEntryPointUsesRandomCnonce() {
        String header = CHALLENGE + ", qop=\"auth\"";
        String first =
                CameraRequestAuthentication.challenge(
                        header, "DESCRIBE", RTSP_TARGET, "fixture-user", "fixture-password");
        String second =
                CameraRequestAuthentication.challenge(
                        header, "DESCRIBE", RTSP_TARGET, "fixture-user", "fixture-password");

        assertThat(first).containsPattern("cnonce=\"[0-9a-f]{32}\"");
        assertThat(second).containsPattern("cnonce=\"[0-9a-f]{32}\"").isNotEqualTo(first);
    }

    @Test
    void basicRetainsUtf8Encoding() {
        assertThat(
                        CameraRequestAuthentication.challenge(
                                "Basic realm=\"fixture-realm\"",
                                "DESCRIBE",
                                RTSP_TARGET,
                                "usér",
                                "päss"))
                .isEqualTo("Basic dXPDqXI6cMOkc3M=");
    }

    @Test
    void basicRejectsColonInUsernameWithFixedDiagnostic() {
        assertThatThrownBy(
                        () ->
                                CameraRequestAuthentication.challenge(
                                        "Basic realm=\"fixture-realm\"",
                                        "DESCRIBE",
                                        RTSP_TARGET,
                                        "fixture:user",
                                        "fixture-private-password"))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage("UNSUPPORTED_AUTHENTICATION");
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
                "Digest realm=\"fixture-private-realm\", nonce=\"fixture-nonce\", algorithm=SHA-512",
                "Digest realm=\"fixture-private-realm\", nonce=\"fixture-nonce\", qop=\"auth-int\"",
                "Digest realm=\"fixture-private-realm\"",
                "Bearer fixture-private-token"
            })
    void unsupportedChallengesExposeOnlyFixedDiagnostic(String header) {
        assertThatThrownBy(
                        () ->
                                CameraRequestAuthentication.challenge(
                                        header,
                                        "DESCRIBE",
                                        RTSP_TARGET,
                                        "fixture-user",
                                        "fixture-private-password"))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage("UNSUPPORTED_AUTHENTICATION");
    }

    @Test
    void controlCharactersAreNotCopiedIntoDigestHeaderOrDiagnostic() {
        assertThatThrownBy(
                        () ->
                                CameraRequestAuthentication.challenge(
                                        CHALLENGE,
                                        "DESCRIBE",
                                        RTSP_TARGET + "\r\nfixture-private-value",
                                        "fixture-user",
                                        "fixture-private-password",
                                        "fixture-cnonce"))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage("UNSUPPORTED_AUTHENTICATION");
    }

    @Test
    void nullTargetHasFixedDiagnostic() {
        assertThatThrownBy(
                        () ->
                                CameraRequestAuthentication.challenge(
                                        CHALLENGE,
                                        "DESCRIBE",
                                        null,
                                        "fixture-user",
                                        "fixture-private-password"))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage("UNSUPPORTED_AUTHENTICATION");
    }

    @Test
    void hashRetainsStableCatalogIdentifierVector() {
        assertThat(CameraRequestAuthentication.hash("SHA-256", "abc", StandardCharsets.UTF_8))
                .isEqualTo("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    }
}
