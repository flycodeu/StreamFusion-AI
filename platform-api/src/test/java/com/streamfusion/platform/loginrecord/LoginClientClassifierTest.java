package com.streamfusion.platform.loginrecord;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.loginrecord.config.LoginRecordProperties;
import com.streamfusion.platform.loginrecord.service.LoginClientClassifier;
import com.streamfusion.platform.loginrecord.service.OfflineIpRegion;
import java.time.Duration;
import java.util.List;
import org.junit.jupiter.api.Test;

class LoginClientClassifierTest {
    @Test
    void classifiesLocalNetworksAndMappedIpv6WithoutSendingPrivateAddressesToGeo() {
        var geo = mock(OfflineIpRegion.class);
        var classifier =
                new LoginClientClassifier(
                        new LoginRecordProperties(
                                Duration.ofSeconds(60),
                                List.of(
                                        new LoginRecordProperties.Network("10.20.0.0/16", "研发内网"),
                                        new LoginRecordProperties.Network(
                                                "fd12:3456::/32", "研发IPv6")),
                                "",
                                ""),
                        geo);
        assertThat(classifier.region("10.20.1.3").name()).isEqualTo("研发内网");
        assertThat(classifier.region("fd12:3456::a").name()).isEqualTo("研发IPv6");
        assertThat(classifier.region("::ffff:192.168.1.2").type()).isEqualTo("PRIVATE");
        assertThat(classifier.region("127.0.0.1").type()).isEqualTo("LOOPBACK");
        assertThat(classifier.region("::1").type()).isEqualTo("LOOPBACK");
        assertThat(classifier.region("fe80::1234").type()).isEqualTo("LINK_LOCAL");
        assertThat(classifier.region("100.64.1.1").type()).isEqualTo("PRIVATE");
        assertThat(classifier.region("fdff::1").type()).isEqualTo("PRIVATE");
        assertThat(classifier.region("hostname.example").type()).isEqualTo("UNKNOWN");
        verifyNoInteractions(geo);
        assertThat(classifier.region("8.8.8.8").name()).isEqualTo("公网（地区未知）");
    }

    @Test
    void rejectsInvalidCidrsAndBoundsClientClassification() {
        assertThatThrownBy(() -> new LoginRecordProperties.Network("localhost/24", "名称"))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> new LoginRecordProperties.Network("192.168.0.0/33", "名称"))
                .isInstanceOf(IllegalArgumentException.class);
        assertThatThrownBy(() -> new LoginRecordProperties.Network("fd00::/129", "名称"))
                .isInstanceOf(IllegalArgumentException.class);
        var classifier =
                new LoginClientClassifier(
                        new LoginRecordProperties(Duration.ofSeconds(60), List.of(), "", ""),
                        mock(OfflineIpRegion.class));
        assertThat(classifier.client("Mozilla/5.0 (iPhone) Version/17.0 Safari/605.1").os())
                .isEqualTo("iOS");
        assertThat(classifier.client("Mozilla/5.0 Linux Firefox/123.0").browser())
                .isEqualTo("Firefox");
        assertThat(classifier.client("x".repeat(513) + "Chrome/1.0").browser()).isEqualTo("未知");
        assertThat(classifier.client(null).os()).isEqualTo("未知");
    }
}
