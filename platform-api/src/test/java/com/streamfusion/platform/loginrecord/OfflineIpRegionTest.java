package com.streamfusion.platform.loginrecord;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.loginrecord.config.LoginRecordProperties;
import com.streamfusion.platform.loginrecord.service.OfflineIpRegion;
import java.io.RandomAccessFile;
import java.net.InetAddress;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class OfflineIpRegionTest {
    @TempDir Path temporary;

    @Test
    void missingMalformedAndOversizedDataDegradeWithoutBlockingAuthentication() throws Exception {
        Path malformed = temporary.resolve("invalid.xdb");
        Files.write(malformed, new byte[1024]);
        var missing = new OfflineIpRegion(properties(malformed, temporary.resolve("missing.xdb")));
        assertThat(missing.lookup(InetAddress.getByName("8.8.8.8").getAddress())).isNull();
        Path oversized = temporary.resolve("large.xdb");
        try (var file = new RandomAccessFile(oversized.toFile(), "rw")) {
            file.setLength(64L * 1024 * 1024 + 1);
        }
        var large = new OfflineIpRegion(properties(oversized, malformed));
        assertThat(large.lookup(InetAddress.getByName("2604:a840:3::a04d").getAddress())).isNull();
    }

    private LoginRecordProperties properties(Path v4, Path v6) {
        return new LoginRecordProperties(
                Duration.ofSeconds(60), List.of(), v4.toString(), v6.toString());
    }
}
