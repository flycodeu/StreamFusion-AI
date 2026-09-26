package com.streamfusion.platform.auth.guard;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.common.exception.BusinessException;
import java.time.Duration;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.MockHttpServletRequest;

class ClientIpResolverTest {
    @Test
    void ignoresUntrustedForwardingAndCanonicalizesIpv4MappedIpv6() {
        var resolver = resolver();
        var request = request("::ffff:192.0.2.10", "203.0.113.66");
        request.addHeader("Forwarded", "for=198.51.100.20");
        assertThat(resolver.resolve(request)).isEqualTo("192.0.2.10");
        assertThat(request.getAttribute(ClientIpResolver.ATTRIBUTE)).isEqualTo("192.0.2.10");
    }

    @Test
    void walksOnlyExplicitTrustedHopsFromRightAndDoesNotUseSpoofedLeftPrefix() {
        var resolver = resolver("127.0.0.1", "::1");
        assertThat(
                        resolver.resolve(
                                request("::ffff:127.0.0.1", "spoofed.example, 198.51.100.5, ::1")))
                .isEqualTo("198.51.100.5");
        assertThat(resolver.resolve(request("127.0.0.1", "2001:db8::1")))
                .isEqualTo("2001:db8:0:0:0:0:0:1");
        assertThat(resolver.resolve(request("127.0.0.1", "::1"))).isEqualTo("0:0:0:0:0:0:0:1");
    }

    @Test
    void rejectsAmbiguousOrNonLiteralTrustedHeadersWithoutDns() {
        var resolver = resolver("127.0.0.1");
        for (String header :
                List.of(
                        "host.example",
                        "",
                        "192.0.2.1:8080",
                        "127.1",
                        "0177.0.0.1",
                        "1.2.3.999",
                        "192.0.2.1,"))
            assertThatThrownBy(() -> resolver.resolve(request("127.0.0.1", header)))
                    .isInstanceOf(BusinessException.class);
        var duplicated = request("127.0.0.1", "192.0.2.1");
        duplicated.addHeader("X-Forwarded-For", "192.0.2.2");
        assertThatThrownBy(() -> resolver.resolve(duplicated))
                .isInstanceOf(BusinessException.class);
    }

    private ClientIpResolver resolver(String... trusted) {
        return new ClientIpResolver(
                new IpGuardProperties(
                        true,
                        20,
                        Duration.ofMinutes(10),
                        120,
                        Duration.ofMinutes(1),
                        "test:guard",
                        List.of(trusted)));
    }

    private MockHttpServletRequest request(String peer, String forwarded) {
        var request = new MockHttpServletRequest();
        request.setRemoteAddr(peer);
        request.addHeader("X-Forwarded-For", forwarded);
        return request;
    }
}
