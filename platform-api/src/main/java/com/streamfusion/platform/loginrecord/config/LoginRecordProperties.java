package com.streamfusion.platform.loginrecord.config;

import com.streamfusion.platform.auth.guard.IpAddresses;
import java.net.InetAddress;
import java.time.Duration;
import java.util.List;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;

@ConfigurationProperties(prefix = "platform.login-record", ignoreUnknownFields = false)
public record LoginRecordProperties(
        @DefaultValue("60s") Duration activityInterval,
        List<Network> localNetworks,
        @DefaultValue(".run/ip-region/ip2region_v4.xdb") String ipv4Database,
        @DefaultValue(".run/ip-region/ip2region_v6.xdb") String ipv6Database) {
    public LoginRecordProperties {
        if (activityInterval == null
                || activityInterval.compareTo(Duration.ofSeconds(10)) < 0
                || activityInterval.compareTo(Duration.ofMinutes(5)) > 0) {
            throw new IllegalArgumentException("Invalid login record activity interval");
        }
        localNetworks = localNetworks == null ? List.of() : List.copyOf(localNetworks);
        if (localNetworks.size() > 64)
            throw new IllegalArgumentException("Too many local networks");
    }

    public record Network(String cidr, String name) {
        public Network {
            if (cidr == null
                    || name == null
                    || name.isBlank()
                    || name.length() > 64
                    || name.codePoints().anyMatch(Character::isISOControl)) {
                throw new IllegalArgumentException("Invalid local network label");
            }
            try {
                String[] parts = cidr.split("/", -1);
                if (parts.length != 2) throw new IllegalArgumentException();
                String ip = IpAddresses.canonical(parts[0]);
                int bits = InetAddress.getByName(ip).getAddress().length * 8;
                int prefix = Integer.parseInt(parts[1]);
                if (prefix < 0 || prefix > bits) throw new IllegalArgumentException();
                cidr = ip + "/" + prefix;
                name = name.strip();
            } catch (Exception exception) {
                throw new IllegalArgumentException("Invalid local network CIDR", exception);
            }
        }
    }
}
