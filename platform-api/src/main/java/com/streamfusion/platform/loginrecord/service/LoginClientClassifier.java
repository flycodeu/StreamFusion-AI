package com.streamfusion.platform.loginrecord.service;

import com.streamfusion.platform.auth.guard.IpAddresses;
import com.streamfusion.platform.loginrecord.config.LoginRecordProperties;
import java.net.InetAddress;
import java.util.Locale;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.stereotype.Component;

/** Bounded local classification only; no geolocation service or raw User-Agent persistence. */
@Component
@EnableConfigurationProperties(LoginRecordProperties.class)
public class LoginClientClassifier {
    private final LoginRecordProperties properties;
    private final OfflineIpRegion offline;

    public LoginClientClassifier(LoginRecordProperties properties, OfflineIpRegion offline) {
        this.properties = properties;
        this.offline = offline;
    }

    public Region region(String sourceIp) {
        try {
            byte[] address = InetAddress.getByName(IpAddresses.canonical(sourceIp)).getAddress();
            for (var network : properties.localNetworks()) {
                String[] parts = network.cidr().split("/");
                byte[] subnet = InetAddress.getByName(parts[0]).getAddress();
                if (matches(address, subnet, Integer.parseInt(parts[1]))) {
                    return new Region("LOCAL_NETWORK", network.name());
                }
            }
            InetAddress parsed = InetAddress.getByAddress(address);
            if (parsed.isLoopbackAddress()) return new Region("LOOPBACK", "本机");
            if (parsed.isLinkLocalAddress()) return new Region("LINK_LOCAL", "链路本地");
            boolean uniqueLocal = address.length == 16 && (address[0] & 0xfe) == 0xfc;
            boolean shared =
                    address.length == 4 && (address[0] & 0xff) == 100 && (address[1] & 0xc0) == 64;
            if (parsed.isSiteLocalAddress() || uniqueLocal || shared) {
                return new Region("PRIVATE", "内网");
            }
            if (parsed.isAnyLocalAddress() || parsed.isMulticastAddress()) {
                return new Region("UNKNOWN", "未知");
            }
            String region = offline.lookup(address);
            return new Region("PUBLIC", region == null ? "公网（地区未知）" : region);
        } catch (Exception exception) {
            return new Region("UNKNOWN", "未知");
        }
    }

    public Client client(String header) {
        String ua = header == null ? "" : header.substring(0, Math.min(header.length(), 512));
        String lower = ua.toLowerCase(Locale.ROOT);
        String browser =
                lower.contains("edg/") || lower.contains("edga/") || lower.contains("edgios/")
                        ? "Edge"
                        : lower.contains("opr/") || lower.contains("opera")
                                ? "Opera"
                                : lower.contains("firefox/") || lower.contains("fxios/")
                                        ? "Firefox"
                                        : lower.contains("chrome/") || lower.contains("crios/")
                                                ? "Chrome"
                                                : lower.contains("safari/")
                                                                && lower.contains("version/")
                                                        ? "Safari"
                                                        : "未知";
        String os =
                lower.contains("android")
                        ? "Android"
                        : lower.contains("iphone") || lower.contains("ipad")
                                ? "iOS"
                                : lower.contains("windows")
                                        ? "Windows"
                                        : lower.contains("macintosh") || lower.contains("mac os x")
                                                ? "macOS"
                                                : lower.contains("linux") ? "Linux" : "未知";
        return new Client(browser, os);
    }

    private static boolean matches(byte[] address, byte[] subnet, int prefix) {
        if (address.length != subnet.length) return false;
        for (int index = 0; index < address.length; index++) {
            int bits = Math.min(8, Math.max(0, prefix - index * 8));
            int mask = bits == 0 ? 0 : 0xff << (8 - bits);
            if ((address[index] & mask) != (subnet[index] & mask)) return false;
        }
        return true;
    }

    public record Region(String type, String name) {}

    public record Client(String browser, String os) {}
}
