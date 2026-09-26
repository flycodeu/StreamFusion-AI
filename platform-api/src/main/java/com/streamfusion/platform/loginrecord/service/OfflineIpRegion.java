package com.streamfusion.platform.loginrecord.service;

import com.streamfusion.platform.loginrecord.config.LoginRecordProperties;
import java.io.RandomAccessFile;
import java.nio.file.Path;
import java.util.LinkedHashSet;
import java.util.concurrent.atomic.AtomicBoolean;
import org.lionsoul.ip2region.xdb.LongByteArray;
import org.lionsoul.ip2region.xdb.Searcher;
import org.lionsoul.ip2region.xdb.Version;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Component;

/** Entire immutable XDB buffers: concurrent queries perform no network or file I/O. */
@Component
public class OfflineIpRegion {
    private static final Logger LOG = LoggerFactory.getLogger(OfflineIpRegion.class);
    private static final int MAX_BYTES = 64 * 1024 * 1024;
    private final Searcher ipv4;
    private final Searcher ipv6;
    private final AtomicBoolean queryWarning = new AtomicBoolean();

    public OfflineIpRegion(LoginRecordProperties properties) {
        ipv4 = load(properties.ipv4Database(), Version.IPv4);
        ipv6 = load(properties.ipv6Database(), Version.IPv6);
        if (ipv4 == null || ipv6 == null) {
            LOG.warn(
                    "Offline IP region data incomplete; unavailable address families show unknown region");
        }
    }

    private Searcher load(String configuredPath, Version version) {
        if (configuredPath == null || configuredPath.isBlank()) return null;
        try (var file = new RandomAccessFile(Path.of(configuredPath).toFile(), "r")) {
            long length = file.length();
            if (length < Searcher.HeaderInfoLength + 256 * 256 * 8 || length > MAX_BYTES)
                return null;
            var header = Searcher.loadHeader(file);
            Searcher.verify(header, length);
            if (header.ipVersion != version.id) return null;
            // One bounded allocation per family; use the same open file and reject size changes.
            byte[] bytes = new byte[(int) length];
            file.seek(0);
            file.readFully(bytes);
            if (file.length() != length) return null;
            var content = new LongByteArray(bytes.length);
            content.append(bytes);
            return Searcher.newWithBuffer(version, content);
        } catch (Exception exception) {
            return null;
        }
    }

    public String lookup(byte[] address) {
        Searcher searcher = address.length == 4 ? ipv4 : ipv6;
        if (searcher == null) return null;
        try {
            String result = searcher.search(address);
            if (result == null || result.length() > 1024) return null;
            var parts = new LinkedHashSet<String>();
            for (String item : result.split("\\|")) {
                String value = item.strip();
                if (!value.isEmpty()
                        && !value.equals("0")
                        && !value.codePoints().anyMatch(Character::isISOControl)) parts.add(value);
            }
            String region = String.join(" / ", parts);
            return region.isBlank() || region.length() > 256 ? null : region;
        } catch (Exception exception) {
            if (queryWarning.compareAndSet(false, true))
                LOG.warn("Offline IP region lookup unavailable; showing unknown region");
            return null;
        }
    }
}
