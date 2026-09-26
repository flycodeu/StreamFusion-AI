package com.streamfusion.platform.loginrecord;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.loginrecord.config.LoginRecordProperties;
import com.streamfusion.platform.loginrecord.service.OfflineIpRegion;
import java.net.InetAddress;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Callable;
import java.util.concurrent.Executors;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;

@EnabledIfSystemProperty(named = "sf.test.ipRegion", matches = "true")
class OfflineIpRegionDataTest {
    @Test
    void actualIpv4AndIpv6DataSupportConcurrentImmutableLookups() throws Exception {
        var geo =
                new OfflineIpRegion(
                        new LoginRecordProperties(
                                Duration.ofSeconds(60),
                                List.of(),
                                ".run/ip-region/ip2region_v4.xdb",
                                ".run/ip-region/ip2region_v6.xdb"));
        byte[] v4 = InetAddress.getByName("113.92.157.29").getAddress();
        byte[] v6 = InetAddress.getByName("240e:3b7:3272:d8d0:db09:c067:8d59:539e").getAddress();
        String ipv4 = geo.lookup(v4);
        String ipv6 = geo.lookup(v6);
        assertThat(ipv4).contains("中国");
        assertThat(ipv6).contains("中国");
        try (var executor = Executors.newFixedThreadPool(8)) {
            var calls = new ArrayList<Callable<Boolean>>();
            for (int i = 0; i < 200; i++)
                calls.add(() -> ipv4.equals(geo.lookup(v4)) && ipv6.equals(geo.lookup(v6)));
            for (var result : executor.invokeAll(calls)) assertThat(result.get()).isTrue();
        }
    }
}
